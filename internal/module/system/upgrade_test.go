package system

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v1.0.0", "0.4.1", true},
		{"v0.4.1", "0.4.1", false},
		{"v0.4.10", "0.4.9", true},
		{"v0.3.9", "0.4.0", false},
		{"1.2.0", "v1.10.0", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestLatestCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"tag_name":"v1.0.0","name":"sshDesktop v1.0.0","html_url":"https://example.com/r","body":"notes"}`))
	}))
	defer srv.Close()
	old, oldPage := ReleaseURL, ReleasePageURL
	ReleaseURL, ReleasePageURL = srv.URL, srv.URL+"/releases/latest"
	defer func() { ReleaseURL, ReleasePageURL = old, oldPage }()

	s := New(Deps{Version: "0.4.1"})
	rel, errMsg, _ := s.latest(context.Background(), false)
	if errMsg != "" || rel == nil || rel.Tag != "v1.0.0" || rel.Notes != "notes" {
		t.Fatalf("rel=%+v err=%s", rel, errMsg)
	}
	s.latest(context.Background(), false)
	if calls != 1 {
		t.Fatalf("应使用缓存，实际请求 %d 次", calls)
	}
	s.latest(context.Background(), true)
	if calls != 2 {
		t.Fatalf("refresh 应重新查询，实际请求 %d 次", calls)
	}
}

func TestLatestRejectsBadTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v1.0.0; rm -rf /"}`))
	}))
	defer srv.Close()
	old, oldPage := ReleaseURL, ReleasePageURL
	ReleaseURL, ReleasePageURL = srv.URL, srv.URL+"/releases/latest"
	defer func() { ReleaseURL, ReleasePageURL = old, oldPage }()
	s := New(Deps{Version: "0.4.1"})
	if rel, errMsg, _ := s.latest(context.Background(), false); rel != nil || errMsg == "" {
		t.Fatalf("应拒绝不合法的版本号：%+v", rel)
	}
}

func TestLatestFallsBackToRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		http.Redirect(w, r, "/apple2333usa/sshDesktop/releases/tag/v1.0.0", http.StatusFound)
	}))
	defer srv.Close()
	oldAPI, oldPage := ReleaseURL, ReleasePageURL
	ReleaseURL, ReleasePageURL = srv.URL+"/api", srv.URL+"/releases/latest"
	defer func() { ReleaseURL, ReleasePageURL = oldAPI, oldPage }()
	tag, err := LatestTag(context.Background())
	if err != nil || tag != "v1.0.0" {
		t.Fatalf("tag=%q err=%v", tag, err)
	}
}
