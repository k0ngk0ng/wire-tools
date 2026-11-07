package media

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

type Command struct {
	Action string `json:"action"`
	File   string `json:"file,omitempty"`
	Peer   string `json:"peer,omitempty"`
	Mode   string `json:"mode,omitempty"`
	Loop   bool   `json:"loop,omitempty"`
}

func (s *Session) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var c Command
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		http.Error(w, "expected one command", 400)
		return
	}
	var err error
	switch c.Action {
	case "record-start":
		if c.File == "" {
			err = fmt.Errorf("recording file is required")
		} else {
			err = s.StartRecording(c.File, c.Peer)
		}
	case "record-stop":
		err = s.StopRecording()
	case "input-start":
		if c.File == "" {
			err = fmt.Errorf("input file is required")
		} else {
			err = s.StartInput(c.File, c.Mode, c.Loop)
		}
	case "input-stop", "input-pause", "input-resume":
		err = s.InputAction(c.Action[len("input-"):])
	default:
		err = fmt.Errorf("unknown media action")
	}
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.Status())
}
