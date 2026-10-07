// Command slack-tabbed-tui follows a hand-picked set of Slack threads
// from the terminal.
//
//	slack-tabbed-tui auth [<workspace URL>]   sign in, or check sessions
//	slack-tabbed-tui add <link>...            watch threads
//	slack-tabbed-tui rm <link>...             stop watching threads
//	slack-tabbed-tui ls                       list watched threads
//	slack-tabbed-tui show <link>              print a thread, fetched now
//	slack-tabbed-tui reply <link> [text|-]    post a reply
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
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/clobrano/slack-tabbed-tui/internal/config"
	"github.com/clobrano/slack-tabbed-tui/internal/creds"
	"github.com/clobrano/slack-tabbed-tui/internal/directory"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
	"github.com/clobrano/slack-tabbed-tui/internal/watchlist"
)

const usage = `Usage:
  slack-tabbed-tui auth                     list sessions and check they still work
  slack-tabbed-tui auth <workspace URL>     sign in to a workspace
  slack-tabbed-tui auth -rm <workspace>     forget a workspace's session
  slack-tabbed-tui add <link>...            watch threads (a message link from Slack)
  slack-tabbed-tui rm <link>...             stop watching threads
  slack-tabbed-tui ls                       list watched threads
  slack-tabbed-tui show <link>              print a thread, fetched now
  slack-tabbed-tui reply <link> [text|-]    post a reply (text from args, or stdin)

The TUI is not there yet: see docs/PRD.md for the plan.
`

// app holds what the commands need, so tests can swap it.
type app struct {
	paths config.Paths
	store creds.Store
	in    io.Reader
	out   io.Writer
	// client builds an API client for a session.
	client func(c creds.Credential) *slack.Client
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("slack-tabbed-tui: ")
	flag.Usage = func() { fmt.Fprint(flag.CommandLine.Output(), usage) }
	flag.Parse()
	paths := config.DefaultPaths()
	a := &app{
		paths:  paths,
		store:  creds.Store{Path: paths.Credentials()},
		in:     os.Stdin,
		out:    os.Stdout,
		client: func(c creds.Credential) *slack.Client { return slack.New(c.URL, c.Token, c.Cookie) },
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := a.run(ctx, flag.Args()); err != nil {
		log.Fatal(err)
	}
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
		return a.cmdAdd(args)
	case "rm", "remove":
		return a.cmdRemove(args)
	case "ls", "list":
		return a.cmdList()
	case "show":
		return a.cmdShow(ctx, args)
	case "reply":
		return a.cmdReply(ctx, args)
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
		return a.authSignIn(ctx, args[0])
	}
	return errors.New("usage: slack-tabbed-tui auth [<workspace URL> | -rm <workspace>]")
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

// authSignIn stores a browser session for a workspace. For now the token
// and cookie are copied by hand from the browser (PRD §6, manual
// fallback); the browser-driven login comes next.
func (a *app) authSignIn(ctx context.Context, workspace string) error {
	host := workspaceHost(workspace)
	if host == "" {
		return fmt.Errorf("%q is not a workspace URL, e.g. https://acme.slack.com", workspace)
	}
	token, cookie := os.Getenv(creds.EnvToken), os.Getenv(creds.EnvCookie)
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
	fmt.Fprintf(a.out, "Signed in to %s (%s) as %s.\n", c.Team, c.Workspace, c.User)
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
	if !strings.Contains(r.Workspace, ".") {
		if all, err := a.store.All(); err == nil {
			for _, c := range all {
				if c.TeamID == r.Workspace && c.Workspace != "" {
					r.Workspace = c.Workspace
				}
			}
		}
	}
	return r, nil
}

func (a *app) parseID(s string) (string, error) {
	r, err := a.parse(s)
	return r.ID(), err
}

func (a *app) cmdAdd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: slack-tabbed-tui add <link>...")
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

func (a *app) cmdRemove(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: slack-tabbed-tui rm <link>...")
	}
	for _, s := range args {
		id, err := a.parseID(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s, err)
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
	for _, id := range ids {
		r, _ := slack.ParseRef(id)
		fmt.Fprintf(a.out, "%s\t%s\n", id, r.Permalink())
	}
	return nil
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
