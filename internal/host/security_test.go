package host

import (
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
