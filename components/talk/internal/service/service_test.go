package service

import (
	"encoding/binary"
	"encoding/xml"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNativeServiceDefinitions(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		p, err := Build(goos, "/home/A B", "/config", "1234", "/app/a & b/talk", "/state/a & b")
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Start) == 0 || len(p.Stop) == 0 {
			t.Fatal("missing lifecycle commands")
		}
		if !strings.Contains(p.Content, "__serve") {
			t.Fatal("service does not run audio")
		}
		if goos != "linux" {
			d := xml.NewDecoder(strings.NewReader(p.Content))
			for {
				_, e := d.Token()
				if e != nil {
					if e.Error() != "EOF" {
						t.Fatal(e)
					}
					break
				}
			}
		}
		if goos == "windows" && !strings.Contains(p.Content, "InteractiveToken") {
			t.Fatal("audio cannot run in session 0")
		}
	}
}
func TestServiceRejectsLineInjection(t *testing.T) {
	if _, err := Build("linux", "/home", "/config", "1", "/bin/talk", "/state\nExecStart=bad"); err == nil {
		t.Fatal("accepted injected unit")
	}
}

func TestSchedulerFileUsesUTF16WithBOM(t *testing.T) {
	p, err := Build("windows", "/home", "/config", "S-1-5-21-123", `C:\程序\talk.exe`, `C:\用户\state`)
	if err != nil {
		t.Fatal(err)
	}
	b := encodeDefinition("windows", p.Content)
	if len(b) < 2 || b[0] != 0xff || b[1] != 0xfe {
		t.Fatal("missing UTF-16 BOM")
	}
	units := make([]uint16, (len(b)-2)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(b[2+i*2:])
	}
	decoded := string(utf16.Decode(units))
	want := strings.Replace(p.Content, `encoding="UTF-8"`, `encoding="UTF-16"`, 1)
	if decoded != want {
		t.Fatal("scheduler XML lost Unicode paths")
	}
}
