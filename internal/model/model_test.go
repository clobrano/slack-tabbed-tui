package model

import "testing"

func thread() Thread {
	return Thread{
		ID: "thread:acme.slack.com/C1/1700000000.000100", Workspace: "acme.slack.com", Channel: "C1", ThreadTS: "1700000000.000100",
		Permalink: "https://acme.slack.com/archives/C1/p1700000000000100",
		Messages: []Message{
			{TS: "1700000000.000100", Author: "alice", Text: "Deploy  is\nstuck"},
			{TS: "1700000100.000100", Author: "me", Mine: true, Text: "looking"},
			{TS: "1700000200.000100", Author: "bob", Text: "@me see logs", MentionsMe: true},
			{TS: "1700000300.000100", Author: "ci", Bot: true, Text: "build failed"},
		},
		ReadTS: "1700000100.000100",
	}
}

func TestUnread(t *testing.T) {
	th := thread()
	if u, m := th.Unread(); u != 2 || m != 1 {
		t.Errorf("Unread = %d, %d", u, m)
	}
	th.ReadTS = th.Latest()
	if u, _ := th.Unread(); u != 0 {
		t.Errorf("after reading: %d", u)
	}
	th.ReadTS = ""
	if u, _ := th.Unread(); u != 3 {
		t.Errorf("never read: %d (my own message is never unread)", u)
	}
}

func TestTitleAndLinks(t *testing.T) {
	th := thread()
	if th.Title() != "Deploy is stuck" {
		t.Errorf("Title = %q", th.Title())
	}
	if got := th.MessageLink("1700000200.000100"); got != "https://acme.slack.com/archives/C1/p1700000200000100?thread_ts=1700000000.000100&cid=C1" {
		t.Errorf("reply link %q", got)
	}
	if got := th.MessageLink(th.ThreadTS); got != th.Permalink {
		t.Errorf("parent link %q", got)
	}
	if th.Latest() != "1700000300.000100" || (Thread{ThreadTS: "1.000001"}).Latest() != "1.000001" {
		t.Error("Latest")
	}
}

func TestTSTime(t *testing.T) {
	if !After("1700000000.000200", "1700000000.000100") || After("1700000000.000100", "1700000000.000100") || !After("1700000000.1", "") {
		t.Error("After")
	}
	if !TSTime("x.1").IsZero() {
		t.Error("bad ts")
	}
}

func TestSnapshotFind(t *testing.T) {
	s := Snapshot{Threads: []Thread{thread()}, Workspaces: []Workspace{{Host: "acme.slack.com", Conn: ConnLive}}}
	if s.Find(thread().ID) == nil || s.Find("x") != nil || s.Workspace("acme.slack.com").Conn != ConnLive || s.Workspace("x") != nil {
		t.Error("Find/Workspace")
	}
	if !DefaultSettings().Events[EventMention] || DefaultSettings().Events[EventReaction] {
		t.Error("defaults")
	}
}
