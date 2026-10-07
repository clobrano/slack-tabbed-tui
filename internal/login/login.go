// Package login signs in to Slack through a real browser: it starts
// Chromium (or Chrome, Brave, Edge) with a dedicated profile and the
// DevTools protocol on, lets the user log in as usual (SSO, 2FA, …),
// then reads the session the Slack web client keeps: the xoxc- token
// from its localStorage and the d cookie.
package login

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/ws"
)

// Team is a workspace the browser is signed in to.
type Team struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Domain       string `json:"domain"`
	URL          string `json:"url"` // "https://acme.slack.com/"
	EnterpriseID string `json:"enterprise_id"`
	Token        string `json:"token"` // xoxc-…
}

// Host is the workspace host, e.g. "acme.slack.com".
func (t Team) Host() string {
	u, err := url.Parse(t.URL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Cookie is a browser cookie of a slack.com domain.
type Cookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
}

// Session is what a successful login yields.
type Session struct {
	Cookie    string   // the d cookie, xoxd-…
	Cookies   []Cookie // every slack.com cookie, d included
	Teams     []Team   // every workspace of the browser session
	UserAgent string   // the browser's, sent with API calls like the web client
}

// CookieHeader is the Cookie header the browser sends to host: every
// cookie whose domain matches it. Enterprise Grid sessions need more
// than d (d-s, at least).
func (s Session) CookieHeader(host string) string {
	var parts []string
	for _, c := range s.Cookies {
		d := strings.ToLower(c.Domain)
		if d == host || strings.HasPrefix(d, ".") && (host == d[1:] || strings.HasSuffix(host, d)) {
			parts = append(parts, c.Name+"="+c.Value)
		}
	}
	if len(parts) == 0 && s.Cookie != "" {
		return "d=" + s.Cookie
	}
	return strings.Join(parts, "; ")
}

// Browser is a started browser with the DevTools protocol on.
type Browser struct {
	// DebuggerURL is the browser-level DevTools WebSocket URL.
	DebuggerURL string
	// Stop ends the browser process, if it is still running.
	Stop func()
}

// Launcher starts a browser on a page.
type Launcher func(ctx context.Context, startURL string) (*Browser, error)

// Options tunes Login.
type Options struct {
	Launch Launcher
	// Poll is how often to look for the session. Default 1s.
	Poll time.Duration
	// Progress, if set, gets short status messages for the user.
	Progress func(string)
}

// Login opens workspaceURL in a browser and waits until the user has
// signed in, or ctx ends. The browser is closed before returning.
func Login(ctx context.Context, workspaceURL string, opt Options) (Session, error) {
	if opt.Poll <= 0 {
		opt.Poll = time.Second
	}
	say := func(s string) {
		if opt.Progress != nil {
			opt.Progress(s)
		}
	}
	b, err := opt.Launch(ctx, workspaceURL)
	if err != nil {
		return Session{}, err
	}
	defer b.Stop()
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := ws.Dial(dctx, b.DebuggerURL, nil)
	cancel()
	if err != nil {
		return Session{}, fmt.Errorf("connecting to the browser: %w", err)
	}
	cdp := &devtools{conn: conn}
	defer conn.Close()
	say("Sign in to Slack in the browser window; it closes by itself when you are done.")

	attached := map[string]string{} // target ID → session ID
	for {
		s, err := cdp.session(ctx, attached)
		if err != nil {
			return Session{}, err
		}
		if s.Cookie != "" && len(s.Teams) > 0 {
			var v struct {
				UserAgent string `json:"userAgent"`
			}
			if cdp.callInto(ctx, "Browser.getVersion", nil, "", &v) == nil {
				s.UserAgent = v.UserAgent
			}
			_, _ = cdp.call(ctx, "Browser.close", nil, "")
			return s, nil
		}
		select {
		case <-ctx.Done():
			return Session{}, ctx.Err()
		case <-time.After(opt.Poll):
		}
	}
}

// session reads the d cookie and the teams from every Slack page.
func (d *devtools) session(ctx context.Context, attached map[string]string) (Session, error) {
	var s Session
	var cookies struct {
		Cookies []Cookie `json:"cookies"`
	}
	if err := d.callInto(ctx, "Storage.getCookies", nil, "", &cookies); err != nil {
		return s, err
	}
	for _, c := range cookies.Cookies {
		d := strings.TrimPrefix(c.Domain, ".")
		if d != "slack.com" && !strings.HasSuffix(d, ".slack.com") {
			continue
		}
		s.Cookies = append(s.Cookies, c)
		if c.Name == "d" && strings.HasPrefix(c.Value, "xoxd-") {
			s.Cookie = c.Value
		}
	}
	var targets struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
			Type     string `json:"type"`
			URL      string `json:"url"`
		} `json:"targetInfos"`
	}
	if err := d.callInto(ctx, "Target.getTargets", nil, "", &targets); err != nil {
		return s, err
	}
	seen := map[string]bool{}
	for _, t := range targets.TargetInfos {
		u, err := url.Parse(t.URL)
		if t.Type != "page" || err != nil || !strings.HasSuffix(u.Hostname(), "slack.com") {
			continue
		}
		sid, ok := attached[t.TargetID]
		if !ok {
			var res struct {
				SessionID string `json:"sessionId"`
			}
			if err := d.callInto(ctx, "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, "", &res); err != nil {
				continue // the page went away
			}
			sid = res.SessionID
			attached[t.TargetID] = sid
		}
		var ev struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		err = d.callInto(ctx, "Runtime.evaluate", map[string]any{
			"expression":    "(function(){try{return localStorage.getItem('localConfig_v2')||''}catch(e){return ''}})()",
			"returnByValue": true,
		}, sid, &ev)
		if err != nil {
			delete(attached, t.TargetID)
			continue
		}
		for _, team := range parseLocalConfig(ev.Result.Value) {
			if !seen[team.ID] {
				seen[team.ID] = true
				s.Teams = append(s.Teams, team)
			}
		}
	}
	return s, nil
}

// parseLocalConfig extracts the signed-in teams from the web client's
// localConfig_v2.
func parseLocalConfig(raw string) []Team {
	var cfg struct {
		Teams map[string]Team `json:"teams"`
	}
	if raw == "" || json.Unmarshal([]byte(raw), &cfg) != nil {
		return nil
	}
	var teams []Team
	for id, t := range cfg.Teams {
		if !strings.HasPrefix(t.Token, "xoxc-") {
			continue
		}
		if t.ID == "" {
			t.ID = id
		}
		teams = append(teams, t)
	}
	return teams
}

// Pick returns the team for a workspace host, or the only team.
func (s Session) Pick(host string) (Team, error) {
	for _, t := range s.Teams {
		if t.Host() == host || t.Domain+".slack.com" == host {
			return t, nil
		}
	}
	if len(s.Teams) == 1 {
		return s.Teams[0], nil
	}
	var hosts []string
	for _, t := range s.Teams {
		hosts = append(hosts, t.Host())
	}
	return Team{}, fmt.Errorf("signed in, but not to %s (signed in to: %s)", host, strings.Join(hosts, ", "))
}

// devtools is a minimal Chrome DevTools protocol client.
type devtools struct {
	conn *ws.Conn
	id   atomic.Int64
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (d *devtools) call(ctx context.Context, method string, params any, sessionID string) (json.RawMessage, error) {
	id := d.id.Add(1)
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	if err := d.conn.WriteJSON(msg); err != nil {
		return nil, err
	}
	for {
		if dl, ok := ctx.Deadline(); ok {
			_ = d.conn.SetReadDeadline(dl)
		} else {
			_ = d.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		}
		_, data, err := d.conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("browser connection: %w", err)
		}
		var res struct {
			ID     int64           `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *cdpError       `json:"error"`
		}
		if json.Unmarshal(data, &res) != nil || res.ID != id {
			continue // an event, or a late answer
		}
		if res.Error != nil {
			return nil, fmt.Errorf("browser: %s: %s", method, res.Error.Message)
		}
		return res.Result, nil
	}
}

func (d *devtools) callInto(ctx context.Context, method string, params any, sessionID string, out any) error {
	raw, err := d.call(ctx, method, params, sessionID)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// Candidates are the browser executables tried, in order.
var Candidates = []string{
	"chromium", "chromium-browser", "google-chrome", "google-chrome-stable",
	"brave-browser", "brave", "microsoft-edge", "microsoft-edge-stable",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
}

// ErrNoBrowser means no Chromium-based browser was found.
var ErrNoBrowser = errors.New("no Chromium-based browser found (tried chromium, google-chrome, brave, edge)")

// FindBrowser returns the first browser of Candidates that exists.
func FindBrowser() (string, error) {
	for _, c := range Candidates {
		if p, err := exec.LookPath(c); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBrowser
}

// stopGrace is how long Stop waits for the browser to exit by itself.
var stopGrace = 5 * time.Second

// ExecLauncher starts the browser at path with its own profile in
// profileDir (kept between logins, so SSO remembers the user), and
// reads the DevTools address from the DevToolsActivePort file the
// browser writes there.
func ExecLauncher(path, profileDir string) Launcher {
	return func(ctx context.Context, startURL string) (*Browser, error) {
		if err := os.MkdirAll(profileDir, 0o700); err != nil {
			return nil, err
		}
		portFile := filepath.Join(profileDir, "DevToolsActivePort")
		_ = os.Remove(portFile)
		cmd := exec.Command(path,
			"--user-data-dir="+profileDir,
			"--remote-debugging-port=0",
			"--remote-allow-origins=*",
			"--no-first-run",
			"--no-default-browser-check",
			"--new-window",
			startURL,
		)
		// Its own process group, so Stop also ends the helper processes.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("starting the browser: %w", err)
		}
		exited := make(chan struct{})
		go func() { _ = cmd.Wait(); close(exited) }()
		// stop gives the browser a few seconds to exit by itself (Login
		// asks it to close), then kills its process group.
		stop := func() {
			select {
			case <-exited:
			case <-time.After(stopGrace):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-exited
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for {
			data, err := os.ReadFile(portFile)
			if err == nil {
				port, path, _ := strings.Cut(strings.TrimSpace(string(data)), "\n")
				if port != "" && path != "" {
					return &Browser{DebuggerURL: "ws://127.0.0.1:" + strings.TrimSpace(port) + strings.TrimSpace(path), Stop: stop}, nil
				}
			}
			select {
			case <-exited:
				return nil, errors.New("the browser exited before it was ready (is another instance using the profile?)")
			case <-ctx.Done():
				stop()
				return nil, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				stop()
				return nil, errors.New("the browser did not open its DevTools port")
			}
		}
	}
}
