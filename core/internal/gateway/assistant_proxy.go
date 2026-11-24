package gateway

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// assistantProxy forwards /api/assistant/{status,chat} to the City
// Intelligence service, so the browser talks to one origin. The assistant is
// optional: without ASSISTANT_URL these routes answer 503.
func (s *Server) assistantProxy() http.Handler {
	if s.cfg.AssistantURL == "" {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "assistant not configured (set ASSISTANT_URL)"})
		})
	}
	target, err := url.Parse(s.cfg.AssistantURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		slog.Error("invalid ASSISTANT_URL; assistant disabled", "url", s.cfg.AssistantURL)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "assistant misconfigured"})
		})
	}
	// An answer can include a what-if scenario run, which takes up to ~90 s,
	// plus a model fallback; allow generously before giving up.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 180 * time.Second
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = strings.TrimSuffix(target.Path, "/") + "/" + pr.In.PathValue("op")
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Del("Cookie")
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.Warn("assistant unreachable", "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "assistant unreachable"})
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		proxy.ServeHTTP(w, r)
	})
}
