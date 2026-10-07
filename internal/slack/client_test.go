package slack_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/slack/slacktest"
)

func thread(n int) []slack.Message {
	msgs := make([]slack.Message, n)
	for i := range msgs {
		msgs[i] = slack.Message{TS: fmt.Sprintf("17000000%02d.000100", i), User: "U1", Text: fmt.Sprintf("msg %d", i)}
	}
	return msgs
}

func TestAuthTest(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	a, err := s.Client().AuthTest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if a.TeamID != "T0ACME" || a.UserID != "U0ME" || a.URL != s.URL+"/" {
		t.Errorf("AuthTest = %+v", a)
	}
}

func TestBadSession(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	for _, c := range []*slack.Client{
		slack.New(s.URL, "xoxc-wrong", slacktest.Cookie),
		slack.New(s.URL, slacktest.Token, ""),
	} {
		_, err := c.AuthTest(context.Background())
		var e *slack.Error
		if !errors.As(err, &e) || !e.Auth() || e.Code != "invalid_auth" {
			t.Errorf("err = %v, want invalid_auth", err)
		}
	}
}

func TestRateLimited(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.RateLimit = 1
	_, err := s.Client().AuthTest(context.Background())
	var e *slack.Error
	if !errors.As(err, &e) || !e.RateLimited() || e.RetryAfter != 3*time.Second {
		t.Fatalf("err = %#v, want rate limited with Retry-After 3s", err)
	}
	if _, err := s.Client().AuthTest(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
}

func TestRepliesPaginates(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.PageSize = 3
	s.AddThread("C1", thread(8)...)
	msgs, err := s.Client().Replies(context.Background(), "C1", "1700000000.000100", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 8 || msgs[0].Text != "msg 0" || msgs[7].Text != "msg 7" {
		t.Fatalf("got %d messages: %+v", len(msgs), msgs)
	}
	if n := s.CallCount("conversations.replies"); n != 3 {
		t.Errorf("conversations.replies called %d times, want 3", n)
	}
	newer, err := s.Client().Replies(context.Background(), "C1", "1700000000.000100", "1700000005.000100")
	if err != nil {
		t.Fatal(err)
	}
	if len(newer) != 3 || newer[1].Text != "msg 6" {
		t.Errorf("with oldest: %+v", newer)
	}
}

func TestRepliesNotFound(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	_, err := s.Client().Replies(context.Background(), "C1", "1700000000.000100", "")
	var e *slack.Error
	if !errors.As(err, &e) || e.Code != "thread_not_found" || e.Auth() {
		t.Errorf("err = %v", err)
	}
}

func TestUsersPaginates(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.PageSize = 2
	for i := range 5 {
		s.Users = append(s.Users, slack.User{ID: fmt.Sprintf("U%d", i), Name: fmt.Sprintf("u%d", i), IsBot: i == 4})
	}
	users, err := s.Client().Users(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 5 || !users[4].IsBot {
		t.Errorf("users = %+v", users)
	}
}

func TestPostReply(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.AddThread("C1", thread(1)...)
	c := s.Client()
	m, err := c.PostReply(context.Background(), "C1", "1700000000.000100", "hello <@U1>", true)
	if err != nil {
		t.Fatal(err)
	}
	if m.Text != "hello <@U1>" || m.User != "U0ME" || m.Subtype != "thread_broadcast" {
		t.Errorf("posted %+v", m)
	}
	msgs, _ := c.Replies(context.Background(), "C1", "1700000000.000100", "")
	if len(msgs) != 2 || msgs[1].TS != m.TS {
		t.Errorf("thread after post: %+v", msgs)
	}
}

func TestTSTime(t *testing.T) {
	got := slack.TSTime("1700000000.123456")
	if got.Unix() != 1700000000 || got.Nanosecond() != 123456000 {
		t.Errorf("TSTime = %v", got)
	}
	if !slack.TSTime("garbage").IsZero() {
		t.Error("TSTime(garbage) is not zero")
	}
}
