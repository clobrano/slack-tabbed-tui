package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/clobrano/slack-tabbed-tui/internal/model"
)

func TestFormat(t *testing.T) {
	th := model.Thread{ChannelName: "general", Permalink: "https://acme.slack.com/archives/C1/p1",
		Messages: []model.Message{{Text: "Deploy is stuck"}}}
	n := Format(model.Event{Type: model.EventReply, Author: "alice", URL: "u/1"}, th, "on it")
	if n.Title != "alice replied in #general" || n.Body != "on it" || n.URL != "u/1" || n.Urgent {
		t.Errorf("reply: %+v", n)
	}
	n = Format(model.Event{Type: model.EventMention, Author: "bob"}, th, strings.Repeat("x", 400))
	if n.Title != "bob mentioned you in #general" || !n.Urgent || len([]rune(n.Body)) != 300 || n.URL != th.Permalink {
		t.Errorf("mention: %+v", n)
	}
	th.IsDM, th.ChannelName = true, "carol"
	n = Format(model.Event{Type: model.EventReaction, Author: "carol"}, th, "eyes")
	if n.Title != "carol reacted :eyes: to your message in carol" || n.Body != "Deploy is stuck" {
		t.Errorf("reaction: %+v", n)
	}
}

func TestExec(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out.json")
	n := Notification{Title: "t", Body: "b", Event: model.Event{Type: model.EventReply, ThreadID: "thread:x"}}
	if err := (Exec{Command: "cat > " + out}).Notify(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(out)
	var got Notification
	if json.Unmarshal(data, &got) != nil || got.Title != "t" || got.Event.ThreadID != "thread:x" {
		t.Errorf("exec wrote %s", data)
	}
	if err := (Exec{Command: "exit 3"}).Notify(context.Background(), n); err == nil {
		t.Error("failing command: no error")
	}
}
