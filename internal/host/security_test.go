package host

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIAuthenticationBoundary(t *testing.T) {
	token := strings.Repeat("c", 64)
	h := Authenticate(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, test := range []struct {
		token, origin string
		want          int
	}{
		{"", "", 401}, {"wrong", "", 401}, {token, "https://evil.example", 403}, {token, "null", 403}, {token, "", 204},
	} {
		req := httptest.NewRequest("GET", "http://127.0.0.1:8080/v1/status", nil)
		if test.token != "" {
			req.Header.Set("Authorization", "Bearer "+test.token)
		}
		if test.origin != "" {
			req.Header.Set("Origin", test.origin)
		}
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != test.want {
			t.Fatalf("got %d want %d", rr.Code, test.want)
		}
	}
}

// Body failure matrix: ignored POST JSON with Connection: close; preserving
// JSON for resource decoders; oversized bodies; and unauthenticated input.
func TestAuthenticatedBodyIsConsumedBeforeDispatch(t *testing.T) {
	token := strings.Repeat("d", 64)
	source := strings.NewReader(`{"value":1}`)
	h := Authenticate(token, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if source.Len() != 0 {
			t.Error("wire body remains unread before response")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != `{"value":1}` {
			t.Fatalf("resource body %q %v", body, err)
		}
		w.WriteHeader(204)
	}))
	req := httptest.NewRequest("POST", "http://127.0.0.1/v1/talk/session", source)
	req.Close = true
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != 204 {
		t.Fatal(rr.Code)
	}
}
