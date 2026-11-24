package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAssistantProxy(t *testing.T) {
	var gotPath, gotCookie, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotCookie = r.URL.Path, r.Header.Get("Cookie")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answer":"ok"}`))
	}))
	defer upstream.Close()

	ts := httptest.NewServer(New(Config{AssistantURL: upstream.URL}, nil).Handler())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/assistant/chat", strings.NewReader(`{"message":"hi"}`))
	req.Header.Set("Cookie", "secret=1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != `{"answer":"ok"}` {
		t.Fatalf("chat: %d %s (upstream path=%q)", resp.StatusCode, body, gotPath)
	}
	if gotPath != "/chat" || gotBody != `{"message":"hi"}` || gotCookie != "" {
		t.Fatalf("upstream saw path=%q body=%q cookie=%q", gotPath, gotBody, gotCookie)
	}

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/assistant/chat"}, {"POST", "/api/assistant/status"}, {"GET", "/api/assistant/health"},
	} {
		req, _ := http.NewRequest(c.method, ts.URL+c.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s %s: got %d, want 404/405", c.method, c.path, resp.StatusCode)
		}
	}
}

func TestAssistantProxyDisabledAndDown(t *testing.T) {
	for name, cfg := range map[string]Config{"unset": {}, "down": {AssistantURL: "http://127.0.0.1:1"}} {
		ts := httptest.NewServer(New(cfg, nil).Handler())
		resp, err := http.Get(ts.URL + "/api/assistant/status")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		ts.Close()
		want := map[string]int{"unset": http.StatusServiceUnavailable, "down": http.StatusBadGateway}[name]
		if resp.StatusCode != want {
			t.Errorf("%s: got %d, want %d", name, resp.StatusCode, want)
		}
	}
}
