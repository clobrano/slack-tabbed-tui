package login

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestRealBrowser runs Login against a real Chromium, headless, on a
// local page standing in for app.slack.com. It checks the DevTools calls
// against the real protocol. Run with STT_TEST_BROWSER=/path/to/chromium.
func TestRealBrowser(t *testing.T) {
	bin := os.Getenv("STT_TEST_BROWSER")
	if bin == "" {
		t.Skip("set STT_TEST_BROWSER to a Chromium binary to run")
	}
	page := `<!doctype html><script>
localStorage.setItem('localConfig_v2', JSON.stringify({teams: {T1: {id: 'T1', name: 'Acme', domain: 'acme', url: 'https://acme.slack.com/', token: 'xoxc-real-test'}}}));
document.cookie = 'd=xoxd-real%2Ftest; domain=.slack.com; path=/';
</script>signed in`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	}))
	defer srv.Close()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())

	dir := t.TempDir()
	wrapper := filepath.Join(dir, "chromium")
	script := fmt.Sprintf("#!/bin/sh\nexec %q --headless=new --no-sandbox --no-proxy-server --host-resolver-rules='MAP *.slack.com 127.0.0.1' \"$@\"\n", bin)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := Login(ctx, "http://app.slack.com:"+port+"/client", Options{Launch: ExecLauncher(wrapper, filepath.Join(dir, "profile")), Poll: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	team, err := s.Pick("acme.slack.com")
	if s.Cookie != "xoxd-real%2Ftest" || err != nil || team.Token != "xoxc-real-test" {
		t.Errorf("session %+v, team %+v, %v", s, team, err)
	}
}
