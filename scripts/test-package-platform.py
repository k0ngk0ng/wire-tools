#!/usr/bin/env python3
"""Reproduce actual release failures before changing packaging.

Failure scenarios: valid UTF-8 Go metadata contains bytes rejected by cp1252;
container checkout owner differs from its build user; dirty worktrees must still
be rejected after granting trust to that one repository.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import unittest
from unittest.mock import patch

import package as distribution

ROOT=Path(__file__).resolve().parents[1]

class PlatformPackaging(unittest.TestCase):
    def setUp(self):
        self.root=ROOT/'.cache/script-tests/package-platform'/self._testMethodName
        shutil.rmtree(self.root,ignore_errors=True)
        self.root.mkdir(parents=True)

    def test_utf8_go_metadata_works_with_cp1252_platform_default(self):
        for name in ('README.md','LICENSE','THIRD_PARTY_NOTICES.md','go/LICENSE'):
            file=self.root/name;file.parent.mkdir(parents=True,exist_ok=True);file.write_text('fixture',encoding='utf-8')
        for component in ('connect','talk'):
            for name in ('LICENSE','THIRD_PARTY_NOTICES.md'):
                file=self.root/'components'/component/name;file.parent.mkdir(parents=True,exist_ok=True);file.write_text('fixture',encoding='utf-8')
        dependency=self.root/'dependency';dependency.mkdir();(dependency/'LICENSE').write_text('fixture',encoding='utf-8')
        raw=json.dumps({'Doc':'right double quote: ”','Module':{'Path':'fixture/module','Version':'v1.0.0','Dir':str(dependency)}},ensure_ascii=False).encode('utf-8')
        real_output=subprocess.check_output
        def go_output(args,**kwargs):
            if args[:2]==['go','list']:
                return real_output([sys.executable,'-c','import sys; sys.stdout.buffer.write(bytes.fromhex('+repr(raw.hex())+'))'],**kwargs)
            if args==['go','env','GOROOT']:
                return str(self.root/'go')+'\n'
            raise AssertionError('unexpected command '+repr(args))
        stage=self.root/'stage';stage.mkdir()
        with patch.object(distribution,'ROOT',self.root),patch('package.subprocess.check_output',side_effect=go_output),patch('subprocess._text_encoding',return_value='cp1252'):
            distribution.notices(stage,dict(os.environ),False)
        inventory=json.loads((stage/'licenses/dependencies.json').read_text(encoding='utf-8'))
        self.assertEqual(inventory[0]['module'],'fixture/module')

    def git_fixture(self):
        hooks=self.root/'empty-hooks';hooks.mkdir()
        def git(*args):
            subprocess.run(['git','-c','init.defaultBranch=main','-C',str(self.root),*args],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        git('init')
        git('-c','user.name=Fixture','-c','user.email=fixture@example.invalid','-c','commit.gpgsign=false','-c','core.hooksPath='+str(hooks),'commit','--allow-empty','-m','fixture')
        # Keep the hooks directory ignored so the tracked checkout is clean.
        (self.root/'.git/info/exclude').write_text('/empty-hooks/\n',encoding='utf-8')
        return dict(os.environ,GIT_TEST_ASSUME_DIFFERENT_OWNER='1')

    def test_foreign_owner_checkout_is_trusted_only_for_release_commands(self):
        env=self.git_fixture()
        command=['git','-C',str(self.root),'status','--porcelain']
        before=subprocess.run(command,env=env,capture_output=True,text=True)
        trust_command=['git','config','--global','--get-all','safe.directory']
        trust_before=subprocess.run(trust_command,env=env,capture_output=True,text=True)
        with patch.object(distribution,'ROOT',self.root):
            self.assertEqual(distribution.release_sources(env),[])
        after=subprocess.run(command,env=env,capture_output=True,text=True)
        trust_after=subprocess.run(trust_command,env=env,capture_output=True,text=True)
        # Older Git ignores its test-only owner override. Preserve the same
        # trust outcome and config in either version; supported versions must
        # continue to reject the foreign owner outside the scoped command.
        self.assertEqual(after.returncode,before.returncode)
        self.assertEqual((trust_after.returncode,trust_after.stdout),(trust_before.returncode,trust_before.stdout))

    def test_dirty_foreign_owner_checkout_still_rejects_sources(self):
        env=self.git_fixture()
        (self.root/'uncommitted.go').write_text('package fixture\n',encoding='utf-8')
        with patch.object(distribution,'ROOT',self.root):
            with self.assertRaisesRegex(ValueError,'clean committed checkout'):
                distribution.release_sources(env)

if __name__=='__main__':
    result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(PlatformPackaging))
    evidence=ROOT/'.cache/evidence';evidence.mkdir(parents=True,exist_ok=True)
    (evidence/'package-platform-tests.json').write_text(json.dumps({'suite':'package-platform-failures','tests':result.testsRun,'successful':result.wasSuccessful()},indent=2)+'\n',encoding='utf-8')
    sys.exit(not result.wasSuccessful())
