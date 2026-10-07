// Command fakeslack serves a fake Slack workspace with sample threads,
// and sets up a session and a watchlist for it, so slack-tabbed-tui can
// be tried (or recorded) without a real workspace:
//
//	go run ./docs/demo/fakeslack -home /tmp/stt-demo
//	XDG_CONFIG_HOME=/tmp/stt-demo/config XDG_STATE_HOME=/tmp/stt-demo/state \
//	  XDG_CACHE_HOME=/tmp/stt-demo/cache XDG_RUNTIME_DIR=/tmp/stt-demo/run slack-tabbed-tui
//
// Every few seconds someone replies in one of the threads.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/creds"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/slack/slacktest"
)

func user(id, name, display string, bot bool) slack.User {
	u := slack.User{ID: id, Name: name, IsBot: bot}
	u.Profile.DisplayName = display
	return u
}

func main() {
	home := flag.String("home", "", "directory for the demo's config, state and cache (required)")
	every := flag.Duration("every", 8*time.Second, "how often someone replies")
	flag.Parse()
	if *home == "" {
		log.Fatal("-home is required")
	}
	s := slacktest.New()
	s.Auth.URL = "https://acme.slack.com/"
	s.Users = []slack.User{
		user("U0ME", "me", "Carlo", false),
		user("U1", "alice", "Alice", false),
		user("U2", "bob", "Bob", false),
		user("U3", "deploybot", "Deploy Bot", true),
	}
	s.Groups = []slack.UserGroup{{ID: "S1", Handle: "oncall", Name: "On call"}}
	s.Convs["C01INFRA"] = slack.Conversation{ID: "C01INFRA", Name: "team-infra", IsChannel: true, IsMember: true}
	s.Convs["C02REL"] = slack.Conversation{ID: "C02REL", Name: "releases", IsChannel: true, IsMember: true}
	s.Convs["D01BOB"] = slack.Conversation{ID: "D01BOB", IsIM: true, User: "U2"}
	base := time.Now().Add(-3 * time.Hour).Unix()
	ts := func(min int) string { return fmt.Sprintf("%d.000100", base+int64(min)*60) }
	s.AddThread("C01INFRA",
		slack.Message{TS: ts(0), User: "U1", Text: "Staging deploy is stuck at the migration step, <!subteam^S1> can someone look?"},
		slack.Message{TS: ts(4), User: "U0ME", Text: "Looking. The lock on `schema_migrations` is held by an old pod."},
		slack.Message{TS: ts(9), User: "U3", BotProfile: &slack.BotProfile{Name: "Deploy Bot"}, BotID: "B1", Text: "Deployment *staging-42* failed",
			Attachments: []slack.Attachment{{Title: "Logs: https://ci.example/runs/42"}}, Reactions: []slack.Reaction{{Name: "eyes", Count: 2, Users: []string{"U1", "U0ME"}}}},
		slack.Message{TS: ts(15), User: "U2", Text: "<@U0ME> I killed the pod, can you retry?", Files: []slack.File{{Name: "pods.txt", Size: 3400}}},
	)
	s.AddThread("C02REL",
		slack.Message{TS: ts(30), User: "U2", Text: "Release 1.8 checklist :rocket:"},
		slack.Message{TS: ts(31), User: "U1", Text: "Docs are merged."},
	)
	s.AddThread("D01BOB", slack.Message{TS: ts(60), User: "U2", Text: "Got a minute for the on-call handover?"})

	dirs := map[string]string{}
	for _, d := range []string{"config", "state", "cache", "run"} {
		dirs[d] = filepath.Join(*home, d)
		if err := os.MkdirAll(dirs[d], 0o700); err != nil {
			log.Fatal(err)
		}
	}
	app := filepath.Join(dirs["config"], "slack-tabbed-tui")
	store := creds.Store{Path: filepath.Join(app, "credentials.json"), Getenv: func(string) string { return "" }}
	if err := store.Save(creds.Credential{Workspace: "acme.slack.com", URL: s.URL + "/", TeamID: "T0ACME", Team: "Acme",
		UserID: "U0ME", User: "me", Token: slacktest.Token, Cookie: slacktest.Cookie}); err != nil {
		log.Fatal(err)
	}
	watch := "https://acme.slack.com/archives/C01INFRA/p" + ts(0)[:10] + "000100\n" +
		"https://acme.slack.com/archives/C02REL/p" + ts(30)[:10] + "000100\n" +
		"https://acme.slack.com/archives/D01BOB/p" + ts(60)[:10] + "000100\n"
	if err := os.WriteFile(filepath.Join(app, "watch"), []byte(watch), 0o600); err != nil {
		log.Fatal(err)
	}
	log.Printf("fake Slack at %s; config in %s", s.URL, *home)

	lines := []struct{ ch, thread, user, text string }{
		{"C01INFRA", ts(0), "U1", "Retried, it is past the migration now :tada:"},
		{"C02REL", ts(30), "U2", "<@U0ME> can you tag the release?"},
		{"C01INFRA", ts(0), "U3", "Deployment *staging-43* succeeded"},
		{"D01BOB", ts(60), "U2", "No rush, after lunch is fine"},
	}
	for i := 0; ; i++ {
		time.Sleep(*every)
		l := lines[i%len(lines)]
		s.Reply(l.ch, l.thread, l.user, l.text, true)
	}
}
