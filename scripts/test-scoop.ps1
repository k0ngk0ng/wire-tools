# Run only in a disposable GitHub-hosted runner; all Scoop state stays in this checkout.
$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
if ($env:GITHUB_ACTIONS -ne 'true') { throw 'Run this installation test in a disposable CI runner' }
$root = $env:GITHUB_WORKSPACE
$env:SCOOP = [IO.Path]::GetFullPath("$root/.cache/scoop")
New-Item -ItemType Directory -Path "$root/.cache/scoop-manifest" -Force | Out-Null
Invoke-WebRequest https://get.scoop.sh -OutFile "$root/.cache/install-scoop.ps1"
& "$root/.cache/install-scoop.ps1" -RunAsAdmin -ScoopDir $env:SCOOP
if ($LASTEXITCODE -and $LASTEXITCODE -ne 0) { throw 'Scoop bootstrap failed' }
$env:PATH = "$env:SCOOP/shims;$env:PATH"
$manifest = Get-Content "$root/.cache/release/wirectl.json" -Raw | ConvertFrom-Json
$listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = $listener.LocalEndpoint.Port
$listener.Stop()
$server = Start-Process python -ArgumentList '-m', 'http.server', "$port", '--bind', '127.0.0.1', '--directory', "$root/.cache/release" -PassThru -RedirectStandardOutput "$root/.cache/scoop-http.log" -RedirectStandardError "$root/.cache/scoop-http-error.log"
try {
    $filename = "wirectl-$env:VERSION-windows-amd64.zip"
    $manifest.architecture.'64bit'.url = "http://127.0.0.1:$port/$filename"
    $manifest | ConvertTo-Json -Depth 15 | Set-Content "$root/.cache/scoop-manifest/wirectl.json" -Encoding utf8
    $ready = $false
    for ($i = 0; $i -lt 100; $i++) {
        try {
            Invoke-WebRequest "http://127.0.0.1:$port/" -TimeoutSec 1 | Out-Null
            $ready = $true
            break
        } catch { Start-Sleep -Milliseconds 100 }
    }
    if (!$ready) { throw 'Local archive server did not become ready' }
    & scoop install "$root/.cache/scoop-manifest/wirectl.json"
    if ($LASTEXITCODE -ne 0) { throw 'Scoop installation failed' }
    $binary = "$env:SCOOP/apps/wirectl/current/bin/wirectl.exe"
    if ((& $binary version).Trim() -ne $env:VERSION) { throw 'Incorrect installed version' }
    if (!(Test-Path "$env:SCOOP/apps/wirectl/current/bin/wintun.dll")) { throw 'Missing Wintun runtime' }
    $marker = [IO.File]::ReadAllText("$env:SCOOP/apps/wirectl/current/bin/.wirectl-package-manager")
    if ($marker.Trim() -ne 'scoop') { throw 'Incorrect package manager marker' }
    & $binary --help | Out-Host
    if ($LASTEXITCODE -ne 0) { throw 'Installed help failed' }
    & scoop uninstall wirectl
    if ($LASTEXITCODE -ne 0) { throw 'Scoop uninstall failed' }
} finally {
    Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
}
