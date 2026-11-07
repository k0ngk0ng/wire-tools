package config

import (
	"testing"
)

func TestGroupMigrationAndPersistence(t *testing.T) {
	dir := t.TempDir()
	c, _ := New()
	c.Input = "usb"
	c.Output = "speaker"
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	primary := loaded.RoomConfigs()[0]
	id := primary.ID()
	if len(id) != 16 || loaded.CurrentID() != id {
		t.Fatal("legacy room has no stable ID")
	}
	same := primary
	same.Name = "other label"
	same.Listen = "127.0.0.1:1"
	if same.ID() != id {
		t.Fatal("room identity depends on local metadata")
	}
	fresh, _ := New()
	extra := Group{Name: "work", Key: fresh.Key, Listen: "127.0.0.1:0"}
	next, err := loaded.Changed(GroupChange{Action: "add", Group: &extra})
	if err != nil {
		t.Fatal(err)
	}
	if next.CurrentID() != id || len(loaded.Groups) != 0 {
		t.Fatal("adding room changed microphone routing or original config")
	}
	for _, cmd := range []GroupChange{{Action: "use", Room: "work"}, {Action: "mute", Room: id}, {Action: "rename", Room: id, Name: "home"}} {
		next, err = next.Changed(cmd)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = Rewrite(dir, next); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CurrentID() != extra.ID() || reloaded.Input != "usb" || reloaded.Output != "speaker" || !reloaded.ListenMuted || reloaded.Key != c.Key {
		t.Fatal("migration lost devices, key or group settings")
	}
	next, err = reloaded.Changed(GroupChange{Action: "leave", Room: id})
	if err != nil {
		t.Fatal(err)
	}
	if next.Key != extra.Key || next.CurrentID() != extra.ID() || next.Input != "usb" {
		t.Fatal("removing legacy room lost current routing")
	}
	if _, err = next.Changed(GroupChange{Action: "leave", Room: extra.ID()}); err == nil {
		t.Fatal("removed current room")
	}
}

func TestGroupValidation(t *testing.T) {
	c, _ := New()
	g := c.RoomConfigs()[0]
	g.Name = "duplicate-key"
	if _, err := c.Changed(GroupChange{Action: "add", Group: &g}); err == nil {
		t.Fatal("duplicate room accepted")
	}
	fresh, _ := New()
	g.Key = fresh.Key
	g.Name = "default"
	if _, err := c.Changed(GroupChange{Action: "add", Group: &g}); err == nil {
		t.Fatal("duplicate name accepted")
	}
	g.Name = "bad\nname"
	if _, err := c.Changed(GroupChange{Action: "add", Group: &g}); err == nil {
		t.Fatal("unsafe name accepted")
	}
	if _, err := c.Changed(GroupChange{Action: "use", Room: "missing"}); err == nil {
		t.Fatal("unknown room accepted")
	}
}
