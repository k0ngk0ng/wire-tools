#!/usr/bin/env python3
"""Platform-selection checks, before changing shell launch behavior.

Failure scenarios: Windows PATH resolves WSL, Git lives under a path with spaces,
portable Git is discoverable only through git.exe, a candidate cannot execute,
version output is not GNU Bash, and no native Git Bash installation is present.
All filesystem fixtures remain inside this repository.
"""
import json
import os
from pathlib import Path
import shutil
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from shell_tools import find_bash

ROOT=Path(__file__).resolve().parents[1]

class BashSelection(unittest.TestCase):
    def setUp(self):
        self.dir=ROOT/'.cache/script-tests/bash-selection'/self._testMethodName
        shutil.rmtree(self.dir,ignore_errors=True)
        self.dir.mkdir(parents=True)
        self.wsl=self.fixture('Windows/System32/bash.exe')
        self.env={'PATH':str(self.wsl.parent)}

    def fixture(self,path):
        p=self.dir/path
        p.parent.mkdir(parents=True,exist_ok=True)
        p.write_bytes(b'executable selection fixture')
        p.chmod(0o755)
        return p

    def version(self,args,**kwargs):
        return SimpleNamespace(returncode=0,stdout='GNU bash, version 5.2.37(1)-release\n',stderr='')

    def test_windows_prefers_program_files_git_bash_over_path_wsl(self):
        git_bash=self.fixture('Program Files/Git/bin/bash.exe')
        self.env['ProgramFiles']=str(self.dir/'Program Files')
        with patch('shell_tools.shutil.which',return_value=str(self.wsl)),patch('shell_tools.subprocess.run',side_effect=self.version) as run:
            self.assertEqual(Path(find_bash(self.env,windows=True)),git_bash)
        self.assertEqual(Path(run.call_args.args[0][0]),git_bash)

    def test_portable_git_is_resolved_relative_to_git_executable(self):
        git=self.fixture('portable-git/mingw64/bin/git.exe')
        bash=self.fixture('portable-git/usr/bin/bash.exe')
        def which(name,**kwargs):
            return str(git) if name=='git.exe' else str(self.wsl)
        with patch('shell_tools.shutil.which',side_effect=which),patch('shell_tools.subprocess.run',side_effect=self.version):
            self.assertEqual(Path(find_bash(self.env,windows=True)),bash)

    def test_invalid_version_candidate_is_rejected_before_using_next(self):
        bad=self.fixture('bad/Git/bin/bash.exe')
        good=self.fixture('good/Git/bin/bash.exe')
        self.env.update(ProgramFiles=str(self.dir/'bad'),ProgramW6432=str(self.dir/'good'))
        def version(args,**kwargs):
            if Path(args[0])==bad:
                return SimpleNamespace(returncode=1,stdout='WSL distribution is missing',stderr='')
            return self.version(args,**kwargs)
        with patch('shell_tools.shutil.which',return_value=None),patch('shell_tools.subprocess.run',side_effect=version):
            self.assertEqual(Path(find_bash(self.env,windows=True)),good)

    def test_cannot_execute_candidate_is_rejected(self):
        self.fixture('Program Files/Git/bin/bash.exe')
        self.env['ProgramFiles']=str(self.dir/'Program Files')
        with patch('shell_tools.shutil.which',return_value=None),patch('shell_tools.subprocess.run',side_effect=OSError('not executable')):
            with self.assertRaises(FileNotFoundError):
                find_bash(self.env,windows=True)

    def test_missing_git_bash_does_not_fall_back_to_wsl(self):
        with patch('shell_tools.shutil.which',return_value=str(self.wsl)),patch('shell_tools.subprocess.run') as run:
            with self.assertRaisesRegex(FileNotFoundError,'Git for Windows'):
                find_bash(self.env,windows=True)
        run.assert_not_called()

if __name__=='__main__':
    result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(BashSelection))
    evidence=ROOT/'.cache/evidence'
    evidence.mkdir(parents=True,exist_ok=True)
    (evidence/'shell-selection.json').write_text(json.dumps({'suite':'native-bash-selection','tests':result.testsRun,'successful':result.wasSuccessful(),'failures':[str(t) for t,_ in result.failures],'errors':[str(t) for t,_ in result.errors]},indent=2)+'\n')
    sys.exit(not result.wasSuccessful())
