package utils

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrontendOriginPolicy(t *testing.T) {
	handler := AssetMiddleware([]string{"https://app.example.com"})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	for _, tc := range []struct {
		origin, method string
		status         int
	}{
		{"https://app.example.com", "POST", 202}, {"https://app.example.com", "OPTIONS", 204},
		{"https://evil.example", "POST", 403}, {"https://evil.example", "OPTIONS", 403},
		{"http://wails.localhost", "POST", 202}, {"", "POST", 202}, {"null", "POST", 403},
	} {
		req := httptest.NewRequest(tc.method, "http://wails.localhost/wails/runtime", nil)
		req.Header.Set("Origin", tc.origin)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Errorf("%q %s: got %d", tc.origin, tc.method, res.Code)
		}
		if tc.origin != "" && tc.status != 403 && res.Header().Get("Access-Control-Allow-Origin") != tc.origin {
			t.Fatal("missing exact CORS origin")
		}
		if tc.status == 403 && res.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("untrusted origin allowed")
		}
	}
}
