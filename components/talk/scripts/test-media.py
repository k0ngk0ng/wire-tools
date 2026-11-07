#!/usr/bin/env python3
"""Real UDP media integration using the native null device, never user hardware."""
import base64
import json
import math
import os
from pathlib import Path
import struct
import subprocess
import tempfile
import time
import wave

root=Path(__file__).resolve().parents[1]
env=dict(os.environ)
for var,folder in [('GOCACHE','go-build'),('GOMODCACHE','go-mod'),('GOTMPDIR','tmp'),('TMPDIR','tmp')]:
    dest=root/'.cache'/folder;dest.mkdir(parents=True,exist_ok=True);env[var]=str(dest)
exe=root/'.cache'/('media-test.exe' if os.name=='nt' else 'media-test')
subprocess.run(['go','build','-tags','talk_test_audio','-o',str(exe),'./cmd/wirectl-talk'],cwd=root,env=env,check=True)
with tempfile.TemporaryDirectory(dir=root/'.cache') as tmp:
    d=Path(tmp); processes=[]
    key=d/'room.key';key.write_text(base64.b64encode(b'1'*32).decode().rstrip('='));key.chmod(0o600)
    def run(node,*args,check=True):
        return subprocess.run([str(exe),'--state-dir',str(d/node),*args],env=env,text=True,capture_output=True,timeout=20,check=check)
    def status(node):return json.loads(run(node,'status','--json').stdout)
    def wait(predicate,message):
        until=time.monotonic()+8
        while time.monotonic()<until:
            if predicate():return
            time.sleep(.05)
        raise AssertionError(message)
    def start(node,peers=None):
        args=['init','--listen','127.0.0.1:0','--input','none','--headphones','--key-file',str(key)]
        if peers:args+=['--peers',peers]
        run(node,*args)
        p=subprocess.Popen([str(exe),'--state-dir',str(d/node),'join'],env=env,stdout=subprocess.DEVNULL,stderr=subprocess.PIPE)
        processes.append((node,p))
        wait(lambda:run(node,'status','--json',check=False).returncode==0,f'{node} failed to start')
    def tone(path,hz):
        with wave.open(str(path),'wb') as f:
            f.setnchannels(1);f.setsampwidth(2);f.setframerate(16000)
            f.writeframes(b''.join(struct.pack('<h',int(5000*math.sin(2*math.pi*hz*i/16000))) for i in range(16000)))
    def magnitude(path,hz):
        with wave.open(str(path),'rb') as f:
            assert f.getframerate()==16000 and f.getnchannels()==1
            data=f.readframes(f.getnframes())
        values=struct.unpack('<'+'h'*(len(data)//2),data)
        assert len(values)>5000,'recording too short'
        # Evaluate the middle of the recording, after jitter-buffer warmup.
        values=values[3200:-1600]
        # UDP jitter can insert/drop an entire 20ms frame. A single coherent
        # transform over the whole recording then cancels audible tones with
        # different phases. Measure each audio frame and average its power.
        window=[0.5-0.5*math.cos(2*math.pi*i/319) for i in range(320)]
        normalization=sum(window)
        powers=[]
        for start in range(0,len(values)-319,320):
            block=values[start:start+320]
            re=sum(v*w*math.cos(2*math.pi*hz*i/16000) for i,(v,w) in enumerate(zip(block,window)))
            im=sum(v*w*math.sin(2*math.pi*hz*i/16000) for i,(v,w) in enumerate(zip(block,window)))
            powers.append((re*re+im*im)/(normalization*normalization))
        return math.sqrt(sum(powers)/len(powers))
    try:
        start('receiver');address=status('receiver')['listen']
        start('one',address);start('two',address)
        wait(lambda:len(status('receiver')['peers'])==2,'peer discovery')
        one_id=status('one')['id']
        f1=d/'one.wav';f2=d/'two.wav';tone(f1,440);tone(f2,880)
        run('receiver','mute','output')
        run('one','mute','input') # File source must still be clocked while microphone is muted.
        run('one','input','start',str(f1),'--loop')
        run('two','input','start',str(f2),'--loop')
        time.sleep(.3)
        wait(lambda:status('one')['output_device']['level']['rms']>0,'live output level')
        assert status('receiver')['output_device']['level']['rms']==0,'muted output meter was not silent'
        meter=subprocess.Popen([str(exe),'--state-dir',str(d/'one'),'levels'],env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        time.sleep(.5)
        meter.terminate()
        meter_out,meter_err=meter.communicate(timeout=5)
        assert 'Input [' in meter_out and 'Output [#' in meter_out and 'dBFS' in meter_out,(meter_out,meter_err)
        all_file=d/'all.wav'
        run('receiver','record','start',str(all_file));time.sleep(1.5);run('receiver','record','stop')
        levels=(magnitude(all_file,440),magnitude(all_file,880))
        assert min(levels)>500, f'mixed recording missing speaker: {levels}'
        selected=d/'selected.wav'
        run('receiver','record','start',str(selected),'--peer',one_id);time.sleep(1.5);run('receiver','record','stop')
        assert magnitude(selected,440)>500,'selected peer missing'
        assert magnitude(selected,880)<100,'other peer leaked into selected recording'
        assert run('receiver','record','start',str(selected),check=False).returncode!=0,'overwrote recording'
        run('one','input','pause');count=status('one')['media']['file_input']['frames'];time.sleep(.2)
        assert status('one')['media']['file_input']['frames']==count,'paused source consumed frames'
        run('one','input','resume');wait(lambda:status('one')['media']['file_input']['frames']>count,'resume failed')
        run('one','input','stop');run('two','input','stop')
        assert status('one')['media']['file_input']['state']=='stopped'
        run('one','input','start',str(f1),'--mode','mix')
        wait(lambda:status('one')['media']['file_input']['state']=='ended','EOF not reported')
        assert 'Recording:' in run('receiver','status').stdout
        assert json.loads(run('receiver','record','status','--json').stdout)['state']=='stopped'
        assert json.loads(run('one','input','status','--json').stdout)['state']=='ended'
        final=d/'shutdown.wav';run('receiver','record','start',str(final));time.sleep(.2)
        run('receiver','daemon','stop')
        with wave.open(str(final),'rb') as f:assert f.getnframes()>0,'shutdown did not finalize WAV'
        for direction in ['input','output']:
            # A file-only profile intentionally disables input. Test meters in a
            # fresh profile so they select the native null default device.
            result=run('meter','test',direction,'--seconds','1')
            assert 'dBFS' in result.stdout and '[' in result.stdout,'no live meter'
        print('PASS: three-process file transmission, all/selected peer recording, mute independence, pause/resume/EOF, shutdown WAV, device meters')
    finally:
        for node,p in processes:
            if p.poll() is None:
                run(node,'daemon','stop',check=False)
                try:p.wait(timeout=5)
                except subprocess.TimeoutExpired:p.kill();p.wait()
            if p.returncode not in (0,None):print(node,p.stderr.read().decode())
