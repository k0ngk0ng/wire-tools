#!/usr/bin/env python3
"""Linux ABI gate scenarios, defined before implementation.

Accept compatible executable/runtime DSOs; reject a newer glibc requirement;
reject unreadable ELF version metadata; skip ordinary notices and data files.
"""
import json
from pathlib import Path
import shutil
import subprocess
import sys
import unittest
from unittest.mock import patch

import package as distribution

ROOT=Path(__file__).resolve().parents[1]

class LinuxABI(unittest.TestCase):
    def setUp(self):
        self.stage=ROOT/'.cache/script-tests/linux-abi'/self._testMethodName
        shutil.rmtree(self.stage,ignore_errors=True)
        self.stage.mkdir(parents=True)
        self.binary=self.stage/'bin/wirectl'
        self.binary.parent.mkdir()
        self.binary.write_bytes(b'\x7fELFfixture')
        (self.stage/'LICENSE').write_text('ordinary notice')

    def test_compatible_executable_and_runtime_dso_are_accepted(self):
        library=self.stage/'libexec/wirectl-download/lib/libstdc++.so.6'
        library.parent.mkdir(parents=True)
        library.write_bytes(b'\x7fELFfixture')
        with patch('package.subprocess.check_output',return_value='Name: GLIBC_2.34\nName: GLIBC_2.35\n') as readelf:
            report=distribution.verify_linux_abi(self.stage)
        self.assertEqual(report['baseline'],'2.35')
        self.assertEqual(report['required_glibc'],'2.35')
        self.assertEqual(report['elf_files'],2)
        self.assertEqual(readelf.call_count,2)

    def test_bookworm_glibc_requirement_is_rejected(self):
        with patch('package.subprocess.check_output',return_value='Name: GLIBC_2.36\n'):
            with self.assertRaisesRegex(ValueError,'2.36.*2.35'):
                distribution.verify_linux_abi(self.stage)

    def test_newer_packed_relocations_are_rejected(self):
        with patch('package.subprocess.check_output',return_value='Name: GLIBC_ABI_DT_RELR\nName: GLIBC_2.34\n'):
            with self.assertRaisesRegex(ValueError,'DT_RELR.*2.35'):
                distribution.verify_linux_abi(self.stage)

    def test_failed_elf_inspection_is_not_ignored(self):
        with patch('package.subprocess.check_output',side_effect=subprocess.CalledProcessError(1,['readelf'])):
            with self.assertRaises(subprocess.CalledProcessError):
                distribution.verify_linux_abi(self.stage)

    def test_static_executable_does_not_require_glibc(self):
        with patch('package.subprocess.check_output',return_value='No version information found.'):
            report=distribution.verify_linux_abi(self.stage)
        self.assertEqual(report['required_glibc'],'none')

if __name__=='__main__':
    result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(LinuxABI))
    evidence=ROOT/'.cache/evidence'
    evidence.mkdir(parents=True,exist_ok=True)
    (evidence/'linux-abi-tests.json').write_text(json.dumps({'suite':'linux-abi-gate','tests':result.testsRun,'successful':result.wasSuccessful()},indent=2)+'\n')
    sys.exit(not result.wasSuccessful())
