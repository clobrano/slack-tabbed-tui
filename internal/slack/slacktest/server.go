// Package slacktest is a fake Slack Web API for tests: an in-memory
// workspace served over httptest, accepting one session.
package slacktest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/ws"
)

// Session is the only token and cookie the server accepts.
const (
	Token  = "xoxc-test-token"
	Cookie = "xoxd-test%2Fcookie"
)

// Server is a fake workspace. Set its fields before making calls; they
// are guarded by the server's lock while it serves.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	Auth     slack.AuthInfo
	Users    []slack.User
	Groups   []slack.UserGroup
	Convs    map[string]slack.Conversation
	Threads  map[string][]slack.Message // key: channel + "/" + thread ts
	PageSize int                        // messages/users per page, default 200
	// RateLimit makes the next N calls fail with HTTP 429.
	RateLimit int
	Calls     []string // methods called, in order
	nextTS    int64

	sockMu  sync.Mutex
	sockets []*ws.Conn
	// SocketOpened receives a value each time an event socket connects.
	SocketOpened chan struct{}
}

// New starts a fake workspace with a signed-in user "me" (U0ME).
func New() *Server {
	s := &Server{
		Convs:        map[string]slack.Conversation{},
		Threads:      map[string][]slack.Message{},
		nextTS:       1800000000,
		SocketOpened: make(chan struct{}, 16),
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	s.Auth = slack.AuthInfo{URL: s.URL + "/", Team: "Acme", TeamID: "T0ACME", User: "me", UserID: "U0ME"}
	return s
}

// Client returns a client signed in to the fake workspace.
func (s *Server) Client() *slack.Client { return slack.New(s.URL, Token, Cookie) }

// AddThread stores a thread; the first message is the parent.
func (s *Server) AddThread(channel string, msgs ...slack.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(msgs) == 0 {
		return
	}
	ts := msgs[0].TS
	for i := range msgs {
		msgs[i].ThreadTS = ts
		if msgs[i].Type == "" {
			msgs[i].Type = "message"
		}
	}
	s.Threads[channel+"/"+ts] = msgs
}

// CallCount returns how many times method was called.
func (s *Server) CallCount(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.Calls {
		if c == method {
			n++
		}
	}
	return n
}

// Push sends an event to every connected event socket.
func (s *Server) Push(event any) {
	s.sockMu.Lock()
	defer s.sockMu.Unlock()
	for _, c := range s.sockets {
		_ = c.WriteJSON(event)
	}
}

// DropSockets closes every event socket, as a network failure would.
func (s *Server) DropSockets() {
	s.sockMu.Lock()
	defer s.sockMu.Unlock()
	for _, c := range s.sockets {
		c.Close()
	}
	s.sockets = nil
}

// serveSocket is the event stream: "hello", then pushed events; pings
// are answered with pongs.
func (s *Server) serveSocket(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("d"); err != nil || c.Value != Cookie || r.URL.Query().Get("t") != Token {
		http.Error(w, "invalid_auth", http.StatusUnauthorized)
		return
	}
	c, err := ws.Accept(w, r)
	if err != nil {
		return
	}
	s.sockMu.Lock()
	_ = c.WriteJSON(map[string]string{"type": "hello"})
	s.sockets = append(s.sockets, c)
	s.sockMu.Unlock()
	s.SocketOpened <- struct{}{}
	for {
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var ping struct {
			ID   int    `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &ping) == nil && ping.Type == "ping" {
			s.sockMu.Lock()
			_ = c.WriteJSON(map[string]any{"type": "pong", "reply_to": ping.ID})
			s.sockMu.Unlock()
		}
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/websocket" {
		s.serveSocket(w, r)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	method := strings.TrimPrefix(r.URL.Path, "/api/")
	s.Calls = append(s.Calls, method)
	if s.RateLimit > 0 {
		s.RateLimit--
		w.Header().Set("Retry-After", "3")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c, err := r.Cookie("d")
	if r.PostForm.Get("token") != Token || err != nil || c.Value != Cookie {
		reply(w, fail("invalid_auth"))
		return
	}
	f := r.PostForm
	switch method {
	case "auth.test":
		reply(w, ok(s.Auth))
	case "conversations.replies":
		msgs, found := s.Threads[f.Get("channel")+"/"+f.Get("ts")]
		if !found {
			reply(w, fail("thread_not_found"))
			return
		}
		if oldest := f.Get("oldest"); oldest != "" {
			kept := []slack.Message{msgs[0]}
			for _, m := range msgs[1:] {
				if slack.TSTime(m.TS).After(slack.TSTime(oldest)) {
					kept = append(kept, m)
				}
			}
			msgs = kept
		}
		page, next := s.page(len(msgs), f.Get("cursor"))
		reply(w, withCursor(map[string]any{"messages": msgs[page[0]:page[1]], "has_more": next != ""}, next))
	case "conversations.info":
		cv, found := s.Convs[f.Get("channel")]
		if !found {
			reply(w, fail("channel_not_found"))
			return
		}
		reply(w, ok(map[string]any{"channel": cv}))
	case "users.info":
		i := slices.IndexFunc(s.Users, func(u slack.User) bool { return u.ID == f.Get("user") })
		if i < 0 {
			reply(w, fail("user_not_found"))
			return
		}
		reply(w, ok(map[string]any{"user": s.Users[i]}))
	case "users.list":
		page, next := s.page(len(s.Users), f.Get("cursor"))
		reply(w, withCursor(map[string]any{"members": s.Users[page[0]:page[1]]}, next))
	case "rtm.connect":
		reply(w, ok(map[string]any{
			"url":  "ws" + strings.TrimPrefix(s.URL, "http") + "/websocket?t=" + Token,
			"self": map[string]string{"id": s.Auth.UserID, "name": s.Auth.User},
			"team": map[string]string{"id": s.Auth.TeamID, "domain": "acme"},
		}))
	case "usergroups.list":
		reply(w, ok(map[string]any{"usergroups": s.Groups}))
	case "chat.postMessage":
		key := f.Get("channel") + "/" + f.Get("thread_ts")
		if _, found := s.Threads[key]; !found {
			reply(w, fail("thread_not_found"))
			return
		}
		s.nextTS++
		m := slack.Message{Type: "message", TS: fmt.Sprintf("%d.000100", s.nextTS), ThreadTS: f.Get("thread_ts"), User: s.Auth.UserID, Text: f.Get("text")}
		if f.Get("reply_broadcast") == "true" {
			m.Subtype = "thread_broadcast"
		}
		s.Threads[key] = append(s.Threads[key], m)
		reply(w, ok(map[string]any{"channel": f.Get("channel"), "ts": m.TS, "message": m}))
		ev := ok(m)
		delete(ev, "ok")
		ev["channel"] = f.Get("channel")
		go s.Push(ev)
	default:
		reply(w, fail("unknown_method"))
	}
}

// page returns the [start, end) range for cursor, and the next cursor.
func (s *Server) page(n int, cursor string) ([2]int, string) {
	size := s.PageSize
	if size <= 0 {
		size = 200
	}
	start, _ := strconv.Atoi(cursor)
	start = min(start, n)
	end := min(start+size, n)
	next := ""
	if end < n {
		next = strconv.Itoa(end)
	}
	return [2]int{start, end}, next
}

func ok(v any) map[string]any {
	out := map[string]any{"ok": true}
	data, _ := json.Marshal(v)
	_ = json.Unmarshal(data, &out)
	out["ok"] = true
	return out
}

func withCursor(v map[string]any, next string) map[string]any {
	out := ok(v)
	out["response_metadata"] = map[string]string{"next_cursor": next}
	return out
}

func fail(code string) map[string]any { return map[string]any{"ok": false, "error": code} }

func reply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
