// Package tui is the tabbed terminal UI: one tab per watched thread,
// showing its messages. All shared state lives in the daemon; the TUI
// keeps only per-client UI state (active tab, selection, scroll, drafts).
package tui

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/clobrano/slack-tabbed-tui/internal/ipc"
	"github.com/clobrano/slack-tabbed-tui/internal/model"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
)

// Backend performs the side effects the TUI asks for.
type Backend interface {
	// Send delivers a command to the daemon; the outcome comes back
	// through Model.Result.
	Send(cmd ipc.Command)
	// Open opens a URL in the browser (or the Slack app).
	Open(url string) error
	// Copy puts text on the clipboard.
	Copy(text string) error
}

type mode int

const (
	modeNormal mode = iota
	modeAdd
	modeFind
	modeEvents
	modeHelp
	modeConfirm
	modeCompose
)

// readAfter is how long a thread must be on screen to count as read.
const readAfter = 1500 * time.Millisecond

// Model is the TUI state. It is not safe for concurrent use: the event
// loop owns it.
type Model struct {
	backend Backend
	now     func() time.Time
	// Color enables ANSI styles.
	Color bool

	snap      *model.Snapshot
	connected bool

	active      string            // ID of the active tab
	activeIdx   int               // its index, to stay in place if it disappears
	activeSince time.Time         // when it became active
	sel         map[string]string // selected message ts per thread
	top         map[string]int    // first visible row per thread
	readSent    map[string]string // last ts sent as read, per thread
	pageRows    int               // rows of the message list, from the last View
	listWidth   int               // its width

	mode       mode
	input      []rune
	findSel    int
	evSel      int
	confirmMsg string
	confirmCmd ipc.Command
	pendingG   bool

	drafts    map[string][]rune // composer text per thread
	broadcast map[string]bool   // "also send to channel" per thread

	flash      string
	flashErr   bool
	flashUntil time.Time

	quit bool
}

// NewModel returns an empty model.
func NewModel(b Backend) *Model {
	return &Model{
		backend: b, now: time.Now, Color: true,
		sel: map[string]string{}, top: map[string]int{}, readSent: map[string]string{},
		drafts: map[string][]rune{}, broadcast: map[string]bool{},
		pageRows: 10, listWidth: 78,
	}
}

// Quit reports whether the user asked to leave.
func (m *Model) Quit() bool { return m.quit }

func (m *Model) threads() []model.Thread {
	if m.snap == nil {
		return nil
	}
	return m.snap.Threads
}

// SetSnapshot replaces the displayed state.
func (m *Model) SetSnapshot(s *model.Snapshot) {
	m.snap = s
	ths := m.threads()
	if len(ths) == 0 {
		m.active, m.activeIdx = "", 0
		return
	}
	for i, t := range ths {
		if t.ID == m.active {
			m.activeIdx = i
			m.markRead()
			return
		}
	}
	// The active thread is gone (or none yet): stay at the same position.
	m.setActive(m.activeIdx)
}

// SetConnected records the daemon connection state.
func (m *Model) SetConnected(ok bool) { m.connected = ok }

// Tick lets time-based behavior happen: marking the active thread read.
func (m *Model) Tick() { m.markRead() }

// Result shows the outcome of a command.
func (m *Model) Result(info string, err error) {
	if err != nil {
		m.setFlash(err.Error(), true)
	} else if info != "" {
		m.setFlash(info, false)
	}
}

func (m *Model) setFlash(s string, isErr bool) {
	m.flash, m.flashErr, m.flashUntil = s, isErr, m.now().Add(4*time.Second)
}

func (m *Model) current() *model.Thread {
	ths := m.threads()
	if len(ths) == 0 {
		return nil
	}
	return &ths[min(m.activeIdx, len(ths)-1)]
}

func (m *Model) setActive(i int) {
	ths := m.threads()
	if len(ths) == 0 {
		return
	}
	i = min(max(i, 0), len(ths)-1)
	if ths[i].ID != m.active {
		m.activeSince = m.now()
	}
	m.activeIdx, m.active = i, ths[i].ID
}

// markRead tells the daemon the active thread has been seen, once it has
// been on screen for a moment.
func (m *Model) markRead() {
	t := m.current()
	if t == nil || !m.connected || m.mode == modeHelp || m.mode == modeEvents {
		return
	}
	if m.now().Sub(m.activeSince) < readAfter {
		return
	}
	latest := t.Latest()
	if u, _ := t.Unread(); u == 0 || m.readSent[t.ID] == latest {
		return
	}
	m.readSent[t.ID] = latest
	m.backend.Send(ipc.Command{Op: ipc.OpRead, ID: t.ID, TS: latest})
}

// selIndex is the index of the selected message of t. A thread seen for
// the first time selects its first unread message, else its last one.
func (m *Model) selIndex(t *model.Thread) int {
	if len(t.Messages) == 0 {
		return 0
	}
	if ts, ok := m.sel[t.ID]; ok {
		for i, msg := range t.Messages {
			if msg.TS == ts {
				return i
			}
		}
		// Deleted: the next newer one.
		for i, msg := range t.Messages {
			if model.After(msg.TS, ts) {
				return i
			}
		}
		return len(t.Messages) - 1
	}
	for i, msg := range t.Messages {
		if !msg.Mine && model.After(msg.TS, t.ReadTS) {
			return i
		}
	}
	return len(t.Messages) - 1
}

func (m *Model) selected() (*model.Thread, *model.Message) {
	t := m.current()
	if t == nil || len(t.Messages) == 0 {
		return t, nil
	}
	return t, &t.Messages[m.selIndex(t)]
}

func (m *Model) moveMsg(delta int) {
	t := m.current()
	if t == nil || len(t.Messages) == 0 {
		return
	}
	i := min(max(m.selIndex(t)+delta, 0), len(t.Messages)-1)
	m.sel[t.ID] = t.Messages[i].TS
}

func (m *Model) send(cmd ipc.Command) bool {
	if !m.connected {
		m.setFlash("disconnected from the daemon; try again when connected", true)
		return false
	}
	m.backend.Send(cmd)
	return true
}

// Key handles one key press.
func (m *Model) Key(k string) {
	switch m.mode {
	case modeAdd:
		m.keyInput(k, func(s string) {
			if s = strings.TrimSpace(s); s != "" && m.send(ipc.Command{Op: ipc.OpAdd, ID: s}) {
				m.setFlash("adding "+s+"…", false)
			}
		})
	case modeFind:
		m.keyFind(k)
	case modeEvents:
		m.keyEvents(k)
	case modeHelp:
		m.mode = modeNormal
	case modeConfirm:
		m.mode = modeNormal
		if k == "y" || k == "Y" {
			m.send(m.confirmCmd)
		}
	case modeCompose:
		m.keyCompose(k)
	default:
		m.keyNormal(k)
	}
}

func (m *Model) keyNormal(k string) {
	if m.pendingG {
		m.pendingG = false
		switch k {
		case "t":
			m.setActive((m.activeIdx + 1) % max(len(m.threads()), 1))
		case "T":
			m.setActive((m.activeIdx - 1 + len(m.threads())) % max(len(m.threads()), 1))
		case "g":
			m.moveMsg(-1 << 20)
		}
		return
	}
	switch k {
	case "q", kCtrlC:
		m.quit = true
	case "h", kLeft, kShiftTab:
		m.setActive(m.activeIdx - 1)
	case "l", kRight, kTab:
		m.setActive(m.activeIdx + 1)
	case "g":
		m.pendingG = true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		m.setActive(int(k[0] - '1'))
	case "j", kDown:
		m.moveMsg(1)
	case "k", kUp:
		m.moveMsg(-1)
	case kCtrlD, kPgDown:
		m.scroll(m.pageRows / 2)
	case kCtrlU, kPgUp:
		m.scroll(-m.pageRows / 2)
	case "G", kEnd:
		m.moveMsg(1 << 20)
	case kHome:
		m.moveMsg(-1 << 20)
	case kEnter:
		if t, msg := m.selected(); msg != nil {
			m.open(t.MessageLink(msg.TS))
		}
	case "o":
		if t := m.current(); t != nil {
			m.open(t.Permalink)
		}
	case "O":
		if _, msg := m.selected(); msg != nil {
			if u := firstLink(*msg); u != "" {
				m.open(u)
			} else {
				m.setFlash("no link or file in this message", true)
			}
		}
	case "y":
		if t := m.current(); t != nil {
			m.copy(t.Permalink)
		}
	case "Y":
		if t, msg := m.selected(); msg != nil {
			m.copy(t.MessageLink(msg.TS))
		}
	case "i":
		if t := m.current(); t != nil {
			m.mode, m.input = modeCompose, m.drafts[t.ID]
		}
	case "u":
		if t := m.current(); t != nil {
			m.readSent[t.ID] = t.Latest()
			m.send(ipc.Command{Op: ipc.OpRead, ID: t.ID, TS: t.Latest()})
		}
	case "n":
		if t := m.current(); t != nil {
			on := !t.Alerts
			m.send(ipc.Command{Op: ipc.OpAlerts, ID: t.ID, On: &on})
		}
	case "N":
		m.mode, m.evSel = modeEvents, 0
	case "a":
		m.mode, m.input = modeAdd, nil
	case "d":
		if t := m.current(); t != nil {
			m.confirm("Unwatch "+tabName(*t)+" in every client?", ipc.Command{Op: ipc.OpUnwatch, ID: t.ID})
		}
	case "r":
		m.send(ipc.Command{Op: ipc.OpSync})
	case "/":
		m.mode, m.input, m.findSel = modeFind, nil, 0
	case "?":
		m.mode = modeHelp
	}
}

// scroll moves the selection by about rows screen rows.
func (m *Model) scroll(rows int) {
	if t := m.current(); t != nil && len(t.Messages) > 0 {
		m.scrollRows(t, rows, m.listWidth)
	}
}

// firstLink is the first file or URL of a message, for O.
func firstLink(msg model.Message) string {
	for _, f := range msg.Files {
		if f.URL != "" {
			return f.URL
		}
	}
	for _, field := range strings.Fields(msg.Text) {
		field = strings.Trim(field, "()<>,.;")
		if strings.HasPrefix(field, "https://") || strings.HasPrefix(field, "http://") {
			return field
		}
	}
	return ""
}

func (m *Model) confirm(msg string, cmd ipc.Command) {
	m.mode, m.confirmMsg, m.confirmCmd = modeConfirm, msg, cmd
}

func (m *Model) copy(url string) {
	if url == "" {
		m.setFlash("no link to copy", true)
		return
	}
	if err := m.backend.Copy(url); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash("copied "+url, false)
}

func (m *Model) open(url string) {
	if url == "" {
		m.setFlash("no link", true)
		return
	}
	if err := m.backend.Open(url); err != nil {
		m.setFlash(err.Error(), true)
		return
	}
	m.setFlash("opening "+url, false)
}

// keyCompose edits the reply being written.
func (m *Model) keyCompose(k string) {
	t := m.current()
	if t == nil {
		m.mode = modeNormal
		return
	}
	switch k {
	case kEsc:
		m.drafts[t.ID] = m.input
		m.mode = modeNormal
	case kCtrlC:
		delete(m.drafts, t.ID)
		m.input = nil
		m.mode = modeNormal
	case kEnter:
		text := strings.TrimSpace(string(m.input))
		if text == "" {
			return
		}
		on := m.broadcast[t.ID]
		if m.send(ipc.Command{Op: ipc.OpReply, ID: t.ID, Text: slack.Escape(text), On: &on}) {
			delete(m.drafts, t.ID)
			m.input = nil
			m.mode = modeNormal
			m.sel[t.ID] = "9999999999.999999" // follow the new message
			m.setFlash("sending…", false)
		}
	case kAltEnter, kCtrlJ:
		m.input = append(m.input, '\n')
	case kCtrlB:
		m.broadcast[t.ID] = !m.broadcast[t.ID]
	case kBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case kCtrlU:
		// Delete the current line.
		s := string(m.input)
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			m.input = []rune(s[:i+1])
		} else {
			m.input = nil
		}
	case kCtrlW:
		m.input = deleteWord(m.input)
	case kTab:
		m.input = append(m.input, ' ', ' ')
	default:
		if utf8.RuneCountInString(k) == 1 {
			m.input = append(m.input, []rune(k)...)
		}
	}
}

func deleteWord(in []rune) []rune {
	s := strings.TrimRight(string(in), " ")
	if i := strings.LastIndexAny(s, " \n"); i >= 0 {
		return []rune(s[:i+1])
	}
	return nil
}

// keyInput edits the one-line input; submit is called on enter.
func (m *Model) keyInput(k string, submit func(string)) {
	switch k {
	case kEsc, kCtrlC:
		m.mode = modeNormal
	case kEnter:
		m.mode = modeNormal
		submit(string(m.input))
	case kBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case kCtrlU:
		m.input = nil
	case kCtrlW:
		m.input = deleteWord(m.input)
	default:
		if utf8.RuneCountInString(k) == 1 {
			m.input = append(m.input, []rune(k)...)
		}
	}
}

func (m *Model) keyFind(k string) {
	switch k {
	case kDown, kCtrlN, kTab:
		m.findSel++
		return
	case kUp, kCtrlP, kShiftTab:
		m.findSel = max(m.findSel-1, 0)
		return
	}
	before := string(m.input)
	m.keyInput(k, func(string) {
		t := m.current()
		matches := m.findMatches()
		if t == nil || len(matches) == 0 {
			return
		}
		m.sel[t.ID] = t.Messages[matches[min(m.findSel, len(matches)-1)]].TS
	})
	if string(m.input) != before {
		m.findSel = 0
	}
}

// findMatches returns the indexes of the current thread's messages whose
// author or text contains the query, ignoring case, newest first.
func (m *Model) findMatches() []int {
	t := m.current()
	if t == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(string(m.input)))
	var out []int
	for i, msg := range t.Messages {
		if q == "" || strings.Contains(strings.ToLower(msg.Author+" "+msg.Text), q) {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a] > out[b] })
	return out
}

func (m *Model) keyEvents(k string) {
	rows := len(model.EventTypes) + 1
	switch k {
	case "j", kDown, kTab:
		m.evSel = (m.evSel + 1) % rows
	case "k", kUp, kShiftTab:
		m.evSel = (m.evSel - 1 + rows) % rows
	case " ", "x", kEnter:
		if m.snap == nil {
			return
		}
		if m.evSel == len(model.EventTypes) {
			on := !m.snap.Settings.Mute
			m.send(ipc.Command{Op: ipc.OpMute, On: &on})
			return
		}
		e := model.EventTypes[m.evSel]
		m.send(ipc.Command{Op: ipc.OpEvents, Events: map[model.EventType]bool{e: !m.snap.Settings.Events[e]}})
	case kEsc, "q", "N":
		m.mode = modeNormal
	}
}
