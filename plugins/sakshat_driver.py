"""SAKS SDK process boundary. Only the TM1637 and two button pins are owned."""
import contextlib
import json
import os
import queue
import signal
import sys
import threading
import time

stop = threading.Event()
output_lock = threading.Lock()
commands = queue.Queue(maxsize=1)
protocol_output = sys.stdout


def emit(**message):
    with output_lock:
        print(json.dumps(message), file=protocol_output, flush=True)


def enqueue(message):
    try:
        commands.put_nowait(message)
    except queue.Full:
        try:
            commands.get_nowait()
        except queue.Empty:
            pass
        commands.put_nowait(message)


def read_commands():
    try:
        for line in sys.stdin:
            enqueue(json.loads(line))
    except Exception as error:
        emit(kind='error', error=str(error))
        print(str(error), file=sys.stderr, flush=True)
    finally:
        stop.set()


def main():
    gpio = display = button_thread = lock_file = None
    owned = []
    signal.signal(signal.SIGTERM, lambda *_: stop.set())
    signal.signal(signal.SIGINT, lambda *_: stop.set())
    try:
        with contextlib.redirect_stdout(sys.stderr):
            import sakshat
            import RPi.GPIO as GPIO
            from sakshat.sakspins import SAKSPins as PINS
            from sakshat.entities.digital_display_tm1637 import DigitalDisplayTM1637
            gpio = GPIO
            if '--test-sdk' not in sys.argv:
                import fcntl
                lock_file = open('/dev/gpiomem', 'r+b', buffering=0)
                fcntl.flock(lock_file.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
            # Do not instantiate SAKSHAT: its initializer resets all GPIO and
            # takes ownership of the buzzer, LEDs and unrelated board pins.
            owned = [PINS.IC_TM1637_DI, PINS.IC_TM1637_CLK,
                     PINS.TACT_LEFT, PINS.TACT_RIGHT]
            GPIO.setwarnings(False)
            GPIO.setmode(GPIO.BCM)
            for pin in owned[:2]:
                GPIO.setup(pin, GPIO.OUT, initial=GPIO.HIGH)
            for pin in owned[2:]:
                GPIO.setup(pin, GPIO.IN, pull_up_down=GPIO.PUD_UP)
            display = DigitalDisplayTM1637(
                {'di': PINS.IC_TM1637_DI, 'clk': PINS.IC_TM1637_CLK}, GPIO.HIGH)
            display.off()
            pins = {'s1': PINS.TACT_LEFT, 's2': PINS.TACT_RIGHT}
            initial = {key: GPIO.input(pin) == GPIO.LOW for key, pin in pins.items()}

        def poll_buttons():
            stable = dict(initial)
            candidate = dict(initial)
            since = {key: time.monotonic() for key in pins}
            try:
                while not stop.wait(.02):
                    changed = False
                    for key, pin in pins.items():
                        value = GPIO.input(pin) == GPIO.LOW
                        now = time.monotonic()
                        if value != candidate[key]:
                            candidate[key], since[key] = value, now
                        elif value != stable[key] and now - since[key] >= .06:
                            stable[key] = value
                            changed = True
                    if changed:
                        emit(kind='buttons', buttons=dict(stable))
            except Exception as error:
                emit(kind='error', error=str(error))
                stop.set()

        button_thread = threading.Thread(target=poll_buttons, daemon=True)
        button_thread.start()
        threading.Thread(target=read_commands, daemon=True).start()
        emit(kind='ready', device='SAKS HAT / TM1637', buttons=initial,
             sdk_version=getattr(sakshat, '__version__', 'test SDK'))
        while not stop.is_set():
            try:
                command = commands.get(timeout=.05)
            except queue.Empty:
                continue
            if command.get('kind') == 'stop':
                break
            if command.get('kind') != 'display' or command.get('text') != '6702':
                raise ValueError('invalid display command')
            visible = command.get('visible')
            if not isinstance(visible, bool):
                raise ValueError('visible must be boolean')
            with contextlib.redirect_stdout(sys.stderr):
                if visible:
                    display.show('6702')
                else:
                    display.off()
            emit(kind='display', seq=command['seq'], visible=visible)
    except Exception as error:
        emit(kind='error', error=str(error))
        print(str(error), file=sys.stderr, flush=True)
    finally:
        stop.set()
        if button_thread:
            button_thread.join(timeout=1)
        with contextlib.redirect_stdout(sys.stderr):
            if display:
                try:
                    display.off()
                except Exception:
                    pass
            if gpio and owned:
                gpio.cleanup(owned)
        if lock_file:
            lock_file.close()


main()
