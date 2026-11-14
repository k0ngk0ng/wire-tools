import json
import os
import time

BCM, IN, OUT, PUD_UP, HIGH, LOW = 11, 1, 0, 22, 1, 0


def trace(operation, **data):
    with open(os.environ['WIRECTL_TEST_SAKSHAT_TRACE'], 'a') as f:
        f.write(json.dumps({'operation': operation, 'time': time.time(), **data}) + '\n')


def setwarnings(value):
    pass


def setmode(mode):
    assert mode == BCM


def setup(pin, mode, **kwargs):
    assert pin in (5, 25, 16, 20), 'unrelated board pin was claimed'
    trace('setup', pin=pin, mode=mode)


def output(pin, value):
    assert pin in (5, 25)


def input(pin):
    assert pin in (16, 20)
    with open(os.environ['WIRECTL_TEST_SAKSHAT_BUTTONS']) as f:
        state = json.load(f)
    if state.get('fail'):
        raise OSError('test GPIO disconnected')
    return state.get(str(pin), 1)


def cleanup(pins=None):
    assert pins is not None, 'global GPIO cleanup is forbidden'
    assert set(pins) == {5, 25, 16, 20}
    trace('cleanup', pins=list(pins))
