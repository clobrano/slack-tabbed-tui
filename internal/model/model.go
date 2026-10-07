// Package model is the state the daemon publishes and every client
// shows: the watched threads, ready to display (names resolved, markup
// rendered), the workspaces' connections and the notification settings.
// The snapshot is a versioned contract, also written to disk for other
// tools.
package model

import (
	"strings"
	"time"
)

// SchemaVersion is the snapshot format version.
const SchemaVersion = 1

// Snapshot is the whole shared state.
type Snapshot struct {
	Schema      int         `json:"schema"`
	GeneratedAt time.Time   `json:"generated_at"`
	Threads     []Thread    `json:"threads"`
	Workspaces  []Workspace `json:"workspaces,omitempty"`
	Settings    Settings    `json:"settings"`
}

// Find returns the thread with the given ID, or nil.
func (s *Snapshot) Find(id string) *Thread {
	for i := range s.Threads {
		if s.Threads[i].ID == id {
			return &s.Threads[i]
		}
	}
	return nil
}

// Workspace returns the workspace a thread belongs to, or nil.
func (s *Snapshot) Workspace(host string) *Workspace {
	for i := range s.Workspaces {
		if s.Workspaces[i].Host == host {
			return &s.Workspaces[i]
		}
	}
	return nil
}

// Conn is the state of a workspace's live connection.
type Conn string

const (
	ConnConnecting Conn = "connecting"
	ConnLive       Conn = "live"
	ConnDown       Conn = "down"       // retrying; threads are polled meanwhile
	ConnLoggedOut  Conn = "logged_out" // the session ended: sign in again
	ConnNoSession  Conn = "no_session" // never signed in to this workspace
)

// Workspace is a workspace with watched threads.
type Workspace struct {
	Host   string `json:"host"` // as in thread IDs, e.g. acme.slack.com
	Team   string `json:"team,omitempty"`
	UserID string `json:"user_id,omitempty"` // the signed-in user
	Conn   Conn   `json:"conn"`
	Error  string `json:"error,omitempty"`
}

// Thread is one watched thread.
type Thread struct {
	ID          string    `json:"id"` // thread:<workspace>/<channel>/<ts>
	Workspace   string    `json:"workspace"`
	Channel     string    `json:"channel"`
	ThreadTS    string    `json:"thread_ts"`
	ChannelName string    `json:"channel_name,omitempty"` // "general", or the person of a DM
	IsDM        bool      `json:"is_dm,omitempty"`
	Permalink   string    `json:"permalink"`
	Messages    []Message `json:"messages,omitempty"` // parent first
	// ReadTS is the newest message the user has seen.
	ReadTS string `json:"read_ts,omitempty"`
	// Alerts turns notifications on for the thread.
	Alerts    bool      `json:"alerts,omitempty"`
	Error     string    `json:"error,omitempty"`
	FetchedAt time.Time `json:"fetched_at,omitempty"`
}

// Message is one message of a thread, ready to display.
type Message struct {
	TS          string     `json:"ts"`
	AuthorID    string     `json:"author_id,omitempty"`
	Author      string     `json:"author"`
	Bot         bool       `json:"bot,omitempty"`
	Mine        bool       `json:"mine,omitempty"`
	Text        string     `json:"text"` // plain text, mentions as @names
	Edited      bool       `json:"edited,omitempty"`
	Broadcast   bool       `json:"broadcast,omitempty"` // also sent to the channel
	MentionsMe  bool       `json:"mentions_me,omitempty"`
	Reactions   []Reaction `json:"reactions,omitempty"`
	Files       []File     `json:"files,omitempty"`
	Attachments []string   `json:"attachments,omitempty"` // bot attachments and unfurls, as text
}

// Reaction is one emoji on a message.
type Reaction struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Mine  bool     `json:"mine,omitempty"`
	Users []string `json:"users,omitempty"` // names
}

// File is a file attached to a message.
type File struct {
	Name string `json:"name"`
	Size int64  `json:"size,omitempty"`
	URL  string `json:"url,omitempty"` // permalink in Slack
}

// Time is when the message was posted.
func (m Message) Time() time.Time { return TSTime(m.TS) }

// TSTime converts a Slack timestamp to a time.
func TSTime(ts string) time.Time {
	sec, frac, _ := strings.Cut(ts, ".")
	var s, us int64
	for _, c := range sec {
		if c < '0' || c > '9' {
			return time.Time{}
		}
		s = s*10 + int64(c-'0')
	}
	frac = (frac + "000000")[:6]
	for _, c := range frac {
		if c < '0' || c > '9' {
			break
		}
		us = us*10 + int64(c-'0')
	}
	return time.Unix(s, us*1000)
}

// After reports whether Slack timestamp a is later than b.
func After(a, b string) bool { return TSTime(a).After(TSTime(b)) }

// Title is a short name for the thread: the start of its parent message.
func (t Thread) Title() string {
	if len(t.Messages) == 0 {
		return ""
	}
	return strings.Join(strings.Fields(t.Messages[0].Text), " ")
}

// Unread counts the messages from others newer than ReadTS.
func (t Thread) Unread() (unread, mentions int) {
	for _, m := range t.Messages {
		if m.Mine || !After(m.TS, t.ReadTS) {
			continue
		}
		unread++
		if m.MentionsMe {
			mentions++
		}
	}
	return
}

// Latest is the ts of the newest message.
func (t Thread) Latest() string {
	if len(t.Messages) == 0 {
		return t.ThreadTS
	}
	return t.Messages[len(t.Messages)-1].TS
}

// MessageLink is the web link to one message of the thread.
func (t Thread) MessageLink(ts string) string {
	base, _, _ := strings.Cut(t.Permalink, "/archives/")
	if base == t.Permalink || base == "" {
		return t.Permalink
	}
	p := base + "/archives/" + t.Channel + "/p" + strings.Replace(ts, ".", "", 1)
	if ts != t.ThreadTS {
		p += "?thread_ts=" + t.ThreadTS + "&cid=" + t.Channel
	}
	return p
}

// EventType is a kind of notification-worthy event.
type EventType string

const (
	EventReply    EventType = "reply"    // a new reply from someone else
	EventMention  EventType = "mention"  // a new reply that mentions you, @here or @channel
	EventReaction EventType = "reaction" // a reaction to one of your messages
)

// EventTypes lists every event type in display order.
var EventTypes = []EventType{EventReply, EventMention, EventReaction}

// Label is the human description of an event type.
func (e EventType) Label() string {
	switch e {
	case EventReply:
		return "new reply"
	case EventMention:
		return "reply mentioning you (or @here, @channel)"
	case EventReaction:
		return "reaction to your message"
	}
	return string(e)
}

// Settings are the notification settings shared by every client.
type Settings struct {
	Mute   bool               `json:"mute"`
	Events map[EventType]bool `json:"events"`
}

// DefaultSettings: replies and mentions notify, reactions do not.
func DefaultSettings() Settings {
	return Settings{Events: map[EventType]bool{EventReply: true, EventMention: true, EventReaction: false}}
}

// Event is something that happened in a thread, for notifications.
type Event struct {
	Type     EventType `json:"type"`
	ThreadID string    `json:"thread_id"`
	TS       string    `json:"ts"`
	Author   string    `json:"author,omitempty"`
	URL      string    `json:"url,omitempty"`
}
