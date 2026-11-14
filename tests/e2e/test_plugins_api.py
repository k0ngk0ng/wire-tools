#!/usr/bin/env python3
"""Real process/API/UDP hardware-plugin acceptance; SDK hardware is simulated."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import unittest
import urllib.error
import urllib.request

sys.dont_write_bytecode = True
from test_talk_api import Runtime, available_room_address, wait_for

ROOT = Path(__file__).resolve().parents[2]
BINARY = Path(os.environ.get('WIRECTL_TEST_BINARY', ROOT / 'bin/wirectl')).resolve()
SCRATCH = ROOT / '.cache/e2e/plugins-process'
EVIDENCE = ROOT / '.cache/evidence'
PROOF = []


class PluginRuntime(Runtime):
    def root_request(self, method, path, body=None):
        ep = self.endpoint()
        payload = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request('http://' + ep['address'] + '/v1' + path,
                                     data=payload, method=method,
                                     headers={'Content-Type': 'application/json',
                                              'Authorization': 'Bearer ' + ep['token']})
        try:
            with self.opener.open(req, timeout=15) as r:
                data = r.read()
                return json.loads(data) if data else None
        except urllib.error.HTTPError as e:
            raise AssertionError('HTTP %d %s' % (e.code, e.read(4096).decode())) from None

    def plugin(self, name):
        return self.root_request('GET', '/plugins/' + name)


class PluginsProcesses(unittest.TestCase):
    def setUp(self):
        self.path = SCRATCH / self._testMethodName
        shutil.rmtree(self.path, ignore_errors=True)
        self.path.mkdir(parents=True)
        self.a, self.b = PluginRuntime(self.path / 'a'), PluginRuntime(self.path / 'b')
        self.buttons = self.path / 'buttons.json'
        self.trace = self.path / 'sdk-trace.jsonl'
        self.keys = self.path / 'keys.jsonl'
        self.keys.touch()
        self.state()
        self.b.env.update(WIRECTL_TEST_SAKSHAT_SDK=str(ROOT / 'tests/fixtures/sakshat_sdk'),
                          WIRECTL_TEST_SAKSHAT_PYTHON=sys.executable,
                          WIRECTL_TEST_SAKSHAT_BUTTONS=str(self.buttons),
                          WIRECTL_TEST_SAKSHAT_TRACE=str(self.trace),
                          WIRE_TALK_TEST_HANDSET_EVENTS=str(self.keys))

    def tearDown(self):
        self.a.close()
        self.b.close()

    def state(self, **values):
        data = {'16': 1, '20': 1, **values}
        temp = self.buttons.with_suffix('.new')
        temp.write_text(json.dumps(data))
        temp.replace(self.buttons)

    def press(self, pin):
        self.state(**{str(pin): 0})
        time.sleep(.16)
        self.state()
        time.sleep(.12)

    def key(self, value, usage=205):
        with self.keys.open('a') as f:
            f.write(json.dumps({'usage_page': 12, 'usage': usage, 'value': value}) + '\n')

    def pair(self):
        self.a.start(); self.b.start()
        address = available_room_address()
        self.a.request('POST', '/config', {'listen': address, 'input': 'none', 'mode': 'direct'})
        self.a.request('POST', '/session', {})
        invitation = self.a.request('POST', '/invitations', {})
        self.b.request('POST', '/pair', {'address': invitation['address'], 'code': invitation['code'],
                                       'listen': available_room_address(), 'input': 'none'})
        self.b.cli('talk', 'join')
        for node in (self.a, self.b):
            wait_for(node.status, lambda s: len(s.get('peers', [])) == 1, 'peer discovery')

    def test_catalog_cli_and_configuration_rejections(self):
        help_text = self.a.cli('plugin', '--help')
        self.assertIn('sakshat', help_text)
        self.assertFalse((self.a.home / 'api.json').exists())
        rows = json.loads(self.a.cli('plugin', 'list', '--json'))
        self.assertEqual({x['name'] for x in rows}, {'sakshat', 'spacetouch'})
        self.assertTrue(all(not x['enabled'] for x in rows))
        for method, path, body, code in (
            ('POST', '/plugins/unknown', {'enabled': True}, 404),
            ('POST', '/plugins/sakshat', {'enabled': 'yes'}, 400),
            ('POST', '/plugins/sakshat', {'pin': 12}, 400),
        ):
            with self.assertRaisesRegex(AssertionError, 'HTTP ' + str(code)):
                self.a.root_request(method, path, body)
        self.assertFalse(self.a.plugin('sakshat')['enabled'])
        PROOF.append({'test': self._testMethodName, 'default_disabled': True})

    def test_sakshat_buttons_display_and_isolation(self):
        self.pair()
        self.b.cli('plugin', 'enable', 'sakshat')
        ready = wait_for(lambda: self.b.plugin('sakshat'), lambda s: s['online'], 'SDK ready')
        self.assertFalse(ready['display']['visible'])
        self.assertNotIn('handset', self.b.status(), 'Talk still owns a hardware listener')
        # Hold: one call only; releasing must not cancel it.
        self.state(**{'16': 0})
        wait_for(self.a.status, lambda s: s['call']['state'] == 'ringing', 'S1 outgoing call')
        time.sleep(.4)
        self.assertEqual(self.b.status()['call']['state'], 'dialing')
        self.state()
        wait_for(lambda: self.b.plugin('sakshat'), lambda s: s['display']['blinking'], 'dialing display')
        time.sleep(1.3)
        rows=[json.loads(x) for x in self.trace.read_text().splitlines()]
        displays=[x for x in rows if x['operation']=='display']
        self.assertTrue(any(x['visible'] and x['text']=='6702' for x in displays))
        self.assertTrue(any(not x['visible'] for x in displays))
        self.a.cli('talk','call','answer')
        active=wait_for(lambda:self.b.plugin('sakshat'),lambda s:s['call_state']=='active' and s['display']['visible'] and not s['display']['blinking'],'steady display')
        self.press(20)
        wait_for(self.a.status,lambda s:s['call']['state']=='idle','S2 hangup')
        wait_for(lambda:self.b.plugin('sakshat'),lambda s:not s['display']['visible'],'idle blank display')
        # Incoming call: the same S1 answers, S2 cancels/hangs up.
        self.a.cli('talk','call','dial')
        wait_for(self.b.status,lambda s:s['call']['state']=='ringing','incoming call')
        self.press(16)
        wait_for(self.a.status,lambda s:s['call']['state']=='active','S1 incoming answer')
        self.state(fail=True)
        wait_for(lambda:self.b.plugin('sakshat'),lambda s:not s['online'] and bool(s.get('error')),'hardware failure reported')
        self.assertEqual(self.a.status()['call']['state'],'active')
        self.state()
        wait_for(lambda:self.b.plugin('sakshat'),lambda s:s['online'],'SDK reconnect',timeout=20)
        self.assertEqual(self.a.status()['call']['state'],'active')
        self.b.cli('plugin','disable','sakshat')
        self.assertEqual(self.a.status()['call']['state'],'active','plugin stop ended the call')
        self.a.cli('talk','call','hangup')
        wait_for(self.b.status,lambda s:s['call']['state']=='idle','call cleanup')
        rows=[json.loads(x) for x in self.trace.read_text().splitlines()]
        self.assertTrue(any(x['operation']=='cleanup' for x in rows))
        self.assertFalse([x for x in rows if x['operation']=='display'][-1]['visible'])
        self.assertEqual({x['pin'] for x in rows if x['operation']=='setup'},{5,25,16,20})
        PROOF.append({'test':self._testMethodName,'active':active,'sdk_trace':str(self.trace),
                      'hardware_loss_did_not_end_call':True,'only_selected_pins':True})

    def test_held_at_start_restart_and_simultaneous_buttons(self):
        self.pair()
        self.state(**{'16':0})
        self.b.cli('plugin','enable','sakshat')
        wait_for(lambda:self.b.plugin('sakshat'),lambda s:s['online'],'SDK ready')
        time.sleep(.3)
        self.assertEqual(self.a.status()['call']['state'],'idle')
        self.state();time.sleep(.15)
        self.press(16)
        wait_for(self.a.status,lambda s:s['call']['state']=='ringing','fresh press after release')
        self.press(20)
        wait_for(self.a.status,lambda s:s['call']['state']=='idle','cancel outgoing')
        self.state(**{'16':0,'20':0});time.sleep(.3)
        self.assertEqual(self.a.status()['call']['state'],'idle','both buttons caused a dial')
        self.state(**{'16':0})
        self.b.close()
        self.b.start()
        restored=wait_for(lambda:self.b.plugin('sakshat'),lambda s:s['enabled'] and s['online'],'enabled plugin restored')
        self.assertFalse(self.b.status()['active'],'plugin started an audio session at boot')
        self.assertFalse(restored['display']['visible'])
        PROOF.append({'test':self._testMethodName,'restart_did_not_dial':True,'held_start_ignored':True})

    def test_spacetouch_is_optional_and_uses_talk_api(self):
        self.pair()
        # An unenabled plugin must not consume physical-key fixtures.
        self.key(1);self.key(0);time.sleep(.15)
        self.assertEqual(self.a.status()['call']['state'],'idle')
        self.b.cli('plugin','enable','spacetouch')
        wait_for(lambda:self.b.plugin('spacetouch'),lambda s:s['online'],'USB plugin ready')
        self.key(1);self.key(1)
        wait_for(self.a.status,lambda s:s['call']['state']=='ringing','play key dials')
        self.key(0)
        self.a.cli('talk','call','answer')
        wait_for(self.b.status,lambda s:s['call']['state']=='active','USB plugin active call')
        self.key(1);self.key(0)
        wait_for(self.a.status,lambda s:s['call']['state']=='idle','play key hangs up')
        self.key(1,233);self.key(0,233)
        wait_for(self.b.status,lambda s:s['output_gain_db']==2,'volume key via API')
        self.b.cli('plugin','disable','spacetouch')
        self.key(1);self.key(0);time.sleep(.2)
        self.assertEqual(self.a.status()['call']['state'],'idle')
        PROOF.append({'test':self._testMethodName,'explicit_enable_required':True})


if __name__=='__main__':
    SCRATCH.mkdir(parents=True,exist_ok=True);EVIDENCE.mkdir(parents=True,exist_ok=True)
    suite=(unittest.defaultTestLoader.loadTestsFromNames(sys.argv[1:],sys.modules[__name__])
           if len(sys.argv)>1 else unittest.defaultTestLoader.loadTestsFromTestCase(PluginsProcesses))
    result=unittest.TextTestRunner(verbosity=2).run(suite)
    report={'suite':'hardware-plugins-process-e2e','tests':result.testsRun,'successful':result.wasSuccessful(),
            'hardware':'explicit test SDK/GPIO and HID backend; no physical display observation',
            'proof':PROOF,'failures':[{'test':str(t),'detail':d} for t,d in result.failures],
            'errors':[{'test':str(t),'detail':d} for t,d in result.errors]}
    (EVIDENCE/'plugins-process-e2e.json').write_text(json.dumps(report,indent=2)+'\n')
    sys.exit(0 if result.wasSuccessful() else 1)
