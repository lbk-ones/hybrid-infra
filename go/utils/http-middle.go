package utils

import (
	"net/http"
	"net/url"
	"strings"
)

// AssetMiddleware permits the configured remote frontends to use the desktop
// asset server. CORS is an origin policy, not authentication or a service sandbox.
func AssetMiddleware(origins []string) func(http.Handler) http.Handler {
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		allowed[origin] = true
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				w.Header().Add("Vary", "Origin")
				u, err := url.Parse(origin)
				sameOrigin := err == nil && u.Host == r.Host && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "wails")
				if !sameOrigin && !allowed[origin] {
					http.Error(w, "frontend origin is not configured", http.StatusForbidden)
					return
				}
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Wails-Client-ID, X-Wails-Window-Name, X-Wails-Chunk-ID, X-Wails-Chunk-Index, X-Wails-Chunk-Total")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions && strings.TrimSpace(origin) != "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
