package tui

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/ipc"
	"github.com/clobrano/slack-tabbed-tui/internal/model"
)

type fakeBackend struct {
	sent   []ipc.Command
	opened []string
	copied []string
}

func (f *fakeBackend) Send(c ipc.Command)  { f.sent = append(f.sent, c) }
func (f *fakeBackend) Open(u string) error { f.opened = append(f.opened, u); return nil }
func (f *fakeBackend) Copy(s string) error { f.copied = append(f.copied, s); return nil }

var now = time.Date(2023, 11, 15, 12, 0, 0, 0, time.UTC)

func ts(minutesAgo int) string {
	return fmt.Sprintf("%d.000100", now.Add(-time.Duration(minutesAgo)*time.Minute).Unix())
}

func snapshot() *model.Snapshot {
	t1 := model.Thread{
		ID: "thread:acme.slack.com/C1/" + ts(60), Workspace: "acme.slack.com", Channel: "C1", ThreadTS: ts(60),
		ChannelName: "general", Permalink: "https://acme.slack.com/archives/C1/p" + strings.Replace(ts(60), ".", "", 1),
		Alerts: true,
		Messages: []model.Message{
			{TS: ts(60), Author: "Alice", Text: "Deploy is stuck in staging, any idea?"},
			{TS: ts(50), Author: "me", Mine: true, Text: "Looking"},
			{TS: ts(40), Author: "CI", Bot: true, Text: "Build #1 failed", Attachments: []string{"details at https://ci.example/1"},
				Reactions: []model.Reaction{{Name: "eyes", Count: 2, Mine: true}}},
			{TS: ts(10), Author: "Bob", Text: "@me the lease expired", MentionsMe: true, Files: []model.File{{Name: "log.txt", Size: 2048, URL: "https://files/log"}}},
		},
		ReadTS: ts(40),
	}
	t2 := model.Thread{
		ID: "thread:acme.slack.com/D1/" + ts(30), Workspace: "acme.slack.com", Channel: "D1", ThreadTS: ts(30),
		ChannelName: "Carol", IsDM: true, Permalink: "https://acme.slack.com/archives/D1/p1",
		Messages: []model.Message{{TS: ts(30), Author: "Carol", Text: "Quick question"}},
		ReadTS:   ts(30),
	}
	t3 := model.Thread{
		ID: "thread:beta.slack.com/C2/" + ts(5), Workspace: "beta.slack.com", Channel: "C2", ThreadTS: ts(5),
		Error: "not signed in to beta.slack.com",
	}
	return &model.Snapshot{
		Schema: 1, Threads: []model.Thread{t1, t2, t3}, Settings: model.DefaultSettings(),
		Workspaces: []model.Workspace{{Host: "acme.slack.com", Conn: model.ConnLive}, {Host: "beta.slack.com", Conn: model.ConnNoSession}},
	}
}

func newModel() (*Model, *fakeBackend) {
	be := &fakeBackend{}
	m := NewModel(be)
	m.now = func() time.Time { return now }
	m.Color = false
	m.SetConnected(true)
	m.SetSnapshot(snapshot())
	return m, be
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		m.Key(k)
	}
}

func screen(m *Model, w, h int) string { return strings.Join(m.View(w, h), "\n") }

func TestView(t *testing.T) {
	m, _ := newModel()
	s := screen(m, 100, 40)
	for _, want := range []string{
		"SLACK-TABBED-TUI",
		"3 threads ●1 @1",
		"acme live · beta not signed in",
		"[1:@1 #general deploy-is " + bellIcon + "]",
		"2: @Carol quick-question",
		"3:! beta C2",
		" #general  acme",
		"3 replies · 4 people",
		"Deploy is stuck in staging, any idea?",
		"1 unread (1 mentioning you)",
		"── 3 replies",
		"Alice",
		"me (you)",
		"CI APP",
		"┃ details at https://ci.example/1",
		":eyes: 2",
		"─── new ───",
		"› Bob",
		"[file] log.txt  2 KB",
		"i reply",
		"connected",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("screen lacks %q:\n%s", want, s)
		}
	}
	// The divider is right above the first unread message.
	if strings.Index(s, "─── new") > strings.Index(s, "Bob") || strings.Index(s, "─── new") < strings.Index(s, "Build #1") {
		t.Errorf("divider misplaced:\n%s", s)
	}
}

func TestNavigation(t *testing.T) {
	m, be := newModel()
	// First visit selects the first unread message.
	if _, msg := m.selected(); msg.Author != "Bob" {
		t.Fatalf("selected %q", msg.Author)
	}
	keys(m, "k", "k")
	if _, msg := m.selected(); msg.Author != "me" {
		t.Errorf("after k k: %q", msg.Author)
	}
	keys(m, "g", "g")
	if _, msg := m.selected(); msg.Author != "Alice" {
		t.Errorf("gg: %q", msg.Author)
	}
	keys(m, "G", kEnter)
	if len(be.opened) != 1 || !strings.Contains(be.opened[0], "thread_ts=") {
		t.Errorf("enter opened %v", be.opened)
	}
	keys(m, "O", "Y", "y", "o")
	if be.opened[1] != "https://files/log" || len(be.copied) != 2 || !strings.Contains(be.copied[0], "thread_ts=") || be.opened[2] != m.current().Permalink {
		t.Errorf("O/Y/y/o: opened %v copied %v", be.opened, be.copied)
	}
	keys(m, "l")
	if m.current().ChannelName != "Carol" {
		t.Errorf("l: %q", m.current().ChannelName)
	}
	keys(m, "3", "g", "t")
	if m.activeIdx != 0 {
		t.Errorf("gt wraps to the first tab: %d", m.activeIdx)
	}
	keys(m, "g", "T")
	if m.activeIdx != 2 {
		t.Errorf("gT wraps to the last tab: %d", m.activeIdx)
	}
	s := screen(m, 100, 30)
	if !strings.Contains(s, "! not signed in to beta.slack.com") {
		t.Errorf("error tab:\n%s", s)
	}
	// The selection per thread is kept.
	keys(m, "1")
	if _, msg := m.selected(); msg.Author != "Bob" {
		t.Errorf("selection lost: %q", msg.Author)
	}
}

func TestScrollKeepsSelectionVisible(t *testing.T) {
	m, _ := newModel()
	th := &m.snap.Threads[0]
	for i := 0; i < 40; i++ {
		th.Messages = append(th.Messages, model.Message{TS: fmt.Sprintf("%d.%06d", now.Unix(), i), Author: fmt.Sprintf("user%d", i), Text: "line one\nline two"})
	}
	keys(m, "G")
	if s := screen(m, 80, 24); !strings.Contains(s, "› user39") {
		t.Errorf("last message not visible:\n%s", s)
	}
	keys(m, "g", "g")
	if s := screen(m, 80, 24); !strings.Contains(s, "› Alice") || !strings.Contains(s, "Deploy is stuck") {
		t.Errorf("first message not visible:\n%s", s)
	}
	keys(m, kCtrlD)
	_, msg := m.selected()
	if msg.Author == "Alice" {
		t.Error("ctrl+d did not move")
	}
	if s := screen(m, 80, 24); !strings.Contains(s, "› "+msg.Author) {
		t.Errorf("ctrl+d selection %q not visible:\n%s", msg.Author, s)
	}
	keys(m, kCtrlU, kCtrlU, kCtrlU)
	if _, msg := m.selected(); msg.Author != "Alice" {
		t.Errorf("ctrl+u back to the top: %q", msg.Author)
	}
}

func TestMarkRead(t *testing.T) {
	m, be := newModel()
	m.Tick()
	if len(be.sent) != 0 {
		t.Fatalf("marked read at once: %v", be.sent)
	}
	m.now = func() time.Time { return now.Add(2 * time.Second) }
	m.Tick()
	m.Tick()
	want := []ipc.Command{{Op: ipc.OpRead, ID: m.current().ID, TS: ts(10)}}
	if !reflect.DeepEqual(be.sent, want) {
		t.Errorf("sent %+v", be.sent)
	}
	// u marks read at once, even when just switched.
	keys(m, "l", "u")
	if last := be.sent[len(be.sent)-1]; last.Op != ipc.OpRead || last.ID != m.current().ID {
		t.Errorf("u sent %+v", last)
	}
	// Not while disconnected.
	m2, be2 := newModel()
	m2.SetConnected(false)
	m2.now = func() time.Time { return now.Add(time.Minute) }
	m2.Tick()
	if len(be2.sent) != 0 {
		t.Errorf("sent while disconnected: %v", be2.sent)
	}
}

func TestCompose(t *testing.T) {
	m, be := newModel()
	keys(m, "i", "h", "i", " ", "<", "@", ">", kAltEnter, "x", kCtrlJ, "y")
	s := screen(m, 80, 30)
	if !strings.Contains(s, "Reply in #general deploy-is") || !strings.Contains(s, "hi <@>") || !strings.Contains(s, "enter send") {
		t.Errorf("composer:\n%s", s)
	}
	// esc keeps the draft for this thread only.
	keys(m, kEsc, "l", "i")
	if len(m.input) != 0 {
		t.Errorf("draft leaked to another thread: %q", string(m.input))
	}
	keys(m, kEsc, "h", "i")
	if string(m.input) != "hi <@>\nx\ny" {
		t.Errorf("draft %q", string(m.input))
	}
	keys(m, kCtrlB, kBackspace, kEnter)
	if len(be.sent) != 1 {
		t.Fatalf("sent %+v", be.sent)
	}
	c := be.sent[0]
	if c.Op != ipc.OpReply || c.Text != "hi &lt;@&gt;\nx" || c.On == nil || !*c.On || c.ID != m.current().ID {
		t.Errorf("reply %+v", c)
	}
	if m.mode != modeNormal || m.drafts[m.current().ID] != nil {
		t.Error("composer not closed after sending")
	}
	// ctrl+c discards; an empty reply is not sent.
	keys(m, "i", "z", kCtrlC, "i", " ", kEnter)
	if len(be.sent) != 1 || m.mode != modeCompose {
		t.Errorf("empty reply: sent %d, mode %v", len(be.sent), m.mode)
	}
}

func TestCommands(t *testing.T) {
	m, be := newModel()
	keys(m, "n", "r", "d", "y")
	if len(be.sent) != 3 || be.sent[0].Op != ipc.OpAlerts || *be.sent[0].On || be.sent[1].Op != ipc.OpSync || be.sent[2].Op != ipc.OpUnwatch {
		t.Errorf("sent %+v", be.sent)
	}
	keys(m, "a", "x", kBackspace)
	for _, r := range "https://acme.slack.com/archives/C1/p1700000000000100" {
		m.Key(string(r))
	}
	keys(m, kEnter)
	if last := be.sent[len(be.sent)-1]; last.Op != ipc.OpAdd || last.ID != "https://acme.slack.com/archives/C1/p1700000000000100" {
		t.Errorf("add %+v", last)
	}
	keys(m, "N", "j", "j", " ", "j", " ", kEsc)
	n := len(be.sent)
	if be.sent[n-2].Events[model.EventReaction] != true || be.sent[n-1].Op != ipc.OpMute {
		t.Errorf("events panel sent %+v", be.sent[n-2:])
	}
	m.SetConnected(false)
	keys(m, "r")
	if len(be.sent) != n || !strings.Contains(screen(m, 100, 30), "disconnected from the daemon") {
		t.Error("command sent while disconnected")
	}
	m.Result("", errors.New("boom"))
	if !strings.Contains(screen(m, 100, 30), "× boom") {
		t.Error("error not shown")
	}
}

func TestFind(t *testing.T) {
	m, _ := newModel()
	keys(m, "/", "i", "n", "g")
	s := screen(m, 100, 30)
	if !strings.Contains(s, "2 of 4 messages match") || !strings.Contains(s, "›me ") || !strings.Contains(s, "Alice") || strings.Contains(s, "11:1…") {
		t.Errorf("find:\n%s", s)
	}
	keys(m, kDown, kEnter)
	if _, msg := m.selected(); msg.Author != "Alice" {
		t.Errorf("find selected %q", msg.Author)
	}
}

func TestHelpAndSmallScreens(t *testing.T) {
	m, _ := newModel()
	keys(m, "?")
	if s := screen(m, 100, 40); !strings.Contains(s, "Keybindings") || !strings.Contains(s, "reply in the thread") {
		t.Errorf("help:\n%s", s)
	}
	keys(m, "x")
	if m.mode != modeNormal {
		t.Error("help not closed")
	}
	if s := screen(m, 20, 5); !strings.Contains(s, "too small") {
		t.Errorf("tiny: %q", s)
	}
	for _, w := range []int{30, 50, 80, 200} {
		for i, row := range m.View(w, 20) {
			if got := (line{{stripANSI(row), ""}}).width(); got > w {
				t.Errorf("width %d: row %d is %d wide: %q", w, i, got, row)
			}
		}
	}
	empty := NewModel(&fakeBackend{})
	empty.Color = false
	empty.SetSnapshot(&model.Snapshot{})
	if s := screen(empty, 80, 20); !strings.Contains(s, "Nothing watched yet") {
		t.Errorf("empty:\n%s", s)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func TestColorRendering(t *testing.T) {
	m, _ := newModel()
	m.Color = true
	s := screen(m, 100, 30)
	if !strings.Contains(s, "\x1b[48;2;51;51;170m") || !strings.Contains(s, "▌") {
		t.Error("selected message not highlighted")
	}
}

func TestWrap(t *testing.T) {
	tests := []struct {
		in   string
		w    int
		want []string
	}{
		{"hello world", 20, []string{"hello world"}},
		{"hello world again", 11, []string{"hello world", "again"}},
		{"abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"a\n\nb", 5, []string{"a", "", "b"}},
		{"日本語のテキスト", 6, []string{"日本語", "のテキ", "スト"}},
	}
	for _, tt := range tests {
		if got := wrap(tt.in, tt.w); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("wrap(%q, %d) = %q, want %q", tt.in, tt.w, got, tt.want)
		}
	}
}

func TestParseKeys(t *testing.T) {
	got := parseKeys([]byte("a\r\n\x1b\r\x02\x04\x05\x1b[A\x1b"))
	want := []string{"a", kEnter, kCtrlJ, kAltEnter, kCtrlB, kCtrlD, kCtrlE, kUp, kEsc}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseKeys = %q, want %q", got, want)
	}
}
