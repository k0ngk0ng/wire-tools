#!/usr/bin/env python3
"""Exercise multi-room CLI and encrypted UDP isolation with test-only null audio."""
import json, math, os, re, struct, subprocess, tempfile, time, wave
from pathlib import Path
root=Path(__file__).resolve().parents[1]
env=dict(os.environ)
for var,folder in [('GOCACHE','go-build'),('GOMODCACHE','go-mod'),('GOTMPDIR','tmp'),('TMPDIR','tmp')]:
    dest=root/'.cache'/folder;dest.mkdir(parents=True,exist_ok=True);env[var]=str(dest)
exe=root/'.cache'/('groups-test.exe' if os.name=='nt' else 'groups-test')
subprocess.run(['go','build','-tags','talk_test_audio','-o',str(exe),'./cmd/wirectl-talk'],cwd=root,env=env,check=True)
with tempfile.TemporaryDirectory(dir=root/'.cache') as directory:
    base=Path(directory);processes=[];handles=[]
    def call(node,*args,check=True):
        r=subprocess.run([str(exe),'--state-dir',str(base/node),*args],env=env,text=True,capture_output=True,timeout=20)
        if check and r.returncode:raise AssertionError((node,args,r.stdout,r.stderr))
        return r
    def groups(node):return json.loads(call(node,'group','list','--json').stdout)
    def group(node,name):return json.loads(call(node,'group','status',name,'--json').stdout)
    def wait(predicate,message):
        until=time.monotonic()+10
        while time.monotonic()<until:
            if predicate():return
            time.sleep(.1)
        raise AssertionError(message)
    def invite(node,ref):
        path=base/f'invite-{len(processes)}.log';handle=path.open('w');handles.append(handle)
        p=subprocess.Popen([str(exe),'--state-dir',str(base/node),'group','invite',ref],env=env,stdout=handle,stderr=handle);processes.append(p)
        wait(lambda:re.search(r'Pairing code: (\d{6})',path.read_text()),'no invitation code')
        code=re.search(r'Pairing code: (\d{6})',path.read_text())[1]
        port=group(node,ref)['listen'].rsplit(':',1)[1]
        return p,'127.0.0.1:'+port,code
    def tone(path,hz):
        with wave.open(str(path),'wb') as f:
            f.setparams((1,2,16000,0,'NONE','not compressed'))
            f.writeframes(b''.join(struct.pack('<h',int(6000*math.sin(2*math.pi*hz*i/16000))) for i in range(16000)))
    def rms(path):
        with wave.open(str(path),'rb') as f:p=f.readframes(f.getnframes())
        values=struct.unpack('<'+'h'*(len(p)//2),p)[1600:]
        assert len(values)>8000
        return math.sqrt(sum(v*v for v in values)/len(values))
    def record_pair(label):
        a,b=base/(label+'-a.wav'),base/(label+'-b.wav')
        call('a','record','start',str(a));call('b','record','start',str(b));time.sleep(1.2)
        call('a','record','stop');call('b','record','stop')
        return rms(a),rms(b)
    try:
        call('hub','init','--listen','127.0.0.1:0','--input','none','--headphones')
        old=json.loads((base/'hub'/'config.json').read_text())
        first=groups('hub')['current']
        call('hub','group','rename',first,'alpha')
        call('hub','daemon','start')
        view=call('hub','group','levels').stdout
        assert view.count('Room alpha')==1 and 'Self (mic disabled)' in view and '\x1b' not in view,view
        call('hub','group','create','beta','--listen','127.0.0.1:0')
        beta=group('hub','beta')['room_id']
        assert groups('hub')['current']==first,'adding room unexpectedly changed microphone target'
        invitation,address,code=invite('hub','alpha')
        call('a','group','join',address,'--code',code,'--name','alpha','--listen','127.0.0.1:0')
        assert invitation.wait(timeout=5)==0
        assert group('a','alpha')['room_id']==first,'paired room IDs differ'
        call('a','daemon','start')
        # A second member adds beta while its existing background room is running.
        call('b','init','--listen','127.0.0.1:0','--input','none','--headphones')
        call('b','daemon','start')
        invitation,address,code=invite('hub','beta')
        call('b','group','join',address,'--code',code,'--name','beta','--listen','127.0.0.1:0')
        assert invitation.wait(timeout=5)==0
        call('b','group','use','beta')
        wait(lambda:len(group('hub','alpha')['peers'])==1 and len(group('hub','beta')['peers'])==1,'members missing from simultaneous rooms')
        peer=group('hub','alpha')['peers'][0]
        call('hub','volume','output','+6')
        call('hub','volume','peer',peer['id'],'+9','--group','alpha')
        settings=json.loads(call('hub','volume','--group','alpha','--json').stdout)
        assert settings['output_gain_db']==6 and list(settings['peer_gains'].values())==[9],settings
        assert call('hub','volume','output','NaN',check=False).returncode!=0
        assert call('hub','volume','output','25',check=False).returncode!=0
        call('hub','volume','output','-3')
        assert groups('hub')['output_gain_db']==-3
        f=base/'tone.wav';tone(f,440)
        call('hub','input','start',str(f),'--loop')
        assert 'Self (file input)' in call('hub','group','levels').stdout
        time.sleep(.3)
        a,b=record_pair('alpha')
        assert a>500 and b<1,(a,b,'audio leaked from alpha to beta')
        call('hub','group','use','beta')
        assert group('hub','alpha')['media']['file_input']['state']=='stopped','old file input kept running after switch'
        assert call('hub','input','start',str(f),'--group','alpha',check=False).returncode!=0,'allowed sending file to non-current room'
        call('hub','input','start',str(f),'--loop')
        time.sleep(.3)
        a,b=record_pair('beta')
        assert b>500 and a<1,(a,b,'audio leaked from beta to alpha')
        call('hub','input','stop')
        call('hub','group','mute','alpha')
        assert not group('hub','alpha')['listening'] and group('hub','beta')['listening']
        # Recordings stay bound to their room after speaking target changes.
        rec=base/'alpha-recording.wav'
        call('hub','record','start',str(rec),'--group','alpha')
        assert json.loads(call('hub','record','status','--group','alpha','--json').stdout)['state']=='recording'
        call('hub','record','stop','--group','alpha')
        watch=subprocess.Popen([str(exe),'--state-dir',str(base/'hub'),'group','watch','beta'],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True);processes.append(watch)
        time.sleep(.3);call('b','daemon','stop');time.sleep(7)
        watch.terminate();output,_=watch.communicate(timeout=5)
        assert 'Left:' in output and 'Others:     0 online' in output,output
        call('hub','daemon','stop')
        offline=groups('hub')
        assert offline['current']==beta and len(offline['groups'])==2
        assert all(not g['online'] for g in offline['groups'])
        call('hub','daemon','start')
        assert groups('hub')['current']==beta and not group('hub','alpha')['listening']
        assert groups('hub')['output_gain_db']==-3 and list(group('hub','alpha')['peer_gains'].values())==[9], 'gain did not survive restart'
        saved=json.loads((base/'hub'/'config.json').read_text())
        assert saved['key']==old['key'] and saved['input']=='none','legacy credentials/devices changed'
        public=call('hub','group','list','--json').stdout
        assert old['key'] not in public and saved['groups'][0]['key'] not in public,'room secret exposed'
        call('hub','group','leave','alpha')
        assert len(groups('hub')['groups'])==1 and groups('hub')['current']==beta
        assert call('hub','group','leave','beta',check=False).returncode!=0
        print('PASS: multi-room IDs, online create/join, microphone/file isolation, listening, room recording, member watch, persistence and legacy migration')
    finally:
        for node in ['hub','a','b']:call(node,'daemon','stop',check=False)
        for p in processes:
            if p.poll() is None:p.kill();p.wait(timeout=5)
        for h in handles:h.close()
