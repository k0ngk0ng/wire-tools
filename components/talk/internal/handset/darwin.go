//go:build darwin && cgo && !talk_test_audio

package handset

/*
#cgo LDFLAGS: -framework IOKit -framework CoreFoundation
#include <IOKit/hid/IOHIDManager.h>
#include <stdlib.h>

typedef struct {
    IOHIDManagerRef manager;
    uint32_t page[128], usage[128];
    CFIndex value[128];
    unsigned read, write;
    IOReturn result;
} TalkHandset;

static void talk_input(void *context, IOReturn result, void *sender, IOHIDValueRef value) {
    TalkHandset *h = context;
    if (result != kIOReturnSuccess || !value) return;
    IOHIDElementRef e = IOHIDValueGetElement(value);
    uint32_t page = IOHIDElementGetUsagePage(e), usage = IOHIDElementGetUsage(e);
    if (page != 12 || (usage != 205 && usage != 233 && usage != 234)) return;
    if (h->write - h->read >= 128) return;
    unsigned i = h->write++ % 128;
    h->page[i] = page;
    h->usage[i] = usage;
    h->value[i] = IOHIDValueGetIntegerValue(value);
}

static TalkHandset *talk_open(void) {
    TalkHandset *h = calloc(1, sizeof(TalkHandset));
    if (!h) return NULL;
    h->manager = IOHIDManagerCreate(kCFAllocatorDefault, kIOHIDOptionsTypeNone);
    if (!h->manager) { free(h); return NULL; }
    CFMutableDictionaryRef match = CFDictionaryCreateMutable(NULL, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    int vendor = 0x001f, product = 0x0b23;
    CFNumberRef v = CFNumberCreate(NULL, kCFNumberIntType, &vendor);
    CFNumberRef p = CFNumberCreate(NULL, kCFNumberIntType, &product);
    CFDictionarySetValue(match, CFSTR(kIOHIDVendorIDKey), v);
    CFDictionarySetValue(match, CFSTR(kIOHIDProductIDKey), p);
    IOHIDManagerSetDeviceMatching(h->manager, match);
    CFRelease(match); CFRelease(v); CFRelease(p);
    IOHIDManagerRegisterInputValueCallback(h->manager, talk_input, h);
    IOHIDManagerScheduleWithRunLoop(h->manager, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
    // Own these consumer keys while talk runs, avoiding a second system volume
    // change or launching a music player for the call button.
    h->result = IOHIDManagerOpen(h->manager, kIOHIDOptionsTypeSeizeDevice);
    return h;
}

static int talk_count(TalkHandset *h) {
    CFSetRef devices = IOHIDManagerCopyDevices(h->manager);
    if (!devices) return 0;
    int count = (int)CFSetGetCount(devices);
    CFRelease(devices);
    return count;
}
static void talk_poll(void) { CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.04, false); }
static int talk_next(TalkHandset *h, uint32_t *page, uint32_t *usage, int *value) {
    if (h->read == h->write) return 0;
    unsigned i = h->read++ % 128;
    *page = h->page[i]; *usage = h->usage[i]; *value = (int)h->value[i];
    return 1;
}
static void talk_close(TalkHandset *h) {
    IOHIDManagerUnscheduleFromRunLoop(h->manager, CFRunLoopGetCurrent(), kCFRunLoopDefaultMode);
    IOHIDManagerClose(h->manager, kIOHIDOptionsTypeNone);
    CFRelease(h->manager); free(h);
}
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"time"
)

func (l *Listener) run(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	for ctx.Err() == nil {
		if !l.enabled() {
			l.update(false, "buttons enabled for the current direct connection only")
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		h := C.talk_open()
		if h == nil {
			l.update(false, "cannot create USB HID listener")
		} else if h.result != 0 {
			l.update(false, fmt.Sprintf("USB buttons unavailable (0x%x); check Input Monitoring permission or another app using the device", uint32(h.result)))
			C.talk_close(h)
		} else {
			for ctx.Err() == nil && l.enabled() {
				C.talk_poll()
				online := C.talk_count(h) > 0
				if online {
					l.update(true, "")
				} else {
					l.update(false, "device disconnected; waiting to reconnect")
					clear(l.pressed)
				}
				var page, usage C.uint32_t
				var value C.int
				for C.talk_next(h, &page, &usage, &value) != 0 {
					l.input(uint32(page), uint32(usage), int(value))
				}
			}
			C.talk_close(h)
			clear(l.pressed)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
