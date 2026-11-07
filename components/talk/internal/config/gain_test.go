package config

import "testing"

func TestGainPersistenceAndValidation(t *testing.T) {
	c, _ := New()
	db := 6.0
	next, err := c.Changed(GroupChange{Action: "peer-gain", Peer: "[::ffff:10.0.0.1]:51830", GainDB: &db})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.PeerGains) != 0 || next.PeerGains["10.0.0.1:51830"] != 6 {
		t.Fatal("copy or address normalization failed")
	}
	next, err = next.Changed(GroupChange{Action: "output-gain", GainDB: &db})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = Save(dir, next); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(dir)
	if err != nil || loaded.OutputGainDB != 6 || loaded.PeerGains["10.0.0.1:51830"] != 6 {
		t.Fatal("gain did not persist", err)
	}
	invalid := 25.0
	if _, err = next.Changed(GroupChange{Action: "peer-gain", Peer: "10.0.0.1:51830", GainDB: &invalid}); err == nil {
		t.Fatal("invalid gain accepted")
	}
	if next.PeerGains["10.0.0.1:51830"] != 6 {
		t.Fatal("failed update mutated config")
	}
	zero := 0.0
	reset, err := next.Changed(GroupChange{Action: "peer-gain", Peer: "10.0.0.1:51830", GainDB: &zero})
	if err != nil || len(reset.PeerGains) != 0 {
		t.Fatal("reset failed", err)
	}
}
