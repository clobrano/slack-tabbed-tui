package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/clobrano/slack-tabbed-tui/internal/model"
)

// appTitle is the app name as shown in the title bar.
const appTitle = "SLACK-TABBED-TUI"

// appTagline follows the name in the title bar.
const appTagline = "Slack thread watcher"

// bellIcon marks alerts: the Nerd Fonts codicon bell (nf-cod-bell), as
// in ghwatch.
const bellIcon = ""

// View renders the screen as width-wide rows, exactly height of them.
func (m *Model) View(width, height int) []string {
	rows := m.lines(width, height)
	out := make([]string, height)
	for i := range out {
		if i < len(rows) {
			out[i] = rows[i].render(width, m.Color)
		}
	}
	return out
}

func (m *Model) lines(w, h int) []line {
	if w < 30 || h < 10 {
		return []line{{{"terminal too small", ""}}}
	}
	switch m.mode {
	case modeHelp:
		return overlay(w, h, "Keybindings", m.helpContent(w, h))
	case modeEvents:
		return overlay(w, h, "Notifications", m.eventsContent())
	}
	out := []line{m.titleBar(w), m.tabBar(w), {{strings.Repeat("─", w), sBorder}}}
	out = append(out, m.header(w)...)
	var composer []line
	if m.mode == modeCompose {
		composer = m.composer(w)
	}
	// The message list sits in a box: border, rows, border.
	rows := h - len(out) - 2 - len(composer) - 1
	var body []line
	if m.mode == modeFind {
		body = m.findBody(w-2, rows)
	} else {
		body = m.messagesBody(w-2, rows)
	}
	content := make([]line, rows)
	copy(content, body)
	out = append(out, box(content, w, sBorder, "", false)...)
	out = append(out, composer...)
	return append(out, m.footer(w))
}

// titleBar: the app name, threads and unread counts, alerts, and the
// state of the workspaces' connections.
func (m *Model) titleBar(w int) line {
	left := line{{" " + appTitle, sBold + sAccent}, {"  " + appTagline, sDim}}
	if m.snap == nil {
		return spread(left, line{{"waiting for the daemon… ", sDim}}, w)
	}
	ths := m.threads()
	left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%d %s", len(ths), plural(len(ths), "thread", "threads")), sMuted})
	unread, mentions, alerts := 0, 0, 0
	for _, t := range ths {
		u, mn := t.Unread()
		unread += u
		mentions += mn
		if t.Alerts {
			alerts++
		}
	}
	if unread > 0 {
		left = append(left, seg{" ", ""}, seg{fmt.Sprintf("●%d", unread), sAccent + sBold})
	}
	if mentions > 0 {
		left = append(left, seg{" ", ""}, seg{fmt.Sprintf("@%d", mentions), sRed + sBold})
	}
	if alerts > 0 {
		left = append(left, seg{" · ", sDim}, seg{fmt.Sprintf("%s %d", bellIcon, alerts), sCyan})
	}
	if m.snap.Settings.Mute {
		left = append(left, seg{" · ", sDim}, seg{"muted", sYellow})
	}
	return spread(left, m.connSummary(), w)
}

// connSummary sums up the workspaces' connections: live, polling, or
// what needs the user.
func (m *Model) connSummary() line {
	if m.snap == nil || len(m.snap.Workspaces) == 0 {
		return line{{" ", ""}}
	}
	var l line
	add := func(text, style string) {
		if len(l) > 0 {
			l = append(l, seg{" · ", sDim})
		}
		l = append(l, seg{text, style})
	}
	for _, ws := range m.snap.Workspaces {
		name := shortHost(ws.Host)
		switch ws.Conn {
		case model.ConnLive:
			add(name+" live", sGreen)
		case model.ConnConnecting:
			add(name+" connecting…", sDim)
		case model.ConnDown:
			add(name+" polling", sYellow)
		case model.ConnLoggedOut:
			add(name+" logged out", sRed+sBold)
		case model.ConnNoSession:
			add(name+" not signed in", sRed+sBold)
		}
	}
	return append(l, seg{" ", ""})
}

// shortHost is "acme" for acme.slack.com.
func shortHost(h string) string {
	name, _, _ := strings.Cut(h, ".")
	return name
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// tabName is a short name for a thread: its channel and the first words
// of its parent message.
func tabName(t model.Thread) string {
	where := "#" + t.ChannelName
	switch {
	case t.IsDM:
		where = "@" + t.ChannelName
	case t.ChannelName == "":
		where = t.Channel
	}
	if s := slug(t.Title(), 2); s != "" {
		return where + " " + s
	}
	return where
}

// slug is the first n words of s, lower case, joined by dashes.
func slug(s string, n int) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	})
	var keep []string
	for _, w := range words {
		if len(keep) == 0 && (w == "hi" || w == "hey" || w == "hello" || w == "here" || w == "channel") {
			continue
		}
		keep = append(keep, w)
		if len(keep) == n {
			break
		}
	}
	return truncate(strings.Join(keep, "-"), 16)
}

func (m *Model) tabLabels() []line {
	ths := m.threads()
	counts := map[string]int{}
	common, best := "", 0
	for _, t := range ths {
		counts[t.Workspace]++
		if counts[t.Workspace] > best {
			common, best = t.Workspace, counts[t.Workspace]
		}
	}
	labels := make([]line, len(ths))
	for i, t := range ths {
		l := line{{"  " + strconv.Itoa(i+1) + ":", sMuted}}
		u, mn := t.Unread()
		switch {
		case t.Error != "":
			l = append(l, seg{"!", sRed + sBold})
		case mn > 0:
			l = append(l, seg{fmt.Sprintf("@%d", u), sRed + sBold})
		case u > 0:
			l = append(l, seg{fmt.Sprintf("●%d", u), sAccent + sBold})
		}
		name := tabName(t)
		if t.Workspace != common {
			name = shortHost(t.Workspace) + " " + name
		}
		l = append(l, seg{" " + name, sMuted})
		if t.Alerts {
			l = append(l, seg{" " + bellIcon, sAccent})
		}
		l = append(l, seg{"  ", ""})
		if i == m.activeIdx {
			for j := range l {
				l[j].style = bgAccent + sBold + sWhite
			}
			if !m.Color {
				l[0].text = " [" + l[0].text[2:]
				l[len(l)-1].text = "] "
			}
		}
		labels[i] = l
	}
	return labels
}

func (m *Model) tabBar(w int) line {
	labels := m.tabLabels()
	if len(labels) == 0 {
		return line{{" no watched threads", sDim}}
	}
	width := func(from, to int) int {
		n := 0
		for i := from; i <= to; i++ {
			n += labels[i].width()
		}
		return n
	}
	start := 0
	for start < m.activeIdx && width(start, m.activeIdx)+2 > w {
		start++
	}
	var out line
	used := 0
	if start > 0 {
		out = append(out, seg{"‹", sDim})
		used++
	}
	for i := start; i < len(labels); i++ {
		lw := labels[i].width()
		reserve := 0
		if i < len(labels)-1 {
			reserve = 1
		}
		if used+lw+reserve > w && i > start {
			out = append(out, seg{"›", sDim})
			break
		}
		out = append(out, labels[i]...)
		used += lw
	}
	return out
}

func (m *Model) header(w int) []line {
	t := m.current()
	if t == nil {
		return []line{{{" Nothing watched yet", sBold}}, {{" press a and paste a message link, or run: slack-tabbed-tui add <link>", sDim}}}
	}
	where := "#" + t.ChannelName
	if t.IsDM {
		where = "DM with " + t.ChannelName
	} else if t.ChannelName == "" {
		where = t.Channel
	}
	title := line{{" " + where, sBold + sAccent}, {"  " + shortHost(t.Workspace), sSecondary}}
	replies := max(len(t.Messages)-1, 0)
	people := map[string]bool{}
	for _, msg := range t.Messages {
		people[msg.Author] = true
	}
	right := line{{fmt.Sprintf("%d %s · %d %s ", replies, plural(replies, "reply", "replies"), len(people), plural(len(people), "person", "people")), sSecondary}}
	if len(t.Messages) == 0 {
		right = nil
	}
	l1 := spread(title, right, w)

	dot := seg{" · ", sDim}
	var l2 line
	add := func(s seg) {
		if len(l2) > 0 {
			l2 = append(l2, dot)
		}
		l2 = append(l2, s)
	}
	switch {
	case len(t.Messages) == 0 && t.Error == "":
		add(seg{"waiting for the first fetch…", sDim})
	case len(t.Messages) > 0:
		add(seg{truncate(t.Title(), max(w/2, 20)), sWhite})
		if last := t.Messages[len(t.Messages)-1]; len(t.Messages) > 1 {
			add(seg{"last reply " + human(m.now().Sub(last.Time())) + " ago", sMuted})
		}
	}
	if u, mn := t.Unread(); mn > 0 {
		add(seg{fmt.Sprintf("%d unread (%d mentioning you)", u, mn), sRed + sBold})
	} else if u > 0 {
		add(seg{fmt.Sprintf("%d unread", u), sAccent + sBold})
	}
	if t.Alerts {
		add(seg{bellIcon + " alerts on", sAccent})
	}
	if t.Error != "" {
		add(seg{"! " + t.Error, sRed})
	}
	if len(l2) > 0 {
		l2 = append(line{{" ", ""}}, l2...)
	}
	return []line{l1, l2}
}

// msgLines renders one message, w cells wide.
func (m *Model) msgLines(t *model.Thread, msg model.Message, w int, selected bool) []line {
	bar := seg{"  ", ""}
	if selected {
		bar = seg{"▌ ", sAccent}
		if !m.Color {
			bar = seg{"› ", ""}
		}
	}
	indent := seg{"  ", ""}
	if selected {
		indent = seg{"▌ ", sAccent}
		if !m.Color {
			indent = seg{"  ", ""}
		}
	}
	authorStyle := sBold + sWhite
	if msg.Mine {
		authorStyle = sBold + sAccent
	}
	head := line{bar, {msg.Author, authorStyle}}
	if msg.Bot {
		head = append(head, seg{" APP", sDim + sBold})
	}
	if msg.Mine {
		head = append(head, seg{" (you)", sDim})
	}
	if msg.Broadcast {
		head = append(head, seg{"  ↪ also sent to the channel", sDim})
	}
	when := msg.Time().Local().Format("Mon 2 Jan 15:04")
	if msg.Edited {
		when = "(edited) " + when
	}
	out := []line{spread(head, line{{when + " ", sSecondary}}, w)}
	bodyStyle := sText
	if msg.MentionsMe && !msg.Mine {
		bodyStyle = sText + sBold
	}
	for _, l := range wrap(msg.Text, w-4) {
		out = append(out, line{indent, {"  " + l, bodyStyle}})
	}
	for _, a := range msg.Attachments {
		for _, l := range wrap(a, w-6) {
			out = append(out, line{indent, {"  ┃ ", sBlue}, {l, sMuted}})
		}
	}
	for _, f := range msg.Files {
		out = append(out, line{indent, {"  [file] ", sDim}, {f.Name, sMuted}, {"  " + size(f.Size), sDim}})
	}
	if len(msg.Reactions) > 0 {
		l := line{indent, {" ", ""}}
		for _, r := range msg.Reactions {
			style := sMuted
			if r.Mine {
				style = sAccent + sBold
			}
			l = append(l, seg{" ", ""}, seg{fmt.Sprintf(":%s: %d", r.Name, r.Count), style})
		}
		out = append(out, l)
	}
	if selected && m.Color {
		out[0] = withStyle(fill(out[0], w, ""), bgSelect)
	}
	return out
}

func size(n int64) string {
	switch {
	case n <= 0:
		return ""
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// threadRows lays out the whole thread and returns the rows with the
// first row of each message.
func (m *Model) threadRows(t *model.Thread, w int) ([]line, []int) {
	sel := m.selIndex(t)
	var rows []line
	starts := make([]int, len(t.Messages))
	divider := false
	for i, msg := range t.Messages {
		if i == 1 {
			n := len(t.Messages) - 1
			label := "  ── " + strconv.Itoa(n) + " " + plural(n, "reply", "replies") + " "
			rows = append(rows, line{{label + strings.Repeat("─", max(w-strWidth(label)-2, 0)), sBorder}})
		}
		if !divider && i > 0 && !msg.Mine && model.After(msg.TS, t.ReadTS) {
			divider = true
			label := " new "
			rows = append(rows, line{{"  " + strings.Repeat("─", 3), sRed}, {label, sRed + sBold}, {strings.Repeat("─", max(w-4-3-len(label), 0)), sRed}})
		}
		starts[i] = len(rows)
		rows = append(rows, m.msgLines(t, msg, w, i == sel)...)
		rows = append(rows, line{})
	}
	return rows, starts
}

func (m *Model) messagesBody(w, rows int) []line {
	m.pageRows, m.listWidth = rows, w
	t := m.current()
	if t == nil {
		return nil
	}
	if len(t.Messages) == 0 {
		if t.Error != "" {
			return []line{{{" " + t.Error, sRed}}}
		}
		return []line{{{" fetching the thread…", sDim}}}
	}
	all, starts := m.threadRows(t, w)
	sel := m.selIndex(t)
	first := starts[sel]
	last := len(all) - 1
	if sel+1 < len(starts) {
		last = starts[sel+1] - 1
	}
	top := m.top[t.ID]
	// Keep the selected message in view, its top first.
	if last >= top+rows {
		top = last - rows + 1
	}
	if first < top {
		top = first
	}
	// The parent separator and the "new" divider belong with the message
	// below them.
	for top > 0 && top == first && len(all[top-1]) > 0 {
		top--
	}
	top = max(min(top, len(all)-rows), 0)
	m.top[t.ID] = top
	end := min(top+rows, len(all))
	return all[top:end]
}

// scrollRows moves the selection to the message rows below (or above)
// the selected one's top.
func (m *Model) scrollRows(t *model.Thread, delta, w int) {
	_, starts := m.threadRows(t, w)
	target := starts[m.selIndex(t)] + delta
	i := 0
	for j, s := range starts {
		if s <= target {
			i = j
		}
	}
	if delta > 0 && i == m.selIndex(t) && i < len(starts)-1 {
		i++
	}
	m.sel[t.ID] = t.Messages[i].TS
}

func (m *Model) findBody(w, rows int) []line {
	t := m.current()
	matches := m.findMatches()
	if t == nil || len(matches) == 0 {
		return []line{{{" no matching message", sDim}}}
	}
	sel := min(m.findSel, len(matches)-1)
	m.findSel = sel
	out := []line{{{fmt.Sprintf(" %d of %d messages match", len(matches), len(t.Messages)), sDim}}}
	start := max(sel-(rows-1)+1, 0)
	for n, idx := range matches[start:] {
		if len(out) >= rows {
			break
		}
		msg := t.Messages[idx]
		text := strings.Join(strings.Fields(msg.Text), " ")
		mark := " "
		if start+n == sel && !m.Color {
			mark = "›"
		}
		l := line{{mark + padRight(msg.Author, 16), sBold + sWhite}, {" " + text, sText}}
		l = spread(l, line{{msg.Time().Local().Format("Mon 2 Jan 15:04") + " ", sSecondary}}, w)
		if start+n == sel && m.Color {
			l = withStyle(fill(l, w, ""), bgSelect+sBold)
		}
		out = append(out, l)
	}
	return out
}

// composer is the reply box: the draft, then its keys.
func (m *Model) composer(w int) []line {
	t := m.current()
	if t == nil {
		return nil
	}
	text := string(m.input) + "▏"
	all := wrap(text, w-4)
	const maxRows = 6
	if len(all) > maxRows {
		all = all[len(all)-maxRows:]
	}
	var content []line
	for _, l := range all {
		content = append(content, line{{" " + l, sWhite}})
	}
	title := " Reply in " + tabName(*t) + " "
	if m.broadcast[t.ID] {
		title += "· also send to the channel "
	}
	b := box(content, w, sAccent, "", true)
	b[0] = line{{"╭─", sAccent}, {title, sBold + sAccent}, {strings.Repeat("─", max(w-3-strWidth(title), 0)) + "╮", sAccent}}
	return b
}

// overlay draws a panel centered on an empty screen, as in ghwatch.
func overlay(w, h int, title string, content []line) []line {
	body := []line{{{" " + title + " ", bgAccent + sBold + sWhite}}, {}}
	body = append(body, content...)
	inner := 0
	for _, l := range body {
		inner = max(inner, l.width())
	}
	bw := min(inner+6, w)
	var padded []line
	padded = append(padded, line{})
	for _, l := range body {
		padded = append(padded, append(line{{"  ", ""}}, l...))
	}
	padded = append(padded, line{})
	for i := range padded {
		padded[i] = withStyle(fill(padded[i], bw-2, ""), bgOverlay)
	}
	panel := box(padded, bw, sAccent, "", true)
	if len(panel) > h {
		panel = append(panel[:h-1], panel[len(panel)-1])
	}
	top, left := (h-len(panel))/2, (w-bw)/2
	out := make([]line, top, h)
	for _, l := range panel {
		out = append(out, append(line{{strings.Repeat(" ", left), ""}}, l...))
	}
	return out
}

func (m *Model) eventsContent() []line {
	var st model.Settings
	if m.snap != nil {
		st = m.snap.Settings
	}
	check := func(on bool) string {
		if on {
			return "[x] "
		}
		return "[ ] "
	}
	const rowW = 56
	row := func(i int, text string) line {
		if i == m.evSel {
			l := line{{"› ", sAccent + sBold}, {text, sBold + sWhite}}
			if m.Color {
				return withStyle(fill(l, rowW, ""), bgSoft)
			}
			return l
		}
		return line{{"  ", ""}, {text, sText}}
	}
	out := []line{
		{{"Notify on these events, for threads with alerts on (n).", sMuted}},
		{},
	}
	for i, e := range model.EventTypes {
		out = append(out, row(i, check(st.Events[e])+e.Label()))
	}
	out = append(out, line{}, row(len(model.EventTypes), check(st.Mute)+"global mute (keeps per-thread settings)"))
	return append(out, line{}, line{{"j/k move · space toggle · esc close", sDim}})
}

var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Navigation", [][2]string{
		{"h / l, gT / gt", "previous / next thread"},
		{"1 – 9", "jump to thread N"},
		{"j / k, gg / G", "next / prev message, first / last"},
		{"ctrl+d / ctrl+u", "half a page down / up"},
		{"/", "find a message"},
	}},
	{"Writing", [][2]string{
		{"i", "reply in the thread"},
		{"enter", "send (in the reply box)"},
		{"alt+enter, ctrl+j", "new line"},
		{"ctrl+b", "also send to the channel"},
		{"esc / ctrl+c", "keep the draft / discard it"},
	}},
	{"Links", [][2]string{
		{"o / enter", "open thread / message in Slack"},
		{"O", "open the message's file or link"},
		{"y / Y", "copy thread / message link"},
	}},
	{"Threads", [][2]string{
		{"a", "add a thread (paste a link)"},
		{"d", "unwatch this thread"},
		{"u", "mark as read"},
		{"r", "fetch every thread now"},
	}},
	{"Notifications", [][2]string{
		{"n", "alerts for this thread"},
		{"N", "alert settings, mute"},
	}},
	{"Other", [][2]string{
		{"? / esc", "close this panel"},
		{"q", "quit (the daemon keeps running)"},
	}},
}

func (m *Model) helpContent(w, h int) []line {
	const keyW, gap = 18, 3
	section := func(i int, descW int) []line {
		sec := helpSections[i]
		out := []line{{{sec.title, sBold + sMuted}}}
		for _, k := range sec.keys {
			out = append(out, line{{padRight(k[0], keyW), sBold + sAccent}, {truncate(k[1], descW), sText}})
		}
		return out
	}
	avail := h - 8
	for cols := 1; ; cols++ {
		descW := min(40, max((w-5-gap*(cols-1))/cols-keyW, 12))
		var columns [][]line
		var cur []line
		total := 0
		for i := range helpSections {
			total += len(helpSections[i].keys) + 2
		}
		target := (total + cols - 1) / cols
		for i := range helpSections {
			s := section(i, descW)
			if len(cur) > 0 && len(cur)+1+len(s) > target && len(columns) < cols-1 {
				columns = append(columns, cur)
				cur = nil
			}
			if len(cur) > 0 {
				cur = append(cur, line{})
			}
			cur = append(cur, s...)
		}
		columns = append(columns, cur)
		height := 0
		for _, c := range columns {
			height = max(height, len(c))
		}
		if height <= avail || cols == 3 {
			colW := keyW + descW + gap
			out := make([]line, height)
			for r := 0; r < height; r++ {
				for c, col := range columns {
					var l line
					if r < len(col) {
						l = col[r]
					}
					if c < len(columns)-1 {
						l = fill(l, colW, "")
					}
					out[r] = append(out[r], l...)
				}
			}
			return out
		}
	}
}

func (m *Model) footer(w int) line {
	var right line
	if m.snap != nil && m.snap.Settings.Mute {
		right = append(right, seg{"muted · ", sDim})
	}
	if m.connected {
		right = append(right, seg{"connected", sGreen})
	} else {
		right = append(right, seg{"disconnected", sRed + sBold})
	}
	right = append(right, seg{" ", ""})

	var left line
	switch {
	case m.mode == modeAdd:
		left = line{{"  Add a thread (message link): ", sBold + sAccent}, {string(m.input) + "_", sWhite}}
	case m.mode == modeFind:
		left = line{{"  /", sBold + sAccent}, {string(m.input) + "_", sWhite}, {"   ↑/↓ choose · enter select · esc cancel", sDim}}
	case m.mode == modeConfirm:
		left = line{{"  " + m.confirmMsg + " ", sBold + sWhite}, {"[y/N]", sYellow}}
	case m.mode == modeCompose:
		left = composeHints(w - right.width() - 1)
	case m.flash != "" && m.now().Before(m.flashUntil):
		if m.flashErr {
			left = line{{"  × " + m.flash, sBold + sRed}}
		} else {
			left = line{{"  ✓ " + m.flash, sBold + sGreen}}
		}
	default:
		left = hintLine(footerHints, w-right.width()-1)
	}
	return spread(left, right, w)
}

type hint struct {
	key, label string
	drop       int
}

// footerHints say what the main keys do; drop is the order in which
// they give way on narrow screens (higher first). "? all keys" stays.
var footerHints = []hint{
	{"h/l", "prev/next thread", 3},
	{"j/k", "next/prev message", 4},
	{"i", "reply", 1},
	{"o", "open in Slack", 2},
	{"y/Y", "copy link", 7},
	{"n", "alerts", 5},
	{"u", "mark read", 8},
	{"/", "find", 6},
	{"?", "all keys", 0},
}

var composeKeys = []hint{
	{"enter", "send", 0},
	{"alt+enter", "new line", 2},
	{"ctrl+b", "also to channel", 3},
	{"esc", "keep draft", 1},
	{"ctrl+c", "discard", 4},
}

func composeHints(w int) line { return hintLine(composeKeys, w) }

// hintLine renders as many hints as fit in w cells.
func hintLine(hints []hint, w int) line {
	keep := make([]bool, len(hints))
	for i := range keep {
		keep[i] = true
	}
	build := func() line {
		l := line{{"  ", ""}}
		first := true
		for i, h := range hints {
			if !keep[i] {
				continue
			}
			if !first {
				l = append(l, seg{" · ", sDim})
			}
			first = false
			l = append(l, seg{h.key, sBold + sAccent}, seg{" " + h.label, sSecondary})
		}
		return l
	}
	for l := build(); ; l = build() {
		if l.width() <= w {
			return l
		}
		worst := -1
		for i, h := range hints {
			if keep[i] && h.drop > 0 && (worst < 0 || h.drop > hints[worst].drop) {
				worst = i
			}
		}
		if worst < 0 {
			return l
		}
		keep[worst] = false
	}
}
