package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/ws"
)

// RTMInfo is the answer of rtm.connect: where the event stream is.
type RTMInfo struct {
	URL  string `json:"url"`
	Self struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"self"`
	Team struct {
		ID     string `json:"id"`
		Domain string `json:"domain"`
	} `json:"team"`
}

// RTMConnect asks for a WebSocket URL for the event stream, the one the
// web client listens to.
func (c *Client) RTMConnect(ctx context.Context) (RTMInfo, error) {
	var info RTMInfo
	err := c.Call(ctx, "rtm.connect", nil, &info)
	return info, err
}

// Event is one event of the stream. Only the fields the app uses are
// decoded; Raw has the rest.
type Event struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	Channel   string `json:"channel,omitempty"`
	User      string `json:"user,omitempty"`
	TS        string `json:"ts,omitempty"`
	ThreadTS  string `json:"thread_ts,omitempty"`
	DeletedTS string `json:"deleted_ts,omitempty"`
	// Message is the new version for message_changed, the parent for
	// message_replied.
	Message  *Message `json:"message,omitempty"`
	Reaction string   `json:"reaction,omitempty"`
	Item     *struct {
		Type    string `json:"type"`
		Channel string `json:"channel"`
		TS      string `json:"ts"`
	} `json:"item,omitempty"`

	Raw json.RawMessage `json:"-"`
}

// AsMessage returns the message carried by a plain "message" event (a
// new message or reply).
func (e Event) AsMessage() (Message, error) {
	var m Message
	err := json.Unmarshal(e.Raw, &m)
	return m, err
}

// Event types the stream adds to Slack's own.
const (
	// EventConnected is sent after each (re)connection: whatever
	// happened while disconnected was missed and must be fetched again.
	EventConnected = "stt_connected"
)

// StreamState is the connection state of a Stream.
type StreamState int

const (
	StreamConnecting StreamState = iota
	StreamLive
	StreamDown      // disconnected, retrying
	StreamLoggedOut // the session is no longer valid; Run returns
)

func (s StreamState) String() string {
	return [...]string{"connecting", "live", "down", "logged out"}[s]
}

// Stream keeps a connection to a workspace's event stream, reconnecting
// with exponential backoff.
type Stream struct {
	Client *Client
	// PingEvery is how often to ping; a connection with no traffic for
	// twice as long is considered dead. Default 30s.
	PingEvery time.Duration
	// MaxBackoff caps the wait between reconnections. Default 5m.
	MaxBackoff time.Duration
	// OnState, if set, is called on every state change with the error
	// that caused it, if any.
	OnState func(StreamState, error)
}

// Run delivers events to fn until ctx is done or the session is logged
// out (then it returns the *Error). fn runs on the reading goroutine.
func (s *Stream) Run(ctx context.Context, fn func(Event)) error {
	backoff := time.Second
	for {
		s.state(StreamConnecting, nil)
		start := time.Now()
		err := s.once(ctx, fn)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		var se *Error
		if errors.As(err, &se) && se.Auth() {
			s.state(StreamLoggedOut, err)
			return err
		}
		s.state(StreamDown, err)
		if time.Since(start) > time.Minute {
			backoff = time.Second // it was up for a while: start over
		}
		wait := backoff
		if errors.As(err, &se) && se.RetryAfter > wait {
			wait = se.RetryAfter
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		backoff = min(backoff*2, s.maxBackoff())
	}
}

func (s *Stream) maxBackoff() time.Duration {
	if s.MaxBackoff > 0 {
		return s.MaxBackoff
	}
	return 5 * time.Minute
}

func (s *Stream) state(st StreamState, err error) {
	if s.OnState != nil {
		s.OnState(st, err)
	}
}

func (s *Stream) once(ctx context.Context, fn func(Event)) error {
	info, err := s.Client.RTMConnect(ctx)
	if err != nil {
		return err
	}
	h := http.Header{}
	if s.Client.Cookie != "" {
		h.Set("Cookie", "d="+s.Client.Cookie)
	}
	dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, err := ws.Dial(dctx, info.URL, h)
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	every := s.PingEvery
	if every <= 0 {
		every = 30 * time.Second
	}
	var id atomic.Int64
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if conn.WriteJSON(map[string]any{"id": id.Add(1), "type": "ping"}) != nil {
					return
				}
			}
		}
	}()

	for {
		_ = conn.SetReadDeadline(time.Now().Add(2*every + 5*time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var ev Event
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		ev.Raw = data
		switch ev.Type {
		case "hello":
			s.state(StreamLive, nil)
			fn(Event{Type: EventConnected})
		case "pong", "":
		case "goodbye":
			return errors.New("slack: server asked to reconnect")
		case "error":
			return fmt.Errorf("slack: stream error: %s", data)
		default:
			fn(ev)
		}
	}
}
