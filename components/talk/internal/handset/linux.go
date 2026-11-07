//go:build linux && !talk_test_audio

package handset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func (l *Listener) run(ctx context.Context) {
	for ctx.Err() == nil {
		fd := -1
		message := "supported USB handset disconnected; waiting to reconnect"
		if l.enabled() {
			paths, _ := filepath.Glob("/sys/class/input/event*/device/id/vendor")
			for _, path := range paths {
				read := func(name string) string {
					b, _ := os.ReadFile(filepath.Join(filepath.Dir(path), name))
					return strings.TrimSpace(string(b))
				}
				vendor, product, bus := read("vendor"), read("product"), read("bustype")
				if bus != "0003" || !(vendor == "001f" && product == "0b23" || vendor == "0c76" && product == "1203") {
					continue
				}
				event := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
				var err error
				fd, err = unix.Open(filepath.Join("/dev/input", event), unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
				if err != nil {
					message = fmt.Sprintf("USB buttons: %v; allow read access to the device's /dev/input event node", err)
					continue
				}
				// EVIOCGRAB affects only this matched USB consumer-control device.
				if err = unix.IoctlSetInt(fd, 0x40044590, 1); err != nil {
					unix.Close(fd)
					fd = -1
					message = fmt.Sprintf("cannot claim USB buttons: %v", err)
					continue
				}
				if vendor == "0c76" {
					l.device = "USB PnP Audio Device"
				} else {
					l.device = "AB17X USB Audio"
				}
				break
			}
		} else {
			message = "buttons enabled for the current direct connection only"
		}
		if fd >= 0 {
			l.update(true, "")
			eventSize := int(unsafe.Sizeof(unix.Timeval{})) + 8
			buffer := make([]byte, eventSize*64)
			for ctx.Err() == nil && l.enabled() {
				n, err := unix.Read(fd, buffer)
				if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
					break
				}
				if err == nil && (n == 0 || n%eventSize != 0) {
					break
				}
				for offset := 0; offset+eventSize <= n; offset += eventSize {
					if page, usage, value, ok := decodeLinuxEvent(buffer[offset : offset+eventSize]); ok {
						l.input(page, usage, value)
					}
				}
				select {
				case <-ctx.Done():
				case <-time.After(20 * time.Millisecond):
				}
			}
			unix.IoctlSetInt(fd, 0x40044590, 0)
			unix.Close(fd)
			clear(l.pressed)
		}
		l.update(false, message)
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
