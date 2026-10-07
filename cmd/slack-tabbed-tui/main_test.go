package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/clobrano/slack-tabbed-tui/internal/config"
	"github.com/clobrano/slack-tabbed-tui/internal/creds"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/slack/slacktest"
)

const (
	parentLink = "https://acme.slack.com/archives/C0GEN/p1700000000000100"
	replyLink  = "https://acme.slack.com/archives/C0GEN/p1700000100000100?thread_ts=1700000000.000100&cid=C0GEN"
	appLink    = "https://app.slack.com/client/T0ACME/C0GEN/thread/C0GEN-1700000000.000100"
	threadID   = "thread:acme.slack.com/C0GEN/1700000000.000100"
)

// testApp returns an app whose files live in a temp dir and whose API
// calls go to a fake workspace, whatever URL a session names.
func testApp(t *testing.T) (*app, *slacktest.Server, *bytes.Buffer) {
	t.Helper()
	t.Setenv(creds.EnvToken, "")
	t.Setenv(creds.EnvCookie, "")
	s := slacktest.New()
	t.Cleanup(s.Close)
	s.Auth.URL = "https://acme.slack.com/"
	u := slack.User{ID: "U1", Name: "alice"}
	u.Profile.DisplayName = "Alice"
	s.Users = []slack.User{u, {ID: "U0ME", Name: "me"}}
	s.Convs["C0GEN"] = slack.Conversation{ID: "C0GEN", Name: "general", IsChannel: true}
	s.AddThread("C0GEN",
		slack.Message{TS: "1700000000.000100", User: "U1", Text: "Deploy is stuck, <!here> any idea?"},
		slack.Message{TS: "1700000100.000100", BotID: "B1", BotProfile: &slack.BotProfile{Name: "CI"}, Text: "Build <https://ci.example/1|#1> failed",
			Reactions: []slack.Reaction{{Name: "eyes", Count: 2, Users: []string{"U1", "U0ME"}}}},
		slack.Message{TS: "1700000200.000100", User: "U0ME", Text: "Looking, cc <@U1>",
			Files: []slack.File{{Name: "log.txt", Size: 2048}}},
	)
	dir := t.TempDir()
	paths := config.Paths{ConfigDir: dir + "/config", StateDir: dir + "/state", CacheDir: dir + "/cache", RuntimeDir: dir + "/run"}
	out := &bytes.Buffer{}
	a := &app{
		paths:  paths,
		store:  creds.Store{Path: paths.Credentials()},
		in:     strings.NewReader(""),
		out:    out,
		client: func(c creds.Credential) *slack.Client { return slack.New(s.URL, c.Token, c.Cookie) },
	}
	return a, s, out
}

func signIn(t *testing.T, a *app) {
	t.Helper()
	a.in = strings.NewReader(slacktest.Token + "\n" + slacktest.Cookie + "\n")
	if err := a.run(context.Background(), []string{"auth", "acme"}); err != nil {
		t.Fatal(err)
	}
}

func TestAuth(t *testing.T) {
	a, _, out := testApp(t)
	if err := a.run(context.Background(), []string{"auth"}); err != nil || !strings.Contains(out.String(), "Not signed in") {
		t.Fatalf("auth before sign-in: %v %q", err, out)
	}
	signIn(t, a)
	if !strings.Contains(out.String(), "Signed in to Acme (acme.slack.com) as me.") {
		t.Errorf("sign-in output: %q", out)
	}
	c, err := a.store.Lookup("T0ACME")
	if err != nil || c.Workspace != "acme.slack.com" || c.UserID != "U0ME" || c.Token != slacktest.Token {
		t.Fatalf("stored session: %+v %v", c, err)
	}

	out.Reset()
	if err := a.run(context.Background(), []string{"auth"}); err != nil || !strings.Contains(out.String(), "acme.slack.com\tAcme as me\tok") {
		t.Errorf("auth check: %v %q", err, out)
	}

	// A session Slack no longer accepts shows as logged out.
	c.Token = "xoxc-expired"
	if err := a.store.Save(c); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := a.run(context.Background(), []string{"auth"}); err != nil || !strings.Contains(out.String(), "logged out") {
		t.Errorf("auth check, expired: %v %q", err, out)
	}

	if err := a.run(context.Background(), []string{"auth", "-rm", "https://acme.slack.com"}); err != nil {
		t.Fatal(err)
	}
	if all, _ := a.store.All(); len(all) != 0 {
		t.Errorf("after -rm: %+v", all)
	}
}

func TestAuthRejectsBadInput(t *testing.T) {
	a, _, _ := testApp(t)
	for _, in := range []string{"xoxp-app-token\nxoxd-1\n", "xoxc-1\nnot-a-cookie\n", "xoxc-wrong\n" + slacktest.Cookie + "\n", ""} {
		a.in = strings.NewReader(in)
		if err := a.run(context.Background(), []string{"auth", "acme.slack.com"}); err == nil {
			t.Errorf("input %q: no error", in)
		}
	}
	if all, _ := a.store.All(); len(all) != 0 {
		t.Errorf("bad sign-ins stored sessions: %+v", all)
	}
}

func TestWorkspaceHost(t *testing.T) {
	for in, want := range map[string]string{
		"acme":                       "acme.slack.com",
		"tesla":                      "tesla.slack.com",
		"https://Acme.slack.com/":    "acme.slack.com",
		"acme.enterprise.slack.com":  "acme.enterprise.slack.com",
		"T0ACME":                     "T0ACME",
		" https://acme.slack.com/x ": "acme.slack.com",
	} {
		if got := workspaceHost(in); got != want {
			t.Errorf("workspaceHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWatchlist(t *testing.T) {
	a, _, out := testApp(t)
	signIn(t, a)
	ctx := context.Background()
	if err := a.run(ctx, []string{"add", replyLink}); err != nil {
		t.Fatal(err)
	}
	// The parent link and the app.slack.com link name the same thread.
	out.Reset()
	if err := a.run(ctx, []string{"add", parentLink, appLink}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "already watching "+threadID) != 2 {
		t.Errorf("add duplicates: %q", out)
	}
	if err := a.run(ctx, []string{"add", "https://example.com/x"}); err == nil {
		t.Error("add accepted a non-Slack link")
	}
	out.Reset()
	if err := a.run(ctx, []string{"ls"}); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != threadID+"\t"+parentLink+"\n" {
		t.Errorf("ls = %q", got)
	}
	if err := a.run(ctx, []string{"rm", appLink}); err != nil {
		t.Fatal(err)
	}
	if err := a.run(ctx, []string{"rm", appLink}); err == nil {
		t.Error("rm of an unwatched thread succeeded")
	}
}

func TestShow(t *testing.T) {
	a, s, out := testApp(t)
	signIn(t, a)
	out.Reset()
	if err := a.run(context.Background(), []string{"show", replyLink}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"#general · 2 replies · " + parentLink,
		"Alice · ",
		"  Deploy is stuck, @here any idea?",
		"CI [APP] · ",
		"← linked",
		"  Build #1 (https://ci.example/1) failed",
		"  :eyes: 2",
		"  Looking, cc @Alice",
		"  [file] log.txt (2 KB)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("show output lacks %q:\n%s", want, got)
		}
	}
	// Names come from the cache the second time.
	before := s.CallCount("users.info") + s.CallCount("conversations.info")
	if err := a.run(context.Background(), []string{"show", parentLink}); err != nil {
		t.Fatal(err)
	}
	if after := s.CallCount("users.info") + s.CallCount("conversations.info"); after != before {
		t.Errorf("directory cache unused: %d lookups, then %d", before, after)
	}
}

func TestShowNotSignedIn(t *testing.T) {
	a, _, _ := testApp(t)
	err := a.run(context.Background(), []string{"show", parentLink})
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("err = %v", err)
	}
}

func TestReply(t *testing.T) {
	a, s, out := testApp(t)
	signIn(t, a)
	a.in = strings.NewReader("fixed in <PR> & deployed\n")
	if err := a.run(context.Background(), []string{"reply", parentLink, "-"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "posted ") {
		t.Errorf("output %q", out)
	}
	msgs := s.Threads["C0GEN/1700000000.000100"]
	if last := msgs[len(msgs)-1]; last.Text != "fixed in &lt;PR&gt; &amp; deployed" || last.User != "U0ME" {
		t.Errorf("posted %+v", last)
	}
	if err := a.run(context.Background(), []string{"reply", parentLink, "  "}); err == nil {
		t.Error("blank reply posted")
	}
}
