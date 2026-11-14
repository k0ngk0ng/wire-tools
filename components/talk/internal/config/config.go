package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/k0ngk0ng/wire-tools/components/talk/internal/private"
	"os"
	"path/filepath"
)

type Config struct {
	Mode         string             `json:"mode,omitempty"`
	OutputGainDB float64            `json:"output_gain_db,omitempty"`
	PeerGains    map[string]float64 `json:"peer_gains,omitempty"`
	Headphones   bool               `json:"headphones"`
	Key          string             `json:"key"`
	Listen       string             `json:"listen"`
	Peers        []string           `json:"peers"`
	Input        string             `json:"input,omitempty"`
	Output       string             `json:"output,omitempty"`
	RingOutput   string             `json:"ring_output,omitempty"`
	Name         string             `json:"name,omitempty"`
	ListenMuted  bool               `json:"listen_muted,omitempty"`
	ActiveGroup  string             `json:"active_group,omitempty"`
	Groups       []Group            `json:"groups,omitempty"`
}

func DefaultDir() (string, error) {
	if s := os.Getenv("WIRE_TALK_HOME"); s != "" {
		return filepath.Abs(s)
	}
	p, err := os.UserConfigDir()
	return filepath.Join(p, "wirectl", "talk"), err
}
func New() (Config, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	return Config{Key: base64.RawURLEncoding.EncodeToString(b), Listen: "0.0.0.0:51830", Peers: []string{}}, err
}
func (c Config) KeyBytes() ([]byte, error) {
	b, e := base64.RawURLEncoding.DecodeString(c.Key)
	if e != nil || len(b) != 32 {
		return nil, fmt.Errorf("invalid room key (expected 32 bytes, base64url)")
	}
	return b, nil
}
func Load(dir string) (Config, error) {
	var c Config
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return c, fmt.Errorf("read config: %w; run wirectl talk init", err)
	}
	err = json.Unmarshal(b, &c)
	if err != nil {
		return c, err
	}
	err = c.ValidateGroups()
	return c, err
}
func Save(dir string, c Config) error {
	if err := c.ValidateGroups(); err != nil {
		return err
	}
	if err := private.Dir(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	// Configuration creation is exclusive: init never destroys an existing room.
	f, err := os.OpenFile(filepath.Join(dir, "config.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
