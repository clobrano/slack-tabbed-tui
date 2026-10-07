package directory

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/slack/slacktest"
)

func user(id, display, real string, bot bool) slack.User {
	u := slack.User{ID: id, Name: "h" + id, IsBot: bot}
	u.Profile.DisplayName, u.Profile.RealName = display, real
	return u
}

func TestResolveAndCache(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.Users = []slack.User{user("U1", "alice", "Alice A", false), user("U2", "", "Bob B", false), user("U3", "", "", true)}
	s.Convs["C1"] = slack.Conversation{ID: "C1", Name: "general", IsChannel: true}
	s.Convs["D1"] = slack.Conversation{ID: "D1", IsIM: true, User: "U2"}
	path := filepath.Join(t.TempDir(), "T1", "directory.json")

	d := Load(path)
	if err := d.Resolve(context.Background(), s.Client(), []string{"U1", "U3"}, []string{"C1", "D1"}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"U1": "alice", "U2": "Bob B", "U3": "hU3"} {
		if got := d.UserName(id); got != want {
			t.Errorf("UserName(%s) = %q, want %q", id, got, want)
		}
	}
	if d.ChannelName("C1") != "general" || d.ChannelName("D1") != "Bob B" {
		t.Errorf("channels: %q %q", d.ChannelName("C1"), d.ChannelName("D1"))
	}
	if err := d.Save(); err != nil {
		t.Fatal(err)
	}

	// Cached: no more calls.
	calls := len(s.Calls)
	d2 := Load(path)
	if err := d2.Resolve(context.Background(), s.Client(), []string{"U1", "U2"}, []string{"C1"}); err != nil {
		t.Fatal(err)
	}
	if len(s.Calls) != calls || d2.UserName("U1") != "alice" {
		t.Errorf("cache not used: %v", s.Calls[calls:])
	}

	// Unknown IDs: error, but the rest resolves.
	d3 := Load("")
	err := d3.Resolve(context.Background(), s.Client(), []string{"U9", "U1"}, nil)
	if err == nil || d3.UserName("U1") != "alice" {
		t.Errorf("partial resolve: %v %q", err, d3.UserName("U1"))
	}
}

func TestSync(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.PageSize = 1
	s.Users = []slack.User{user("U1", "alice", "", false), user("B1", "", "Deploy Bot", true)}
	s.Groups = []slack.UserGroup{{ID: "S1", Handle: "oncall"}}
	d := Load("")
	if err := d.Sync(context.Background(), s.Client()); err != nil {
		t.Fatal(err)
	}
	if d.UserName("B1") != "Deploy Bot" || d.GroupHandle("S1") != "oncall" {
		t.Errorf("after sync: %q %q", d.UserName("B1"), d.GroupHandle("S1"))
	}
	if got := slack.PlainText("<@U1> <!subteam^S1>", d); got != "@alice @oncall" {
		t.Errorf("PlainText via directory = %q", got)
	}
}

func TestAuthorAndMentioned(t *testing.T) {
	d := Load("")
	d.PutUser(user("U1", "alice", "", false))
	tests := []struct {
		m    slack.Message
		want string
	}{
		{slack.Message{User: "U1"}, "alice"},
		{slack.Message{User: "U9"}, "U9"},
		{slack.Message{BotID: "B1", BotProfile: &slack.BotProfile{Name: "CI"}}, "CI"},
		{slack.Message{BotID: "B1", Username: "webhook"}, "webhook"},
		{slack.Message{BotID: "B1"}, "B1"},
	}
	for _, tt := range tests {
		if got := d.Author(tt.m); got != tt.want {
			t.Errorf("Author(%+v) = %q, want %q", tt.m, got, tt.want)
		}
	}
	ids := Mentioned([]slack.Message{
		{User: "U1", Text: "hi <@U2> and <@U3|carol> and <@U1>"},
		{User: "U4", Reactions: []slack.Reaction{{Name: "+1", Users: []string{"U5", "U1"}}}},
		{BotID: "B1", Text: "broken <@U6"},
	})
	if want := []string{"U1", "U2", "U3", "U4", "U5"}; !slices.Equal(ids, want) {
		t.Errorf("Mentioned = %v, want %v", ids, want)
	}
}
