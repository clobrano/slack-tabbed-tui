package complete

import (
	"strings"
	"testing"

	"github.com/clobrano/slack-tabbed-tui/internal/model"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
)

type dir struct {
	users    []slack.User
	groups   []slack.UserGroup
	channels []slack.Conversation
	emoji    []string
}

func (d dir) Users() []slack.User            { return d.users }
func (d dir) Groups() []slack.UserGroup      { return d.groups }
func (d dir) Channels() []slack.Conversation { return d.channels }
func (d dir) CustomEmoji() []string          { return d.emoji }

func user(id, handle, display, real string) slack.User {
	u := slack.User{ID: id, Name: handle}
	u.Profile.DisplayName, u.Profile.RealName = display, real
	return u
}

func testDir() dir {
	bot := user("B1", "deploybot", "Deploy Bot", "")
	bot.IsBot = true
	app := user("A1", "github", "", "GitHub")
	app.IsAppUser = true
	gone := user("U9", "alfred", "Alfred", "")
	gone.Deleted = true
	return dir{
		users: []slack.User{
			user("U0", "me", "Carlo", "Carlo L"),
			user("U1", "alice", "Alice", "Alice Smith"),
			user("U2", "albert", "Al", "Albert Jones"),
			user("U3", "bob", "", "Bob Alden"),
			user("U4", "jose", "José", "José García"),
			bot, app, gone,
		},
		groups:   []slack.UserGroup{{ID: "S1", Handle: "oncall", Name: "On call"}, {ID: "S2", Handle: "alerts-team", Name: "Alerts"}},
		channels: []slack.Conversation{{ID: "C1", Name: "general", IsMember: true}, {ID: "C2", Name: "dev-alerts", IsMember: true}, {ID: "C3", Name: "alerts", IsPrivate: true}},
		emoji:    []string{"partyparrot", "party-blob", "shipit"},
	}
}

func labels(cs []model.Candidate) string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Label)
	}
	return strings.Join(out, ",")
}

func TestPeople(t *testing.T) {
	d := testDir()
	w := Where{Participants: []string{"U3", "U1"}, Members: []string{"U2", "B1"}, Me: "U0"}
	tests := []struct{ q, want string }{
		// Name starts with "al": participant Alice first, then members,
		// then groups and others; Bob Alden matches on a word, later.
		{"al", "@Alice,@Al,@alerts-team,@Bob Alden"},
		{"dep", "@Deploy Bot"},
		{"git", "@GitHub"},
		{"jose", "@José"},
		{"on", "@oncall"},
		{"he", "@here"},
		{"ch", "@channel"},
		{"carlo", ""}, // never yourself
		{"alfred", ""},
		{"zzz", ""},
	}
	for _, tt := range tests {
		if got := labels(People(d, w, tt.q)); got != tt.want {
			t.Errorf("People(%q) = %q, want %q", tt.q, got, tt.want)
		}
	}
	// No query: thread participants first, most recent first.
	if got := People(d, w, ""); len(got) != Limit || got[0].Label != "@Bob Alden" || got[1].Label != "@Alice" {
		t.Errorf("People(\"\") = %q", labels(got))
	}
	// Tokens and kinds.
	got := People(d, w, "deploy")
	if got[0].Token != "<@B1>" || got[0].Kind != "bot" || got[0].Detail != "@deploybot · app" {
		t.Errorf("bot candidate %+v", got[0])
	}
	if got := People(d, w, "oncall"); got[0].Token != "<!subteam^S1>" || got[0].Kind != "group" {
		t.Errorf("group candidate %+v", got[0])
	}
	if got := People(d, w, "here"); got[0].Token != "<!here>" {
		t.Errorf("special %+v", got[0])
	}
	// No @here in a DM.
	if got := labels(People(d, Where{DM: true}, "her")); got != "" {
		t.Errorf("DM: %q", got)
	}
}

func TestChannels(t *testing.T) {
	d := testDir()
	if got := labels(Channels(d, "al")); got != "#alerts,#dev-alerts" {
		t.Errorf("Channels(al) = %q", got)
	}
	got := Channels(d, "gen")
	if len(got) != 1 || got[0].Token != "<#C1>" {
		t.Errorf("Channels(gen) = %+v", got)
	}
	if c := Channels(d, "alerts")[0]; c.Detail != "private" {
		t.Errorf("private channel %+v", c)
	}
}

func TestEmoji(t *testing.T) {
	d := testDir()
	got := Emoji(d, "party")
	if len(got) < 3 || got[0].Label != ":partying_face:" && got[0].Label != ":party_popper:" && !strings.HasPrefix(got[0].Label, ":party") {
		t.Errorf("Emoji(party) = %q", labels(got))
	}
	found := false
	for _, c := range got {
		if c.Label == ":partyparrot:" && c.Detail == "custom" && c.Emoji == "" {
			found = true
		}
	}
	if !found {
		t.Errorf("custom emoji missing: %q", labels(got))
	}
	if got := Emoji(d, "rocket"); got[0].Label != ":rocket:" || got[0].Emoji != "🚀" || got[0].Token != ":rocket:" {
		t.Errorf("Emoji(rocket) = %+v", got[0])
	}
	if got := Emoji(d, "shipi"); labels(got) != ":shipit:" {
		t.Errorf("Emoji(shipi) = %q", labels(got))
	}
}

func TestFold(t *testing.T) {
	if fold("José GARCÍA Øre") != "jose garcia ore" {
		t.Errorf("fold = %q", fold("José GARCÍA Øre"))
	}
}
