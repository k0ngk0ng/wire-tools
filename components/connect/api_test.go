package connect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/k0ngk0ng/wire-tools/components/connect/internal/config"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Failure scenarios are listed in docs/INTEGRATION_PLAN.md before implementation:
// fresh state, malformed/oversized requests, unsafe names, missing credentials,
// and enrollment errors must neither start a tunnel nor expose saved secrets.
func TestResourceAPIWithoutSetup(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connect")
	manager := New(context.Background(), dir, "test")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"GET", "/connections", "", 200},
		{"POST", "/connections", `{"server":"https://vpn.example.com"}`, 422},
		{"POST", "/connections", `{"name":"../bad","server":"https://vpn.example.com"}`, 400},
		{"POST", "/connections", `{"server":"https://vpn.example.com","unknown":true}`, 400},
		{"POST", "/connections", `null`, 400},
		{"POST", "/connections", `{`, 400},
		{"POST", "/connections/missing/resume", `{}`, 404},
		{"POST", "/credentials", `{"server":"https://vpn.example.com","token":""}`, 400},
	}
	for _, tc := range cases {
		req, err := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader(tc.body))
		if err != nil {
			t.Fatal(err)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Errorf("%s %s: %d, want %d: %s", tc.method, tc.path, response.StatusCode, tc.status, body)
		}
		if bytes.Contains(body, []byte("daemon start")) {
			t.Fatal("API requested manual daemon startup")
		}
	}
}

func TestEnrollmentAPIKeepsTokenPrivate(t *testing.T) {
	remote := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/login" || r.Method != "POST" {
			t.Error(r.Method, r.URL.Path)
		}
		var input map[string]string
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["token"] != "enrollment-token" {
			t.Error(input)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"token":"private-device-token","device_id":"device-1"}`)
	}))
	defer remote.Close()
	dir := filepath.Join(t.TempDir(), "connect")
	manager := New(context.Background(), dir, "test")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	fingerprint := sha256.Sum256(remote.Certificate().Raw)
	request := `{"server":"` + remote.URL + `","token":"enrollment-token","certificate_sha256":"` + hex.EncodeToString(fingerprint[:]) + `"}`
	response, err := http.Post(server.URL+"/credentials", "application/json", strings.NewReader(request))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatalf("enroll: %d %s", response.StatusCode, body)
	}
	if strings.Contains(string(body), "private-device-token") {
		t.Fatal("credential returned in public response")
	}
	b, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("private-device-token")) {
		t.Fatal("credential was not saved")
	}
	response, err = http.Get(server.URL + "/connections")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	response.Body.Close()
	if strings.Contains(string(body), "private-device-token") {
		t.Fatal("credential leaked in status")
	}
}

// Restart failure scenarios: missing credentials or an unavailable helper must
// retain the desired session and saved pair; explicitly stopping must clear
// only desired activity so another runtime cannot resurrect that connection.
func TestRuntimeRestoresDesiredConnectionsWithoutLosingSavedPairs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "connect")
	store := config.Store{Dir: dir}
	if err := store.Write("profile-office", config.Profile{Server: "https://vpn.example.com", LocalIP: "100.64.0.1", PeerIP: "100.64.0.2"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write("active-connections", []string{"office"}); err != nil {
		t.Fatal(err)
	}
	manager := New(context.Background(), dir, "test")
	defer manager.Close()
	server := httptest.NewServer(manager)
	defer server.Close()
	var connections struct {
		Connections []Connection `json:"connections"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		response, err := http.Get(server.URL + "/connections")
		if err != nil {
			t.Fatal(err)
		}
		err = json.NewDecoder(response.Body).Decode(&connections)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(connections.Connections) == 1 && connections.Connections[0].State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("desired connection not restored: %+v", connections)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(connections.Connections[0].Error, "not authorized") {
		t.Fatal(connections)
	}
	req, _ := http.NewRequest("DELETE", server.URL+"/connections/office", nil)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	var desired []string
	if err := store.Read("active-connections", &desired); err != nil {
		t.Fatal(err)
	}
	if len(desired) != 0 {
		t.Fatal("stopped connection still desired", desired)
	}
	var saved config.Profile
	if err := store.Read("profile-office", &saved); err != nil {
		t.Fatal("saved pair lost", err)
	}
	if saved.Server != "https://vpn.example.com" {
		t.Fatal(saved)
	}
}
