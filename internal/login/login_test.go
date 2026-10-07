package login

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/ws"
)

const localConfig = `{"teams":{
  "T1":{"id":"T1","name":"Acme","domain":"acme","url":"https://acme.slack.com/","token":"xoxc-acme"},
  "T2":{"name":"Beta","domain":"beta","url":"https://beta.slack.com/","token":"xoxc-beta"},
  "T3":{"name":"Broken","url":"https://x.slack.com/","token":""}}}`

// fakeBrowser answers the DevTools calls Login makes. The user "signs
// in" after loginAfter polls.
type fakeBrowser struct {
	loginAfter int

	mu      sync.Mutex
	polls   int
	closed  bool
	methods []string
}

func (f *fakeBrowser) serve(w http.ResponseWriter, r *http.Request) {
	c, err := ws.Accept(w, r)
	if err != nil {
		return
	}
	defer c.Close()
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var req struct {
			ID        int64          `json:"id"`
			Method    string         `json:"method"`
			Params    map[string]any `json:"params"`
			SessionID string         `json:"sessionId"`
		}
		_ = json.Unmarshal(data, &req)
		f.mu.Lock()
		f.methods = append(f.methods, req.Method)
		signedIn := f.polls >= f.loginAfter
		var result any = map[string]any{}
		switch req.Method {
		case "Storage.getCookies":
			f.polls++
			cookies := []map[string]string{{"name": "b", "value": "x", "domain": ".slack.com"}}
			if signedIn {
				cookies = append(cookies, map[string]string{"name": "d", "value": "xoxd-abc%2F", "domain": ".slack.com"})
			}
			result = map[string]any{"cookies": cookies}
		case "Target.getTargets":
			url := "https://acme.slack.com/sign_in_with_password"
			if signedIn {
				url = "https://app.slack.com/client/T1/C1"
			}
			result = map[string]any{"targetInfos": []map[string]string{
				{"targetId": "p1", "type": "page", "url": url},
				{"targetId": "p2", "type": "page", "url": "https://example.com/"},
				{"targetId": "s1", "type": "service_worker", "url": "https://app.slack.com/sw.js"},
			}}
		case "Target.attachToTarget":
			result = map[string]any{"sessionId": "S-" + req.Params["targetId"].(string)}
		case "Runtime.evaluate":
			value := ""
			if signedIn && req.SessionID == "S-p1" {
				value = localConfig
			}
			result = map[string]any{"result": map[string]any{"type": "string", "value": value}}
		case "Browser.close":
			f.closed = true
		}
		f.mu.Unlock()
		// An unrelated event first: Login must skip it.
		_ = c.WriteJSON(map[string]any{"method": "Target.targetInfoChanged", "params": map[string]any{}})
		_ = c.WriteJSON(map[string]any{"id": req.ID, "result": result})
	}
}

func TestLogin(t *testing.T) {
	fb := &fakeBrowser{loginAfter: 3}
	srv := httptest.NewServer(http.HandlerFunc(fb.serve))
	defer srv.Close()
	stopped := false
	launch := func(_ context.Context, start string) (*Browser, error) {
		if start != "https://acme.slack.com/" {
			t.Errorf("start URL %q", start)
		}
		return &Browser{DebuggerURL: "ws" + strings.TrimPrefix(srv.URL, "http") + "/devtools/browser/x", Stop: func() { stopped = true }}, nil
	}
	var said []string
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, err := Login(ctx, "https://acme.slack.com/", Options{Launch: launch, Poll: time.Millisecond, Progress: func(m string) { said = append(said, m) }})
	if err != nil {
		t.Fatal(err)
	}
	if s.Cookie != "xoxd-abc%2F" || len(s.Teams) != 2 {
		t.Fatalf("session %+v", s)
	}
	team, err := s.Pick("acme.slack.com")
	if err != nil || team.Token != "xoxc-acme" || team.ID != "T1" {
		t.Errorf("Pick(acme) = %+v %v", team, err)
	}
	if b, err := s.Pick("beta.slack.com"); err != nil || b.ID != "T2" {
		t.Errorf("Pick(beta) = %+v %v (team ID from the map key)", b, err)
	}
	if _, err := s.Pick("gamma.slack.com"); err == nil || !strings.Contains(err.Error(), "acme.slack.com") {
		t.Errorf("Pick(gamma): %v", err)
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()
	if !fb.closed || !stopped {
		t.Errorf("browser closed=%v stopped=%v", fb.closed, stopped)
	}
	if fb.polls != 4 {
		t.Errorf("polls = %d, want 4", fb.polls)
	}
	attaches := strings.Count(strings.Join(fb.methods, ","), "Target.attachToTarget")
	if attaches != 1 {
		t.Errorf("attached %d times, want once (to the Slack page only)", attaches)
	}
	if len(said) == 0 {
		t.Error("no progress message")
	}
}

func TestLoginCancelled(t *testing.T) {
	fb := &fakeBrowser{loginAfter: 1 << 30}
	srv := httptest.NewServer(http.HandlerFunc(fb.serve))
	defer srv.Close()
	launch := func(context.Context, string) (*Browser, error) {
		return &Browser{DebuggerURL: "ws" + strings.TrimPrefix(srv.URL, "http"), Stop: func() {}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, err := Login(ctx, "https://acme.slack.com/", Options{Launch: launch, Poll: 10 * time.Millisecond}); err == nil {
		t.Fatal("no error")
	}
}

func TestSinglePick(t *testing.T) {
	s := Session{Teams: []Team{{ID: "E1", URL: "https://acme.enterprise.slack.com/"}}}
	if team, err := s.Pick("acme-eng.slack.com"); err != nil || team.ID != "E1" {
		t.Errorf("single team: %+v %v", team, err)
	}
}

func TestExecLauncher(t *testing.T) {
	old := stopGrace
	stopGrace = 100 * time.Millisecond
	defer func() { stopGrace = old }()
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-chromium")
	// Writes DevToolsActivePort into --user-data-dir, records its args,
	// and waits to be killed.
	body := `#!/bin/sh
for a in "$@"; do case "$a" in --user-data-dir=*) d="${a#--user-data-dir=}";; esac; done
echo "$@" > "$d/args"
printf '9222\n/devtools/browser/abc\n' > "$d/DevToolsActivePort"
exec sleep 30
`
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(dir, "profile")
	b, err := ExecLauncher(script, profile)(context.Background(), "https://acme.slack.com/")
	if err != nil {
		t.Fatal(err)
	}
	if b.DebuggerURL != "ws://127.0.0.1:9222/devtools/browser/abc" {
		t.Errorf("DebuggerURL %q", b.DebuggerURL)
	}
	start := time.Now()
	b.Stop()
	if time.Since(start) > 10*time.Second {
		t.Error("Stop did not kill the browser")
	}
	args, _ := os.ReadFile(filepath.Join(profile, "args"))
	for _, want := range []string{"--remote-debugging-port=0", "--user-data-dir=" + profile, "https://acme.slack.com/"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}

	exits := filepath.Join(dir, "exits")
	_ = os.WriteFile(exits, []byte("#!/bin/sh\nexit 1\n"), 0o755)
	if _, err := ExecLauncher(exits, profile)(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "exited") {
		t.Errorf("exiting browser: %v", err)
	}
}
