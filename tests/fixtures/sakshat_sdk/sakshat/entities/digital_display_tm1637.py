import time
from RPi import GPIO


class DigitalDisplayTM1637:
    def __init__(self, pins, real_true):
        assert pins == {'di': 25, 'clk': 5}
        assert real_true == GPIO.HIGH

    def show(self, text):
        assert text == '6702'
        # Model the SDK's blocking bit-banged transaction so button polling
        # must remain responsive while the display changes.
        time.sleep(.25)
        GPIO.trace('display', text=text, visible=True, segments=[0x7d, 0x07, 0x3f, 0x5b])

    def off(self):
        GPIO.trace('display', text='', visible=False)
