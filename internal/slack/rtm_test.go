package slack_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/slack/slacktest"
)

type recorder struct {
	mu     sync.Mutex
	events []slack.Event
	states []slack.StreamState
	got    chan slack.Event
}

func newRecorder() *recorder { return &recorder{got: make(chan slack.Event, 64)} }

func (r *recorder) event(e slack.Event) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
	r.got <- e
}

func (r *recorder) state(s slack.StreamState, _ error) {
	r.mu.Lock()
	r.states = append(r.states, s)
	r.mu.Unlock()
}

func (r *recorder) next(t *testing.T) slack.Event {
	t.Helper()
	select {
	case e := <-r.got:
		return e
	case <-time.After(5 * time.Second):
		t.Fatal("no event")
		return slack.Event{}
	}
}

func TestStream(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	s.AddThread("C1", slack.Message{TS: "1700000000.000100", User: "U1", Text: "parent"})
	rec := newRecorder()
	st := &slack.Stream{Client: s.Client(), PingEvery: 50 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, OnState: rec.state}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- st.Run(ctx, rec.event) }()

	if e := rec.next(t); e.Type != slack.EventConnected {
		t.Fatalf("first event %+v", e)
	}
	// A reply posted elsewhere arrives as a message event.
	if _, err := s.Client().PostReply(ctx, "C1", "1700000000.000100", "hi", false); err != nil {
		t.Fatal(err)
	}
	e := rec.next(t)
	m, err := e.AsMessage()
	if e.Type != "message" || e.Channel != "C1" || e.ThreadTS != "1700000000.000100" || err != nil || m.Text != "hi" {
		t.Fatalf("reply event %+v %+v %v", e, m, err)
	}
	s.Push(map[string]any{"type": "reaction_added", "user": "U1", "reaction": "eyes",
		"item": map[string]string{"type": "message", "channel": "C1", "ts": "1700000000.000100"}})
	if e := rec.next(t); e.Type != "reaction_added" || e.Item == nil || e.Item.TS != "1700000000.000100" || e.Reaction != "eyes" {
		t.Fatalf("reaction event %+v", e)
	}
	s.Push(map[string]any{"type": "message", "subtype": "message_changed", "channel": "C1",
		"message": map[string]any{"ts": "1700000000.000100", "text": "parent (edited)"}})
	if e := rec.next(t); e.Subtype != "message_changed" || e.Message == nil || e.Message.Text != "parent (edited)" {
		t.Fatalf("changed event %+v", e)
	}

	// Pings keep the connection up past the read deadline.
	time.Sleep(300 * time.Millisecond)
	select {
	case e := <-rec.got:
		t.Fatalf("unexpected event while idle: %+v", e)
	default:
	}

	// A dropped connection reconnects, and says so.
	s.DropSockets()
	if e := rec.next(t); e.Type != slack.EventConnected {
		t.Fatalf("after drop: %+v", e)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run returned %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	want := []slack.StreamState{slack.StreamConnecting, slack.StreamLive, slack.StreamDown, slack.StreamConnecting, slack.StreamLive}
	if len(rec.states) < len(want) {
		t.Fatalf("states %v", rec.states)
	}
	for i, w := range want {
		if rec.states[i] != w {
			t.Errorf("states %v, want prefix %v", rec.states, want)
			break
		}
	}
}

func TestStreamLoggedOut(t *testing.T) {
	s := slacktest.New()
	defer s.Close()
	rec := newRecorder()
	st := &slack.Stream{Client: slack.New(s.URL, "xoxc-expired", slacktest.Cookie), OnState: rec.state}
	err := st.Run(context.Background(), rec.event)
	var se *slack.Error
	if !errors.As(err, &se) || !se.Auth() {
		t.Fatalf("Run = %v, want auth error", err)
	}
	if last := rec.states[len(rec.states)-1]; last != slack.StreamLoggedOut {
		t.Errorf("last state %v", last)
	}
}
