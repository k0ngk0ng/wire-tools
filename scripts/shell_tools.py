"""Find a verified native Bash without selecting the Windows WSL launcher."""
import os
from pathlib import Path
import shutil
import subprocess


def find_bash(env=None, *, windows=None):
    env=dict(os.environ if env is None else env)
    windows=os.name=='nt' if windows is None else windows
    candidates=[]
    if windows:
        folded={name.casefold():value for name,value in env.items()}
        for name in ('ProgramFiles','ProgramW6432','ProgramFiles(x86)'):
            directory=folded.get(name.casefold())
            if directory:
                candidates.append(Path(directory)/'Git/bin/bash.exe')
                candidates.append(Path(directory)/'Git/usr/bin/bash.exe')
        git=shutil.which('git.exe',path=env.get('PATH',''))
        if git:
            # Portable Git can expose git.exe from cmd/, bin/ or mingw64/bin/.
            for parent in Path(git).parents:
                candidates.extend((parent/'bin/bash.exe',parent/'usr/bin/bash.exe'))
    else:
        bash=shutil.which('bash',path=env.get('PATH',''))
        if bash:
            candidates.append(Path(bash))
    seen=set()
    for candidate in candidates:
        if candidate in seen or not candidate.is_file():
            continue
        seen.add(candidate)
        if not os.access(candidate,os.X_OK):
            continue
        try:
            result=subprocess.run([str(candidate),'--noprofile','--norc','--version'],
                                  env=env,capture_output=True,text=True,timeout=10)
        except (OSError,subprocess.SubprocessError):
            continue
        if result.returncode==0 and result.stdout.startswith('GNU bash, version '):
            return str(candidate.resolve())
    if windows:
        raise FileNotFoundError('Git for Windows Bash was not found or could not execute; install Git for Windows (WSL is not required)')
    raise FileNotFoundError('a working Bash executable was not found on PATH')
