// Command slack-tabbed-tui follows a hand-picked set of Slack threads
// from the terminal.
//
//	slack-tabbed-tui                          open the tabbed TUI
//	slack-tabbed-tui -serve                   run the daemon in the foreground
//	slack-tabbed-tui auth [<workspace URL>]   sign in, or check sessions
//	slack-tabbed-tui add <link>...            watch threads
//	slack-tabbed-tui rm <link>...             stop watching threads
//	slack-tabbed-tui ls                       list watched threads
//	slack-tabbed-tui show <link>              print a thread, fetched now
//	slack-tabbed-tui reply <link> [text|-]    post a reply
//	slack-tabbed-tui status [-json]           one-line summary for tmux
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"text/tabwriter"
	"text/template"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/browser"
	"github.com/clobrano/slack-tabbed-tui/internal/config"
	"github.com/clobrano/slack-tabbed-tui/internal/creds"
	"github.com/clobrano/slack-tabbed-tui/internal/daemon"
	"github.com/clobrano/slack-tabbed-tui/internal/directory"
	"github.com/clobrano/slack-tabbed-tui/internal/ipc"
	"github.com/clobrano/slack-tabbed-tui/internal/login"
	"github.com/clobrano/slack-tabbed-tui/internal/model"
	"github.com/clobrano/slack-tabbed-tui/internal/notify"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/tui"
	"github.com/clobrano/slack-tabbed-tui/internal/watchlist"
)

const usage = `Usage:
  slack-tabbed-tui                          open the tabbed TUI
  slack-tabbed-tui -serve                   run the daemon in the foreground
  slack-tabbed-tui auth                     list sessions and check they still work
  slack-tabbed-tui auth <workspace URL>     sign in to a workspace, in a browser window
  slack-tabbed-tui auth -manual <URL>       sign in by pasting the token and cookie
  slack-tabbed-tui auth -rm <workspace>     forget a workspace's session
  slack-tabbed-tui add <link>...            watch threads (a message link from Slack)
  slack-tabbed-tui rm <link>...             stop watching threads
  slack-tabbed-tui ls                       list watched threads
  slack-tabbed-tui show <link>              print a thread, fetched now
  slack-tabbed-tui reply <link> [text|-]    post a reply (text from args, or stdin)
  slack-tabbed-tui status [-json]           one-line summary, e.g. for tmux status-right

Flags:
`

// app holds what the commands need, so tests can swap it.
type app struct {
	paths config.Paths
	cfg   config.Config
	store creds.Store
	in    io.Reader
	out   io.Writer
	// client builds an API client for a session.
	client func(c creds.Credential) *slack.Client
	// login signs in through a browser.
	login func(ctx context.Context, workspaceURL string) (login.Session, error)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("slack-tabbed-tui: ")
	serve := flag.Bool("serve", false, "run the daemon in the foreground")
	idleExit := flag.Duration("idle-exit", 0, "with -serve: exit after this long without clients (0: never)")
	flag.Usage = func() {
		fmt.Fprint(flag.CommandLine.Output(), usage)
		flag.PrintDefaults()
	}
	flag.Parse()
	paths := config.DefaultPaths()
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		log.Fatal(err)
	}
	a := &app{
		paths:  paths,
		cfg:    cfg,
		store:  creds.Store{Path: paths.Credentials(), Secrets: creds.NewSecretTool()},
		in:     os.Stdin,
		out:    os.Stdout,
		client: func(c creds.Credential) *slack.Client { return slack.New(c.URL, c.Token, c.Cookie) },
		login: func(ctx context.Context, workspaceURL string) (login.Session, error) {
			bin, err := login.FindBrowser()
			if err != nil {
				return login.Session{}, err
			}
			ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			defer cancel()
			return login.Login(ctx, workspaceURL, login.Options{
				Launch:   login.ExecLauncher(bin, filepath.Join(paths.StateDir, "browser")),
				Progress: func(m string) { fmt.Fprintln(os.Stdout, m) },
			})
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	switch {
	case *serve:
		err = a.serve(ctx, *idleExit)
	case flag.NArg() == 0:
		stop() // the TUI reads ctrl-c as a key
		err = tui.Run(context.Background(), tui.Options{Paths: paths, Config: cfg})
	default:
		err = a.run(ctx, flag.Args())
	}
	if err != nil {
		log.Fatal(err)
	}
}

func (a *app) serve(ctx context.Context, idleExit time.Duration) error {
	d := &daemon.Daemon{
		Paths:    a.paths,
		Config:   a.cfg,
		Store:    a.store,
		Client:   a.client,
		IdleExit: idleExit,
		Log:      log.New(os.Stderr, "slack-tabbed-tui: ", log.LstdFlags),
	}
	switch a.cfg.Notifier {
	case "desktop", "":
		d.Notifier = &notify.Desktop{Open: func(url string) error { return browser.Open(a.cfg.Browser, url) }}
	case "exec":
		if a.cfg.NotifyCommand == "" {
			return errors.New(`notifier "exec" needs notify_command in config.toml`)
		}
		d.Notifier = notify.Exec{Command: a.cfg.NotifyCommand}
	case "none":
		d.Notifier = notify.Nop{}
	default:
		return fmt.Errorf("unknown notifier %q (want desktop, exec or none)", a.cfg.Notifier)
	}
	return d.Run(ctx)
}

// dial connects to a running daemon, or returns nil.
func (a *app) dial() *ipc.Client {
	c, err := ipc.Dial(a.paths.Socket())
	if err != nil {
		return nil
	}
	return c
}

func (a *app) run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(a.out, usage)
		return nil
	}
	cmd, args := args[0], args[1:]
	switch cmd {
	case "auth":
		return a.cmdAuth(ctx, args)
	case "add":
		return a.cmdAdd(ctx, args)
	case "rm", "remove":
		return a.cmdRemove(ctx, args)
	case "ls", "list":
		return a.cmdList()
	case "show":
		return a.cmdShow(ctx, args)
	case "reply":
		return a.cmdReply(ctx, args)
	case "status":
		return a.cmdStatus(args)
	case "help", "-h", "--help":
		fmt.Fprint(a.out, usage)
		return nil
	}
	return fmt.Errorf("unknown command %q (see slack-tabbed-tui help)", cmd)
}

func (a *app) cmdAuth(ctx context.Context, args []string) error {
	switch {
	case len(args) == 0:
		return a.authCheck(ctx)
	case args[0] == "-rm" && len(args) == 2:
		ok, err := a.store.Remove(workspaceHost(args[1]))
		if err == nil && !ok {
			err = fmt.Errorf("no session for %s", args[1])
		}
		return err
	case len(args) == 1:
		return a.authSignIn(ctx, args[0], false)
	case len(args) == 2 && args[0] == "-manual":
		return a.authSignIn(ctx, args[1], true)
	}
	return errors.New("usage: slack-tabbed-tui auth [[-manual] <workspace URL> | -rm <workspace>]")
}

func (a *app) authCheck(ctx context.Context) error {
	all, err := a.store.All()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		fmt.Fprintln(a.out, "Not signed in. Run: slack-tabbed-tui auth https://<workspace>.slack.com")
		return nil
	}
	for _, c := range all {
		state := "ok"
		if _, err := a.client(c).AuthTest(ctx); err != nil {
			state = err.Error()
			var se *slack.Error
			if errors.As(err, &se) && se.Auth() {
				state = "logged out: run `slack-tabbed-tui auth " + c.URL + "`"
			}
		}
		fmt.Fprintf(a.out, "%s\t%s as %s\t%s\n", c.Workspace, c.Team, c.User, state)
	}
	return nil
}

// authSignIn stores a browser session for a workspace: from a login in a
// browser window the app opens, or with -manual (or no browser found)
// from a token and cookie the user copies from their own browser.
func (a *app) authSignIn(ctx context.Context, workspace string, manual bool) error {
	host := workspaceHost(workspace)
	if host == "" {
		return fmt.Errorf("%q is not a workspace URL, e.g. https://acme.slack.com", workspace)
	}
	token, cookie := os.Getenv(creds.EnvToken), os.Getenv(creds.EnvCookie)
	if (token == "" || cookie == "") && !manual {
		s, err := a.login(ctx, "https://"+host+"/")
		switch {
		case errors.Is(err, login.ErrNoBrowser):
			fmt.Fprintf(a.out, "%v: sign in by hand instead.\n\n", err)
		case err != nil:
			return fmt.Errorf("browser sign-in: %w (try `slack-tabbed-tui auth -manual %s`)", err, host)
		default:
			team, err := s.Pick(host)
			if err != nil {
				return err
			}
			token, cookie = team.Token, s.Cookie
			for _, other := range s.Teams {
				if other.ID != team.ID {
					fmt.Fprintf(a.out, "The browser is also signed in to %s: run `slack-tabbed-tui auth %s` to use it too.\n", other.Host(), other.Host())
				}
			}
		}
	}
	if token == "" || cookie == "" {
		fmt.Fprintf(a.out, `Sign in to https://%s in your browser, then open its developer tools:
  token:  in the Console, run  JSON.parse(localStorage.localConfig_v2).teams
          and copy the "token" (xoxc-…) of the team whose "url" is https://%s/
  cookie: in Application (Chrome) or Storage (Firefox) > Cookies, copy the
          value of the cookie named "d" (xoxd-…)
`, host, host)
		r := bufio.NewReader(a.in)
		var err error
		if token, err = prompt(a.out, r, "xoxc token: "); err != nil {
			return err
		}
		if cookie, err = prompt(a.out, r, "d cookie:   "); err != nil {
			return err
		}
	}
	if !strings.HasPrefix(token, "xoxc-") {
		return errors.New("the token must start with xoxc-")
	}
	if !strings.HasPrefix(cookie, "xoxd-") {
		return errors.New("the cookie must start with xoxd-")
	}
	c := creds.Credential{URL: "https://" + host + "/", Token: token, Cookie: cookie}
	info, err := a.client(c).AuthTest(ctx)
	if err != nil {
		return fmt.Errorf("checking the session: %w", err)
	}
	if u, err := url.Parse(info.URL); err == nil && u.Host != "" {
		c.URL = info.URL // Slack's canonical URL for the workspace
	}
	c.Workspace = workspaceHost(c.URL)
	c.TeamID, c.Team, c.EnterpriseID = info.TeamID, info.Team, info.EnterpriseID
	c.UserID, c.User = info.UserID, info.User
	if err := a.store.Save(c); err != nil {
		return err
	}
	where := a.store.Path
	if saved, err := a.store.Lookup(c.TeamID); err == nil && saved.Keyring {
		where = "the keyring"
	}
	fmt.Fprintf(a.out, "Signed in to %s (%s) as %s. The session is stored in %s.\n", c.Team, c.Workspace, c.User, where)
	return nil
}

func prompt(w io.Writer, r *bufio.Reader, label string) (string, error) {
	fmt.Fprint(w, label)
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("reading %s: %w", strings.TrimSpace(label), err)
	}
	return strings.TrimSpace(line), nil
}

// workspaceHost turns "https://acme.slack.com/", "acme.slack.com" or
// "acme" into "acme.slack.com"; a team ID is returned as is.
func workspaceHost(s string) string {
	s = strings.TrimSpace(s)
	if teamID.MatchString(s) {
		return s
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	h := strings.ToLower(u.Host)
	if h != "" && !strings.Contains(h, ".") {
		h += ".slack.com"
	}
	return h
}

var teamID = regexp.MustCompile(`^T[A-Z0-9]{2,}$`)

// parse turns a link or ID into a canonical thread ID. A team ID
// (app.slack.com links) is replaced by the workspace host when a session
// for that team is stored, so both link forms name the same thread.
func (a *app) parse(s string) (slack.Ref, error) {
	r, err := slack.ParseRef(s)
	if err != nil {
		return r, err
	}
	r.Workspace = a.store.Canonical(r.Workspace)
	return r, nil
}

func (a *app) parseID(s string) (string, error) {
	r, err := a.parse(s)
	return r.ID(), err
}

func (a *app) cmdAdd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: slack-tabbed-tui add <link>...")
	}
	if c := a.dial(); c != nil {
		// The daemon checks the thread and updates every client.
		defer c.Close()
		var errs []error
		for _, s := range args {
			info, err := c.Do(ctx, ipc.Command{Op: ipc.OpAdd, ID: s})
			if err != nil {
				errs = append(errs, err)
				continue
			}
			fmt.Fprintln(a.out, info)
		}
		return errors.Join(errs...)
	}
	for _, s := range args {
		id, err := a.parseID(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
		changed, err := watchlist.Add(a.paths.Watchlist(), id, a.parseID)
		if err != nil {
			return err
		}
		if changed {
			fmt.Fprintln(a.out, "watching", id)
		} else {
			fmt.Fprintln(a.out, "already watching", id)
		}
	}
	return nil
}

func (a *app) cmdRemove(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: slack-tabbed-tui rm <link>...")
	}
	c := a.dial()
	if c != nil {
		defer c.Close()
	}
	for _, s := range args {
		id, err := a.parseID(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
		if c != nil {
			info, err := c.Do(ctx, ipc.Command{Op: ipc.OpUnwatch, ID: id})
			if err != nil {
				return err
			}
			fmt.Fprintln(a.out, info)
			continue
		}
		changed, err := watchlist.Remove(a.paths.Watchlist(), id, a.parseID)
		if err != nil {
			return err
		}
		if !changed {
			return fmt.Errorf("not watching %s", id)
		}
		fmt.Fprintln(a.out, "stopped watching", id)
	}
	return nil
}

func (a *app) cmdList() error {
	ids, bad, err := watchlist.Load(a.paths.Watchlist(), a.parseID)
	if err != nil {
		return err
	}
	for _, e := range bad {
		fmt.Fprintln(os.Stderr, "warning:", e)
	}
	snap, _ := daemon.ReadSnapshot(a.paths.Snapshot())
	tw := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	for _, id := range ids {
		r, _ := slack.ParseRef(id)
		var t *model.Thread
		if snap != nil {
			t = snap.Find(id)
		}
		if t == nil || len(t.Messages) == 0 {
			fmt.Fprintf(tw, "%s\t\t%s\n", id, r.Permalink())
			continue
		}
		u, _ := t.Unread()
		title := []rune(t.Title())
		if len(title) > 50 {
			title = append(title[:49], '…')
		}
		where := "#" + t.ChannelName
		if t.IsDM {
			where = "@" + t.ChannelName
		}
		fmt.Fprintf(tw, "%s\t%s\t%d unread\t%s\n", id, where, u, string(title))
	}
	return tw.Flush()
}

// StatusData is the data available to the status template.
type StatusData struct {
	Threads, Unread, Mentions int
	Stale                     bool
}

func (a *app) cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the whole snapshot as JSON")
	format := fs.String("format", "", "text/template for the summary (default from config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asJSON {
		data, err := os.ReadFile(a.paths.Snapshot())
		if err != nil {
			return err
		}
		_, err = a.out.Write(data)
		return err
	}
	tmpl := a.cfg.StatusTemplate
	if *format != "" {
		tmpl = *format
	}
	t, err := template.New("status").Parse(tmpl)
	if err != nil {
		return fmt.Errorf("status template: %w", err)
	}
	var data StatusData
	snap, err := daemon.ReadSnapshot(a.paths.Snapshot())
	if err != nil {
		data.Stale = true
	} else {
		data.Threads = len(snap.Threads)
		for _, th := range snap.Threads {
			u, mn := th.Unread()
			data.Unread += u
			data.Mentions += mn
		}
		data.Stale = !daemon.Running(a.paths.Lock())
		for _, w := range snap.Workspaces {
			data.Stale = data.Stale || w.Conn == model.ConnLoggedOut || w.Conn == model.ConnNoSession
		}
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return err
	}
	_, err = fmt.Fprintln(a.out, strings.TrimSpace(b.String()))
	return err
}

// session returns the client and the directory for a thread's workspace.
func (a *app) session(r slack.Ref) (*slack.Client, *directory.Directory, error) {
	c, err := a.store.Lookup(r.Workspace)
	if err != nil {
		return nil, nil, err
	}
	team := c.TeamID
	if team == "" {
		team = r.Workspace
	}
	return a.client(c), directory.Load(a.paths.Directory(team)), nil
}

func (a *app) cmdShow(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: slack-tabbed-tui show <link>")
	}
	r, err := a.parse(args[0])
	if err != nil {
		return err
	}
	cl, dir, err := a.session(r)
	if err != nil {
		return err
	}
	msgs, err := cl.Replies(ctx, r.Channel, r.ThreadTS, "")
	if err != nil {
		return err
	}
	// Names are best effort: a missing one shows as its ID.
	_ = dir.Resolve(ctx, cl, directory.Mentioned(msgs), []string{r.Channel})
	_ = dir.Save()
	printThread(a.out, r, msgs, dir)
	return nil
}

func printThread(w io.Writer, r slack.Ref, msgs []slack.Message, dir *directory.Directory) {
	where := "#" + dir.ChannelName(r.Channel)
	if c := dir.Channel(r.Channel); c != nil && c.IsIM {
		where = "DM with " + dir.ChannelName(r.Channel)
	} else if where == "#" {
		where = r.Channel
	}
	replies := max(len(msgs)-1, 0)
	fmt.Fprintf(w, "%s · %d %s · %s\n", where, replies, plural(replies, "reply", "replies"), r.Permalink())
	for i, m := range msgs {
		fmt.Fprintln(w)
		author := dir.Author(m)
		if m.IsBot() {
			author += " [APP]"
		}
		head := author + " · " + m.Time().Local().Format("Mon 2 Jan 15:04")
		if m.Edited != nil {
			head += " (edited)"
		}
		if m.TS == r.MessageTS && r.MessageTS != r.ThreadTS {
			head += "  ← linked"
		}
		if i == 0 && len(msgs) > 1 {
			head += "  — thread start"
		}
		fmt.Fprintln(w, head)
		for _, line := range strings.Split(slack.PlainText(m.Text, dir), "\n") {
			fmt.Fprintln(w, "  "+line)
		}
		for _, at := range m.Attachments {
			if t := firstNonEmpty(at.Title, at.Fallback, at.Pretext); t != "" {
				fmt.Fprintln(w, "  ▏"+slack.PlainText(t, dir))
			}
		}
		for _, f := range m.Files {
			fmt.Fprintf(w, "  [file] %s (%s)\n", firstNonEmpty(f.Name, f.Title), size(f.Size))
		}
		if len(m.Reactions) > 0 {
			var parts []string
			for _, re := range m.Reactions {
				parts = append(parts, fmt.Sprintf(":%s: %d", re.Name, re.Count))
			}
			fmt.Fprintln(w, "  "+strings.Join(parts, "  "))
		}
	}
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func size(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

func (a *app) cmdReply(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: slack-tabbed-tui reply <link> [text|-]")
	}
	r, err := a.parse(args[0])
	if err != nil {
		return err
	}
	text := strings.Join(args[1:], " ")
	if text == "" || text == "-" {
		data, err := io.ReadAll(a.in)
		if err != nil {
			return err
		}
		text = strings.TrimRight(string(data), "\n")
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("empty reply")
	}
	cl, _, err := a.session(r)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m, err := cl.PostReply(ctx, r.Channel, r.ThreadTS, slack.Escape(text), false)
	if err != nil {
		return err
	}
	fmt.Fprintln(a.out, "posted", m.TS)
	return nil
}
