$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$manifests = @{}
foreach ($name in @('wirectl', 'wire-connect')) {
    $manifest = Get-Content -LiteralPath "$env:GITHUB_WORKSPACE/bucket/$name.json" -Raw | ConvertFrom-Json
    if ($manifest.version -notmatch '^\d+\.\d+\.\d+$') { throw "Invalid version for $name" }
    if ($manifest.architecture.'64bit'.hash -notmatch '^[a-f0-9]{64}$') { throw "Missing pinned checksum for $name" }
    $manifests[$name] = $manifest
}
& scoop bucket add k0ngk0ng https://github.com/k0ngk0ng/scoop-bucket
if ($LASTEXITCODE -ne 0) { throw 'Unable to add bucket' }
# Test staged updates, including artifacts not committed to the remote yet.
Copy-Item "$env:GITHUB_WORKSPACE/bucket/*.json" "$env:SCOOP/buckets/k0ngk0ng/bucket/" -Force
& scoop install k0ngk0ng/wire-connect
if ($LASTEXITCODE -ne 0) { throw 'Scoop installation failed' }
function Assert-Installation {
    $baseVersion = (& wirectl version).Trim()
    if ($LASTEXITCODE -ne 0 -or $baseVersion -ne $manifests['wirectl'].version) { throw 'Incorrect wirectl version' }
    $version = (& wirectl connect version).Trim()
    if ($LASTEXITCODE -ne 0 -or $version -ne $manifests['wire-connect'].version) { throw 'Plugin dispatch or version failed' }
    & wirectl connect --help | Out-Host
    if ($LASTEXITCODE -ne 0) { throw 'Plugin help failed' }
    $bin = "$env:SCOOP/apps/wire-connect/current/bin"
    if (!(Test-Path "$bin/wintun.dll" -PathType Leaf)) { throw 'Wintun runtime is missing' }
    $marker = "$bin/.wire-connect-package-manager"
    if ([Convert]::ToHexString([IO.File]::ReadAllBytes($marker)) -cne '73636F6F70') { throw 'Marker must be plain scoop without a BOM' }
    $output = @(& wirectl connect update 2>&1)
    if ($LASTEXITCODE -ne 1 -or ($output -join "`n") -notmatch 'scoop update wire-connect') { throw "Managed updater failed: exit=$LASTEXITCODE; output=$($output -join [Environment]::NewLine)" }
    $global:LASTEXITCODE = 0
}
Assert-Installation
# Force the package replacement path even for the initial published version.
& scoop update wire-connect --force
if ($LASTEXITCODE -ne 0) { throw 'Scoop forced update failed' }
Assert-Installation
& scoop uninstall wire-connect wirectl
if ($LASTEXITCODE -ne 0) { throw 'Scoop uninstall failed' }
