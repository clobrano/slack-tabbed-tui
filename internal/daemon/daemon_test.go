package daemon

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/config"
	"github.com/clobrano/slack-tabbed-tui/internal/creds"
	"github.com/clobrano/slack-tabbed-tui/internal/ipc"
	"github.com/clobrano/slack-tabbed-tui/internal/model"
	"github.com/clobrano/slack-tabbed-tui/internal/notify"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/slack/slacktest"
)

const (
	parentTS = "1700000000.000100"
	link     = "https://acme.slack.com/archives/C0GEN/p1700000000000100"
	threadID = "thread:acme.slack.com/C0GEN/" + parentTS
)

type recorder struct {
	mu    sync.Mutex
	notes []notify.Notification
}

func (r *recorder) Notify(_ context.Context, n notify.Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notes = append(r.notes, n)
	return nil
}

func (r *recorder) all() []notify.Notification {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]notify.Notification(nil), r.notes...)
}

type env struct {
	t     *testing.T
	slack *slacktest.Server
	paths config.Paths
	store creds.Store
	notes *recorder
	c     *ipc.Client
	last  *model.Snapshot
	stop  func()
}

func newEnv(t *testing.T) *env {
	t.Helper()
	s := slacktest.New()
	t.Cleanup(s.Close)
	alice := slack.User{ID: "U1", Name: "alice"}
	alice.Profile.DisplayName = "Alice"
	s.Users = []slack.User{alice, {ID: "U0ME", Name: "me"}}
	s.Convs["C0GEN"] = slack.Conversation{ID: "C0GEN", Name: "general", IsChannel: true, IsMember: true}
	s.AddThread("C0GEN", slack.Message{TS: parentTS, User: "U1", Text: "Deploy is stuck"})

	dir, err := os.MkdirTemp("", "stt") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	paths := config.Paths{ConfigDir: dir + "/c", StateDir: dir + "/s", CacheDir: dir + "/k", RuntimeDir: dir + "/r"}
	store := creds.Store{Path: paths.Credentials(), Getenv: func(string) string { return "" }}
	if err := store.Save(creds.Credential{URL: "https://acme.slack.com/", TeamID: "T0ACME", Team: "Acme", UserID: "U0ME", User: "me",
		Token: slacktest.Token, Cookie: slacktest.Cookie}); err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, slack: s, paths: paths, store: store, notes: &recorder{}}
	e.start()
	t.Cleanup(func() {
		if e.stop != nil {
			e.stop()
		}
	})
	return e
}

func (e *env) start() {
	d := &Daemon{
		Paths: e.paths, Config: config.Config{Interval: time.Hour}, Store: e.store, Notifier: e.notes,
		Log:        log.New(io.Discard, "", 0),
		Client:     func(c creds.Credential) *slack.Client { return slack.New(e.slack.URL, c.Token, c.Cookie) },
		WatchEvery: 20 * time.Millisecond, Debounce: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.Run(ctx) }()
	var c *ipc.Client
	var err error
	for i := 0; i < 200; i++ {
		if c, err = ipc.Dial(e.paths.Socket()); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		e.t.Fatalf("dial: %v", err)
	}
	e.c = c
	e.stop = func() {
		c.Close()
		cancel()
		if err := <-done; err != nil {
			e.t.Errorf("Run: %v", err)
		}
	}
}

func (e *env) restart() {
	e.stop()
	e.stop = nil
	e.start()
}

// wait returns the first snapshot for which ok holds.
func (e *env) wait(what string, ok func(*model.Snapshot) bool) *model.Snapshot {
	e.t.Helper()
	if e.last != nil && ok(e.last) {
		return e.last
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case s, open := <-e.c.Snapshots():
			if !open {
				e.t.Fatalf("waiting for %s: disconnected", what)
			}
			e.last = s
			if ok(s) {
				return s
			}
		case <-timeout:
			e.t.Fatalf("waiting for %s: last snapshot %+v", what, e.last)
		}
	}
}

func (e *env) do(cmd ipc.Command) string {
	e.t.Helper()
	info, err := e.c.Do(context.Background(), cmd)
	if err != nil {
		e.t.Fatalf("%s: %v", cmd.Op, err)
	}
	return info
}

func thread(s *model.Snapshot) *model.Thread { return s.Find(threadID) }

func live(s *model.Snapshot) bool {
	w := s.Workspace("acme.slack.com")
	return w != nil && w.Conn == model.ConnLive
}

func TestDaemon(t *testing.T) {
	e := newEnv(t)
	if info := e.do(ipc.Command{Op: ipc.OpAdd, ID: link}); info != "watching "+threadID {
		t.Fatalf("add: %q", info)
	}
	s := e.wait("thread fetched", func(s *model.Snapshot) bool {
		th := thread(s)
		return th != nil && len(th.Messages) == 1 && live(s)
	})
	th := thread(s)
	if th.ChannelName != "general" || th.Messages[0].Author != "Alice" || th.Messages[0].Text != "Deploy is stuck" || th.ReadTS != parentTS {
		t.Fatalf("thread %+v", th)
	}
	if u, _ := th.Unread(); u != 0 {
		t.Errorf("a new thread starts read, unread %d", u)
	}
	on := true
	e.do(ipc.Command{Op: ipc.OpAlerts, ID: threadID, On: &on})

	// A live reply that mentions me: shown, unread, notified.
	reply := e.slack.Reply("C0GEN", parentTS, "U1", "<@U0ME> can you look?", true)
	s = e.wait("reply", func(s *model.Snapshot) bool { return len(thread(s).Messages) == 2 })
	m := thread(s).Messages[1]
	if m.Text != "@me can you look?" || !m.MentionsMe || m.Mine {
		t.Errorf("reply %+v", m)
	}
	if u, mn := thread(s).Unread(); u != 1 || mn != 1 {
		t.Errorf("unread %d mentions %d", u, mn)
	}
	waitNotes(t, e.notes, 1)
	n := e.notes.all()[0]
	if n.Event.Type != model.EventMention || n.Title != "Alice mentioned you in #general" || !strings.Contains(n.URL, "thread_ts="+parentTS) {
		t.Errorf("notification %+v", n)
	}

	e.do(ipc.Command{Op: ipc.OpRead, ID: threadID, TS: reply.TS})
	e.wait("read", func(s *model.Snapshot) bool { u, _ := thread(s).Unread(); return u == 0 })

	// My reply, then a reaction to it, with reaction alerts on.
	if info := e.do(ipc.Command{Op: ipc.OpReply, ID: threadID, Text: "on it"}); info != "sent" {
		t.Errorf("reply: %q", info)
	}
	s = e.wait("my reply", func(s *model.Snapshot) bool { return len(thread(s).Messages) == 3 })
	mine := thread(s).Messages[2]
	if !mine.Mine || mine.Text != "on it" {
		t.Errorf("my reply %+v", mine)
	}
	if u, _ := thread(s).Unread(); u != 0 {
		t.Errorf("my own reply is unread")
	}
	e.do(ipc.Command{Op: ipc.OpEvents, Events: map[model.EventType]bool{model.EventReaction: true}})
	e.slack.React("C0GEN", parentTS, mine.TS, "U1", "eyes")
	e.wait("reaction", func(s *model.Snapshot) bool {
		r := thread(s).Messages[2].Reactions
		return len(r) == 1 && r[0].Users[0] == "Alice"
	})
	waitNotes(t, e.notes, 2)
	if n := e.notes.all()[1]; n.Event.Type != model.EventReaction || !strings.Contains(n.Title, ":eyes:") {
		t.Errorf("reaction notification %+v", n)
	}

	// An edit of the parent.
	e.slack.Edit("C0GEN", parentTS, parentTS, "Deploy is stuck (fixed)")
	e.wait("edit", func(s *model.Snapshot) bool {
		p := thread(s).Messages[0]
		return p.Edited && p.Text == "Deploy is stuck (fixed)"
	})

	// A reply missed while disconnected is fetched on reconnection.
	e.slack.Reply("C0GEN", parentTS, "U1", "missed", false)
	e.slack.DropSockets()
	e.wait("resync", func(s *model.Snapshot) bool { return len(thread(s).Messages) == 4 })

	// Notifications off: no more notes.
	off := false
	e.do(ipc.Command{Op: ipc.OpAlerts, ID: threadID, On: &off})
	e.slack.Reply("C0GEN", parentTS, "U1", "quiet", true)
	e.wait("quiet reply", func(s *model.Snapshot) bool { return len(thread(s).Messages) == 5 })
	if n := len(e.notes.all()); n != 3 { // mention, reaction, "missed"
		t.Errorf("%d notifications, want 3", n)
	}

	// State survives a restart.
	e.do(ipc.Command{Op: ipc.OpAlerts, ID: threadID, On: &on})
	e.restart()
	e.last = nil
	s = e.wait("restored", func(s *model.Snapshot) bool { th := thread(s); return th != nil && len(th.Messages) == 5 })
	if th := thread(s); !th.Alerts || th.ReadTS != mine.TS {
		t.Errorf("after restart: alerts %v read %s", th.Alerts, th.ReadTS)
	}

	if info := e.do(ipc.Command{Op: ipc.OpUnwatch, ID: link}); info != "unwatched "+threadID {
		t.Errorf("unwatch %q", info)
	}
	e.wait("unwatched", func(s *model.Snapshot) bool { return len(s.Threads) == 0 && len(s.Workspaces) == 0 })
}

func waitNotes(t *testing.T, r *recorder, n int) {
	t.Helper()
	for i := 0; i < 500 && len(r.all()) < n; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(r.all()); got < n {
		t.Fatalf("%d notifications, want %d", got, n)
	}
}

func TestWatchlistFile(t *testing.T) {
	e := newEnv(t)
	// An app.slack.com link names the same thread once the team is known.
	data := "# my threads\nhttps://app.slack.com/client/T0ACME/C0GEN/thread/C0GEN-" + parentTS + "\nbogus\n"
	if err := os.WriteFile(e.paths.Watchlist(), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	s := e.wait("file picked up", func(s *model.Snapshot) bool { th := thread(s); return th != nil && len(th.Messages) == 1 })
	if len(s.Threads) != 1 {
		t.Errorf("threads %+v", s.Threads)
	}
	if err := os.WriteFile(e.paths.Watchlist(), []byte("# none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.wait("file emptied", func(s *model.Snapshot) bool { return len(s.Threads) == 0 })
}

func TestErrors(t *testing.T) {
	e := newEnv(t)
	if _, err := e.c.Do(context.Background(), ipc.Command{Op: ipc.OpAdd, ID: "https://acme.slack.com/archives/C0GEN/p1700000999000100"}); err == nil || !strings.Contains(err.Error(), "thread_not_found") {
		t.Errorf("add missing thread: %v", err)
	}
	if _, err := e.c.Do(context.Background(), ipc.Command{Op: ipc.OpAdd, ID: "https://example.com"}); err == nil {
		t.Error("add non-Slack link")
	}
	if _, err := e.c.Do(context.Background(), ipc.Command{Op: ipc.OpReply, ID: "thread:x.slack.com/C1/1.000001", Text: "x"}); err == nil {
		t.Error("reply to unwatched thread")
	}

	// A thread in a workspace with no session shows why.
	other := "thread:beta.slack.com/C99/1700000000.000100"
	_ = e.store.Save(creds.Credential{URL: "https://gamma.slack.com/", TeamID: "T0G", Token: "xoxc-g", Cookie: "xoxd-g"})
	if err := os.WriteFile(e.paths.Watchlist(), []byte(other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := e.wait("no session", func(s *model.Snapshot) bool {
		th := s.Find(other)
		return th != nil && strings.Contains(th.Error, "not signed in to beta.slack.com")
	})
	if w := s.Workspace("beta.slack.com"); w == nil || w.Conn != model.ConnNoSession {
		t.Errorf("workspace %+v", w)
	}

	// An expired session shows as logged out.
	_ = e.store.Save(creds.Credential{URL: "https://acme.slack.com/", TeamID: "T0ACME", UserID: "U0ME", Token: "xoxc-expired", Cookie: slacktest.Cookie})
	if err := os.WriteFile(e.paths.Watchlist(), []byte(link+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.wait("logged out", func(s *model.Snapshot) bool {
		w := s.Workspace("acme.slack.com")
		return w != nil && w.Conn == model.ConnLoggedOut
	})
	if _, err := os.Stat(filepath.Join(e.paths.StateDir, "snapshot.json")); err != nil {
		t.Errorf("snapshot not written: %v", err)
	}
}

func TestComplete(t *testing.T) {
	e := newEnv(t)
	bot := slack.User{ID: "B1", Name: "deploybot", IsBot: true}
	bot.Profile.DisplayName = "Deploy Bot"
	bob := slack.User{ID: "U2", Name: "bob"}
	e.slack.Users = append(e.slack.Users, bot, bob)
	e.slack.Groups = []slack.UserGroup{{ID: "S1", Handle: "oncall"}}
	e.slack.Members["C0GEN"] = []string{"U2", "B1"}
	e.slack.Emoji["shipit"] = "https://emoji/shipit.png"
	e.slack.Reply("C0GEN", parentTS, "U1", "ship it :tada:", false)
	e.do(ipc.Command{Op: ipc.OpAdd, ID: link})
	s := e.wait("fetched", func(s *model.Snapshot) bool { th := thread(s); return th != nil && len(th.Messages) == 2 })
	if got := thread(s).Messages[1].Text; got != "ship it 🎉" {
		t.Errorf("emoji not rendered: %q", got)
	}

	query := func(kind, text string) []model.Candidate {
		t.Helper()
		m, err := e.c.Query(context.Background(), ipc.Command{Op: ipc.OpComplete, ID: threadID, Kind: kind, Text: text})
		if err != nil {
			t.Fatalf("complete %s%s: %v", kind, text, err)
		}
		return m.Candidates
	}
	// The directory syncs in the background: wait for the bot.
	var got []model.Candidate
	for i := 0; i < 200; i++ {
		if got = query("@", "dep"); len(got) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(got) != 1 || got[0].Token != "<@B1>" || got[0].Kind != "bot" {
		t.Fatalf("@dep = %+v", got)
	}
	// Empty query: the participant (Alice) first, then members, never me.
	all := query("@", "")
	if len(all) < 3 || all[0].Label != "@Alice" || all[1].Label != "@bob" && all[1].Label != "@Deploy Bot" {
		t.Errorf("@ = %+v", all)
	}
	for _, c := range all {
		if c.Token == "<@U0ME>" {
			t.Errorf("proposed myself: %+v", c)
		}
	}
	if got := query("@", "onc"); len(got) != 1 || got[0].Token != "<!subteam^S1>" {
		t.Errorf("@onc = %+v", got)
	}
	if got := query("#", "gen"); len(got) != 1 || got[0].Token != "<#C0GEN>" {
		t.Errorf("#gen = %+v", got)
	}
	if got := query(":", "shipi"); len(got) != 1 || got[0].Label != ":shipit:" {
		t.Errorf(":shipi = %+v", got)
	}
	if _, err := e.c.Query(context.Background(), ipc.Command{Op: ipc.OpComplete, ID: "thread:x.slack.com/C11/1.000001", Kind: "@"}); err == nil {
		t.Error("completion for an unwatched thread")
	}
	if n := e.slack.CallCount("conversations.members"); n != 1 {
		t.Errorf("members fetched %d times, want once (cached)", n)
	}
}
