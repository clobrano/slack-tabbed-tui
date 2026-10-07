// Package daemon owns every connection to Slack and all shared state:
// it reads the watchlist, keeps one live event stream per workspace,
// fetches threads when something happens in them (or on a timer when no
// stream is live), sends notifications, persists the snapshot and
// broadcasts it to clients.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/config"
	"github.com/clobrano/slack-tabbed-tui/internal/creds"
	"github.com/clobrano/slack-tabbed-tui/internal/directory"
	"github.com/clobrano/slack-tabbed-tui/internal/ipc"
	"github.com/clobrano/slack-tabbed-tui/internal/model"
	"github.com/clobrano/slack-tabbed-tui/internal/notify"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/watchlist"
)

// Daemon is the single per-user server.
type Daemon struct {
	Paths    config.Paths
	Config   config.Config
	Store    creds.Store
	Notifier notify.Notifier
	Log      *log.Logger
	// Client builds an API client for a session; nil means slack.New.
	Client func(creds.Credential) *slack.Client
	// Now is the clock, replaceable in tests.
	Now func() time.Time
	// WatchEvery is how often the watchlist file is checked for changes.
	WatchEvery time.Duration
	// IdleExit makes the daemon exit once it has had no client for this
	// long; 0 keeps it running.
	IdleExit time.Duration
	// Debounce groups the fetches a burst of events causes.
	Debounce time.Duration
	// ResyncEvery re-fetches every thread even when streams are live.
	ResyncEvery time.Duration

	mu         sync.Mutex
	snap       model.Snapshot
	watchStat  fileStat
	server     *ipc.Server
	spaces     map[string]*space // by workspace host (as in thread IDs)
	fetchq     chan string       // thread IDs to fetch
	lastClient time.Time
	ctx        context.Context
}

// AutoIdleExit is the IdleExit of daemons started by clients.
const AutoIdleExit = 10 * time.Second

type fileStat struct {
	mod  time.Time
	size int64
	ok   bool
}

// space is a workspace with watched threads.
type space struct {
	host   string
	cred   creds.Credential
	client *slack.Client
	dir    *directory.Directory
	stop   context.CancelFunc
	conn   model.Conn
	err    string
}

func (d *Daemon) now() time.Time {
	if d.Now != nil {
		return d.Now().UTC()
	}
	return time.Now().UTC()
}

// parse turns a link or ID into a canonical thread ID.
func (d *Daemon) parse(s string) (string, error) {
	r, err := slack.ParseRef(s)
	if err != nil {
		return "", err
	}
	r.Workspace = d.Store.Canonical(r.Workspace)
	return r.ID(), nil
}

// Run starts the daemon and blocks until ctx is done. It returns
// ErrRunning if another daemon is already running for this user.
func (d *Daemon) Run(ctx context.Context) error {
	if d.Log == nil {
		d.Log = log.New(os.Stderr, "slack-tabbed-tui: ", log.LstdFlags)
	}
	if d.Notifier == nil {
		d.Notifier = notify.Nop{}
	}
	if d.Client == nil {
		d.Client = func(c creds.Credential) *slack.Client { return slack.New(c.URL, c.Token, c.Cookie) }
	}
	if d.WatchEvery == 0 {
		d.WatchEvery = 2 * time.Second
	}
	if d.Debounce == 0 {
		d.Debounce = 300 * time.Millisecond
	}
	if d.ResyncEvery == 0 {
		d.ResyncEvery = 5 * time.Minute
	}
	if d.Config.Interval == 0 {
		d.Config.Interval = time.Minute
	}
	if err := d.Paths.Ensure(); err != nil {
		return err
	}
	lock, err := AcquireLock(d.Paths.Lock())
	if err != nil {
		return err
	}
	defer lock.Release()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	d.mu.Lock()
	d.ctx = ctx
	d.spaces = map[string]*space{}
	d.fetchq = make(chan string, 1024)
	d.mu.Unlock()
	d.loadState()
	if _, err := d.reloadWatchlist(true); err != nil {
		d.Log.Printf("watchlist: %v", err)
	}
	d.mu.Lock()
	d.persistLocked()
	d.mu.Unlock()

	srv, err := ipc.Listen(d.Paths.Socket(), d)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	d.mu.Lock()
	d.server = srv
	d.mu.Unlock()
	defer os.Remove(d.Paths.Socket())

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ctx) }()
	go d.fetcher(ctx)
	d.Log.Printf("serving on %s", d.Paths.Socket())
	d.enqueueAll("")

	d.loop(ctx)
	cancel()
	d.mu.Lock()
	for _, sp := range d.spaces {
		sp.stop()
		_ = sp.dir.Save()
	}
	d.mu.Unlock()
	if err := <-serveErr; err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (d *Daemon) loop(ctx context.Context) {
	watch := time.NewTicker(d.WatchEvery)
	defer watch.Stop()
	poll := time.NewTicker(d.Config.Interval)
	defer poll.Stop()
	resync := time.NewTicker(d.ResyncEvery)
	defer resync.Stop()
	d.lastClient = time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-watch.C:
			if d.idle(time.Now()) {
				d.Log.Printf("no clients for %s, exiting", d.IdleExit)
				return
			}
			if _, err := d.reloadWatchlist(false); err != nil {
				d.Log.Printf("watchlist: %v", err)
			}
		case <-poll.C:
			// Threads without a live stream are polled.
			d.mu.Lock()
			var ids []string
			for _, t := range d.snap.Threads {
				if sp := d.spaces[t.Workspace]; sp == nil || sp.conn != model.ConnLive {
					ids = append(ids, t.ID)
				}
			}
			d.mu.Unlock()
			for _, id := range ids {
				d.enqueue(id)
			}
		case <-resync.C:
			d.enqueueAll("")
		}
	}
}

// idle reports whether the daemon should exit for lack of clients.
func (d *Daemon) idle(now time.Time) bool {
	if d.IdleExit <= 0 {
		return false
	}
	d.mu.Lock()
	srv := d.server
	d.mu.Unlock()
	if srv != nil && srv.Clients() > 0 {
		d.lastClient = now
		return false
	}
	return now.Sub(d.lastClient) >= d.IdleExit
}

func (d *Daemon) enqueue(id string) {
	select {
	case d.fetchq <- id:
	default: // full: a resync will catch up
	}
}

// enqueueAll queues every thread of a workspace ("" for all).
func (d *Daemon) enqueueAll(host string) {
	d.mu.Lock()
	var ids []string
	for _, t := range d.snap.Threads {
		if host == "" || t.Workspace == host {
			ids = append(ids, t.ID)
		}
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.enqueue(id)
	}
}

// fetcher fetches queued threads, each once per burst of requests.
func (d *Daemon) fetcher(ctx context.Context) {
	for {
		var first string
		select {
		case <-ctx.Done():
			return
		case first = <-d.fetchq:
		}
		pending := []string{first}
		timer := time.NewTimer(d.Debounce)
	collect:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case id := <-d.fetchq:
				if !slices.Contains(pending, id) {
					pending = append(pending, id)
				}
			case <-timer.C:
				break collect
			}
		}
		for _, id := range pending {
			d.fetch(ctx, id)
		}
		d.mu.Lock()
		spaces := make([]*space, 0, len(d.spaces))
		for _, sp := range d.spaces {
			spaces = append(spaces, sp)
		}
		d.mu.Unlock()
		for _, sp := range spaces {
			if err := sp.dir.Save(); err != nil {
				d.Log.Printf("directory: %v", err)
			}
		}
	}
}

// fetch gets one thread from Slack and applies it.
func (d *Daemon) fetch(ctx context.Context, id string) {
	d.mu.Lock()
	t := d.snap.Find(id)
	if t == nil {
		d.mu.Unlock()
		return
	}
	th := *t
	sp := d.spaces[th.Workspace]
	d.mu.Unlock()
	if sp == nil || sp.client == nil {
		d.setThreadError(id, d.noSessionMsg(th.Workspace))
		return
	}
	msgs, err := sp.client.Replies(ctx, th.Channel, th.ThreadTS, "")
	if ctx.Err() != nil {
		return
	}
	if err != nil {
		var se *slack.Error
		if errors.As(err, &se) && se.Auth() {
			d.setConn(sp, model.ConnLoggedOut, "logged out: run `slack-tabbed-tui auth "+sp.cred.URL+"`")
		}
		d.setThreadError(id, err.Error())
		return
	}
	if err := sp.dir.Resolve(ctx, sp.client, directory.Mentioned(msgs), []string{th.Channel}); err != nil {
		d.Log.Printf("names for %s: %v", id, err)
	}
	cur := convert(th, msgs, sp)
	d.mu.Lock()
	notes := d.applyLocked(cur)
	d.publishLocked()
	d.mu.Unlock()
	for _, n := range notes {
		if err := d.Notifier.Notify(ctx, n); err != nil {
			d.Log.Printf("notify: %v", err)
		}
	}
}

func (d *Daemon) noSessionMsg(host string) string {
	return "not signed in to " + host + ": run `slack-tabbed-tui auth https://" + host + "`"
}

func (d *Daemon) setThreadError(id, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t := d.snap.Find(id); t != nil && t.Error != msg {
		t.Error = msg
		d.publishLocked()
	}
}

// convert builds the display form of a thread from Slack messages.
func convert(prev model.Thread, msgs []slack.Message, sp *space) model.Thread {
	cur := prev
	cur.Error = ""
	cur.ChannelName = sp.dir.ChannelName(prev.Channel)
	if c := sp.dir.Channel(prev.Channel); c != nil {
		cur.IsDM = c.IsIM
	}
	me := sp.cred.UserID
	cur.Messages = make([]model.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.ThreadTS != "" && m.ThreadTS != prev.ThreadTS && m.TS != prev.ThreadTS {
			continue // a message of another thread (should not happen)
		}
		out := model.Message{
			TS:         m.TS,
			AuthorID:   m.User,
			Author:     sp.dir.Author(m),
			Bot:        m.IsBot(),
			Mine:       me != "" && m.User == me,
			Text:       slack.PlainText(m.Text, sp.dir),
			Edited:     m.Edited != nil,
			Broadcast:  m.Subtype == "thread_broadcast",
			MentionsMe: mentions(m.Text, me),
		}
		for _, r := range m.Reactions {
			re := model.Reaction{Name: r.Name, Count: r.Count}
			for _, u := range r.Users {
				re.Mine = re.Mine || u == me
				name := sp.dir.UserName(u)
				if name == "" {
					name = u
				}
				re.Users = append(re.Users, name)
			}
			out.Reactions = append(out.Reactions, re)
		}
		for _, f := range m.Files {
			name := f.Name
			if name == "" {
				name = f.Title
			}
			out.Files = append(out.Files, model.File{Name: name, Size: f.Size, URL: f.Permalink})
		}
		for _, a := range m.Attachments {
			for _, s := range []string{a.Pretext, a.Title, a.Text} {
				if s == "" {
					s = a.Fallback
				}
				if s != "" {
					out.Attachments = append(out.Attachments, slack.PlainText(s, sp.dir))
					break
				}
			}
		}
		cur.Messages = append(cur.Messages, out)
	}
	return cur
}

// mentions reports whether Slack markup mentions user me directly or
// through @here, @channel or @everyone.
func mentions(text, me string) bool {
	if me != "" && (strings.Contains(text, "<@"+me+">") || strings.Contains(text, "<@"+me+"|")) {
		return true
	}
	return strings.Contains(text, "<!here") || strings.Contains(text, "<!channel") || strings.Contains(text, "<!everyone")
}

// applyLocked stores a freshly fetched thread and returns the
// notifications its changes cause.
func (d *Daemon) applyLocked(cur model.Thread) []notify.Notification {
	t := d.snap.Find(cur.ID)
	if t == nil {
		return nil // unwatched while fetching
	}
	prev := *t
	cur.Alerts, cur.ReadTS = prev.Alerts, prev.ReadTS
	firstFetch := prev.FetchedAt.IsZero()
	if firstFetch && cur.ReadTS == "" {
		cur.ReadTS = cur.Latest() // a new thread starts read
	}
	cur.FetchedAt = d.now()
	*t = cur
	if firstFetch || !cur.Alerts || d.snap.Settings.Mute {
		return nil
	}
	return d.events(prev, cur)
}

func (d *Daemon) events(prev, cur model.Thread) []notify.Notification {
	on := d.snap.Settings.Events
	old := map[string]model.Message{}
	for _, m := range prev.Messages {
		old[m.TS] = m
	}
	var notes []notify.Notification
	for _, m := range cur.Messages {
		o, seen := old[m.TS]
		if !seen && !m.Mine && model.After(m.TS, prev.Latest()) {
			ev := model.Event{Type: model.EventReply, ThreadID: cur.ID, TS: m.TS, Author: m.Author, URL: cur.MessageLink(m.TS)}
			if m.MentionsMe && on[model.EventMention] {
				ev.Type = model.EventMention
			} else if !on[model.EventReply] {
				continue
			}
			notes = append(notes, notify.Format(ev, cur, m.Text))
			continue
		}
		if !seen || !m.Mine || !on[model.EventReaction] {
			continue
		}
		before := map[string]int{}
		for _, r := range o.Reactions {
			before[r.Name] = others(r)
		}
		for _, r := range m.Reactions {
			if others(r) > before[r.Name] {
				who := ""
				if len(r.Users) > 0 {
					who = r.Users[len(r.Users)-1]
				}
				ev := model.Event{Type: model.EventReaction, ThreadID: cur.ID, TS: m.TS, Author: who, URL: cur.MessageLink(m.TS)}
				notes = append(notes, notify.Format(ev, cur, r.Name))
			}
		}
	}
	return notes
}

// others counts the people other than the user behind a reaction.
func others(r model.Reaction) int {
	if r.Mine {
		return r.Count - 1
	}
	return r.Count
}

// Snapshot implements ipc.Handler.
func (d *Daemon) Snapshot() *model.Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.copyLocked()
}

func (d *Daemon) copyLocked() *model.Snapshot {
	s := d.snap
	s.Threads = slices.Clone(d.snap.Threads)
	s.Workspaces = nil
	hosts := make([]string, 0, len(d.spaces))
	for h := range d.spaces {
		hosts = append(hosts, h)
	}
	slices.Sort(hosts)
	for _, h := range hosts {
		sp := d.spaces[h]
		s.Workspaces = append(s.Workspaces, model.Workspace{Host: h, Team: sp.cred.Team, UserID: sp.cred.UserID, Conn: sp.conn, Error: sp.err})
	}
	s.Settings.Events = map[model.EventType]bool{}
	for k, v := range d.snap.Settings.Events {
		s.Settings.Events[k] = v
	}
	return &s
}

// publishLocked stamps, persists and broadcasts the state.
func (d *Daemon) publishLocked() {
	d.snap.GeneratedAt = d.now()
	d.persistLocked()
	if d.server != nil {
		d.server.Broadcast(d.copyLocked())
	}
}

func (d *Daemon) persistLocked() {
	d.snap.Schema = model.SchemaVersion
	if err := WriteSnapshot(d.Paths.Snapshot(), d.copyLocked()); err != nil {
		d.Log.Printf("write snapshot: %v", err)
	}
}

// WriteSnapshot writes snap atomically with mode 0600.
func WriteSnapshot(path string, snap *model.Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".snapshot-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// ReadSnapshot reads a snapshot file.
func ReadSnapshot(path string) (*model.Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s model.Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &s, nil
}

func (d *Daemon) loadState() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.snap = model.Snapshot{Schema: model.SchemaVersion, Settings: model.DefaultSettings()}
	s, err := ReadSnapshot(d.Paths.Snapshot())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			d.Log.Printf("ignoring saved state: %v", err)
		}
		return
	}
	if s.Schema > model.SchemaVersion {
		d.Log.Printf("ignoring saved state from a newer version (schema %d)", s.Schema)
		return
	}
	d.snap.Threads = s.Threads
	d.snap.Settings.Mute = s.Settings.Mute
	for k, v := range s.Settings.Events {
		d.snap.Settings.Events[k] = v
	}
}

// reloadWatchlist re-reads the watchlist when it changed (or always when
// force is set) and reshapes the thread list to match it.
func (d *Daemon) reloadWatchlist(force bool) (bool, error) {
	path := d.Paths.Watchlist()
	st := fileStat{}
	if fi, err := os.Stat(path); err == nil {
		st = fileStat{mod: fi.ModTime(), size: fi.Size(), ok: true}
	}
	d.mu.Lock()
	if !force && st == d.watchStat {
		d.mu.Unlock()
		return false, nil
	}
	d.watchStat = st
	ids, bad, err := watchlist.Load(path, d.parse)
	if err != nil {
		d.mu.Unlock()
		return false, err
	}
	for _, e := range bad {
		d.Log.Print(e)
	}
	added := d.setIDsLocked(ids)
	d.publishLocked()
	d.mu.Unlock()
	for _, id := range added {
		d.enqueue(id)
	}
	return true, nil
}

// setIDsLocked makes the thread list follow ids (watchlist order) and
// starts or stops workspace connections to match. It returns the IDs
// of threads new to the list.
func (d *Daemon) setIDsLocked(ids []string) []string {
	old := map[string]model.Thread{}
	for _, t := range d.snap.Threads {
		old[t.ID] = t
	}
	var added []string
	threads := make([]model.Thread, 0, len(ids))
	for _, id := range ids {
		if t, ok := old[id]; ok {
			threads = append(threads, t)
			continue
		}
		r, err := slack.ParseRef(id)
		if err != nil {
			continue
		}
		threads = append(threads, model.Thread{ID: id, Workspace: r.Workspace, Channel: r.Channel, ThreadTS: r.ThreadTS, Permalink: r.Permalink()})
		added = append(added, id)
	}
	d.snap.Threads = threads
	d.syncSpacesLocked()
	return added
}

// syncSpacesLocked starts a connection for each workspace with threads
// and stops the others.
func (d *Daemon) syncSpacesLocked() {
	if d.spaces == nil || d.ctx == nil {
		return
	}
	want := map[string]bool{}
	for _, t := range d.snap.Threads {
		want[t.Workspace] = true
	}
	for host, sp := range d.spaces {
		if !want[host] {
			sp.stop()
			delete(d.spaces, host)
		}
	}
	for host := range want {
		if _, ok := d.spaces[host]; !ok {
			d.spaces[host] = d.openSpace(host)
		}
	}
}

// openSpace looks up the session for a workspace and starts its stream.
func (d *Daemon) openSpace(host string) *space {
	sp := &space{host: host, stop: func() {}, conn: model.ConnNoSession}
	cred, err := d.Store.Lookup(host)
	if err != nil {
		sp.err = d.noSessionMsg(host)
		sp.dir = directory.Load("")
		return sp
	}
	team := cred.TeamID
	if team == "" {
		team = host
	}
	sp.cred, sp.client, sp.dir = cred, d.Client(cred), directory.Load(d.Paths.Directory(team))
	sp.conn = model.ConnConnecting
	ctx, cancel := context.WithCancel(d.ctx)
	sp.stop = cancel
	go d.stream(ctx, sp)
	return sp
}

func (d *Daemon) setConn(sp *space, c model.Conn, msg string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.spaces[sp.host] != sp || sp.conn == c && sp.err == msg {
		return
	}
	sp.conn, sp.err = c, msg
	d.publishLocked()
}

// stream keeps the workspace's event stream and routes its events.
func (d *Daemon) stream(ctx context.Context, sp *space) {
	if sp.cred.UserID == "" {
		// A session from the environment: ask who we are.
		if info, err := sp.client.AuthTest(ctx); err == nil {
			d.mu.Lock()
			sp.cred.UserID, sp.cred.Team = info.UserID, info.Team
			d.mu.Unlock()
		}
	}
	st := &slack.Stream{
		Client: sp.client,
		OnState: func(s slack.StreamState, err error) {
			msg := ""
			if err != nil && s != slack.StreamConnecting {
				msg = err.Error()
			}
			switch s {
			case slack.StreamLive:
				d.setConn(sp, model.ConnLive, "")
			case slack.StreamDown:
				d.setConn(sp, model.ConnDown, msg)
			case slack.StreamLoggedOut:
				d.setConn(sp, model.ConnLoggedOut, "logged out: run `slack-tabbed-tui auth "+sp.cred.URL+"`")
			}
		},
	}
	err := st.Run(ctx, func(ev slack.Event) { d.route(sp, ev) })
	if err != nil && ctx.Err() == nil {
		d.Log.Printf("%s: event stream ended: %v", sp.host, err)
	}
}

// route queues a fetch of the watched threads an event concerns.
func (d *Daemon) route(sp *space, ev slack.Event) {
	switch ev.Type {
	case slack.EventConnected:
		d.enqueueAll(sp.host)
		return
	case "user_change", "team_join":
		var u struct {
			User slack.User `json:"user"`
		}
		if json.Unmarshal(ev.Raw, &u) == nil && u.User.ID != "" {
			sp.dir.PutUser(u.User)
		}
		return
	}
	channel, tss := ev.Channel, []string{ev.TS, ev.ThreadTS, ev.DeletedTS}
	if ev.Message != nil {
		tss = append(tss, ev.Message.TS, ev.Message.ThreadTS)
	}
	if ev.Item != nil {
		channel = ev.Item.Channel
		tss = append(tss, ev.Item.TS)
	}
	var prevMsg struct {
		Previous *slack.Message `json:"previous_message"`
	}
	if json.Unmarshal(ev.Raw, &prevMsg) == nil && prevMsg.Previous != nil {
		tss = append(tss, prevMsg.Previous.TS, prevMsg.Previous.ThreadTS)
	}
	if channel == "" {
		return
	}
	d.mu.Lock()
	var ids []string
	for _, t := range d.snap.Threads {
		if t.Workspace != sp.host || t.Channel != channel {
			continue
		}
		for _, ts := range tss {
			if ts == "" {
				continue
			}
			if ts == t.ThreadTS || slices.ContainsFunc(t.Messages, func(m model.Message) bool { return m.TS == ts }) {
				ids = append(ids, t.ID)
				break
			}
		}
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.enqueue(id)
	}
}

// Handle implements ipc.Handler.
func (d *Daemon) Handle(ctx context.Context, cmd ipc.Command) (string, error) {
	switch cmd.Op {
	case ipc.OpAdd:
		return d.add(ctx, cmd.ID)
	case ipc.OpUnwatch:
		return d.unwatch(cmd.ID)
	case ipc.OpSync:
		d.enqueueAll("")
		return "syncing", nil
	case ipc.OpAlerts:
		if cmd.On == nil {
			return "", errors.New("alerts: missing on")
		}
		return d.withThread(cmd.ID, func(t *model.Thread) string {
			t.Alerts = *cmd.On
			return "notifications " + onOff(*cmd.On)
		})
	case ipc.OpRead:
		return d.withThread(cmd.ID, func(t *model.Thread) string {
			ts := cmd.TS
			if ts == "" {
				ts = t.Latest()
			}
			if model.After(ts, t.ReadTS) {
				t.ReadTS = ts
			}
			return ""
		})
	case ipc.OpEvents:
		for k := range cmd.Events {
			if !slices.Contains(model.EventTypes, k) {
				return "", fmt.Errorf("unknown event type %q", k)
			}
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		for k, v := range cmd.Events {
			d.snap.Settings.Events[k] = v
		}
		d.publishLocked()
		return "notification events updated", nil
	case ipc.OpMute:
		if cmd.On == nil {
			return "", errors.New("mute: missing on")
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		d.snap.Settings.Mute = *cmd.On
		d.publishLocked()
		return "mute " + onOff(*cmd.On), nil
	case ipc.OpReply:
		return d.reply(ctx, cmd)
	}
	return "", fmt.Errorf("unknown command %q", cmd.Op)
}

func (d *Daemon) withThread(id string, fn func(*model.Thread) string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	t := d.snap.Find(id)
	if t == nil {
		return "", fmt.Errorf("not watching %s", id)
	}
	info := fn(t)
	d.publishLocked()
	return info, nil
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func (d *Daemon) add(ctx context.Context, input string) (string, error) {
	id, err := d.parse(input)
	if err != nil {
		return "", err
	}
	r, _ := slack.ParseRef(id)
	cred, err := d.Store.Lookup(r.Workspace)
	if err != nil {
		return "", err
	}
	// Check the thread exists and is readable before watching it.
	if _, err := d.Client(cred).Replies(ctx, r.Channel, r.ThreadTS, ""); err != nil {
		return "", fmt.Errorf("%s: %w", id, err)
	}
	d.mu.Lock()
	changed, err := watchlist.Add(d.Paths.Watchlist(), id, d.parse)
	if err != nil {
		d.mu.Unlock()
		return "", err
	}
	if !changed {
		d.mu.Unlock()
		return "already watching " + id, nil
	}
	added, err := d.syncWatchlistLocked()
	if err == nil {
		d.publishLocked()
	}
	d.mu.Unlock()
	if err != nil {
		return "", err
	}
	for _, a := range added {
		d.enqueue(a)
	}
	return "watching " + id, nil
}

func (d *Daemon) unwatch(input string) (string, error) {
	id, err := d.parse(input)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	changed, err := watchlist.Remove(d.Paths.Watchlist(), id, d.parse)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", fmt.Errorf("not watching %s", id)
	}
	if _, err := d.syncWatchlistLocked(); err != nil {
		return "", err
	}
	d.publishLocked()
	return "unwatched " + id, nil
}

// syncWatchlistLocked reloads the file the daemon itself just wrote.
func (d *Daemon) syncWatchlistLocked() ([]string, error) {
	path := d.Paths.Watchlist()
	ids, _, err := watchlist.Load(path, d.parse)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil {
		d.watchStat = fileStat{mod: fi.ModTime(), size: fi.Size(), ok: true}
	}
	return d.setIDsLocked(ids), nil
}

// reply posts text (Slack markup) in a thread and marks the thread read
// up to the new message.
func (d *Daemon) reply(ctx context.Context, cmd ipc.Command) (string, error) {
	d.mu.Lock()
	t := d.snap.Find(cmd.ID)
	var th model.Thread
	if t != nil {
		th = *t
	}
	sp := d.spaces[th.Workspace]
	d.mu.Unlock()
	if t == nil {
		return "", fmt.Errorf("not watching %s", cmd.ID)
	}
	if sp == nil || sp.client == nil {
		return "", errors.New(d.noSessionMsg(th.Workspace))
	}
	broadcast := cmd.On != nil && *cmd.On
	m, err := sp.client.PostReply(ctx, th.Channel, th.ThreadTS, cmd.Text, broadcast)
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	if t := d.snap.Find(cmd.ID); t != nil && model.After(m.TS, t.ReadTS) {
		t.ReadTS = m.TS
	}
	d.mu.Unlock()
	d.enqueue(cmd.ID)
	return "sent", nil
}
