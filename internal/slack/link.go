package slack

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Ref identifies a thread: the workspace, the channel and the timestamp
// of the thread's parent message.
type Ref struct {
	// Workspace is the workspace host ("acme.slack.com",
	// "acme.enterprise.slack.com") or, for app.slack.com links, the team
	// ID ("T0123ABCD"). Credentials are looked up by either.
	Workspace string
	Channel   string
	// ThreadTS is the ts of the parent message.
	ThreadTS string
	// MessageTS is the ts of the message the link pointed to; it equals
	// ThreadTS unless the link was to a reply.
	MessageTS string
}

// ID is the canonical item ID: thread:<workspace>/<channel>/<thread_ts>.
func (r Ref) ID() string { return "thread:" + r.Workspace + "/" + r.Channel + "/" + r.ThreadTS }

// Permalink is the web link to the thread's parent message.
func (r Ref) Permalink() string {
	if !strings.Contains(r.Workspace, ".") {
		return fmt.Sprintf("https://app.slack.com/client/%s/%s/thread/%s-%s", r.Workspace, r.Channel, r.Channel, r.ThreadTS)
	}
	return fmt.Sprintf("https://%s/archives/%s/p%s", r.Workspace, r.Channel, strings.Replace(r.ThreadTS, ".", "", 1))
}

var (
	channelRe = regexp.MustCompile(`^[CGD][A-Z0-9]{2,}$`)
	teamRe    = regexp.MustCompile(`^[TE][A-Z0-9]{2,}$`)
	tsRe      = regexp.MustCompile(`^\d{10}\.\d{6}$`)
	pTSRe     = regexp.MustCompile(`^p(\d{10})(\d{6})$`)
	hostRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*(\.enterprise)?\.slack\.com$`)
)

// ErrNotThread is returned for input that is not a Slack message link.
var ErrNotThread = errors.New("not a Slack message link (copy it with \"Copy link\" on a message)")

// ParseRef accepts a Slack message link in any form Slack produces, or a
// canonical ID, and returns the thread it belongs to:
//
//	https://acme.slack.com/archives/C0123/p1700000000123456
//	https://acme.slack.com/archives/C0123/p1700000000999999?thread_ts=1700000000.123456&cid=C0123
//	https://acme.enterprise.slack.com/archives/C0123/p1700000000123456
//	https://app.slack.com/client/T0123/C0123/thread/C0123-1700000000.123456
//	thread:acme.slack.com/C0123/1700000000.123456
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "thread:"); ok {
		parts := strings.Split(rest, "/")
		if len(parts) != 3 || !validWorkspace(parts[0]) || !channelRe.MatchString(parts[1]) || !tsRe.MatchString(parts[2]) {
			return Ref{}, fmt.Errorf("invalid thread ID %q", s)
		}
		return Ref{Workspace: parts[0], Channel: parts[1], ThreadTS: parts[2], MessageTS: parts[2]}, nil
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return Ref{}, ErrNotThread
	}
	host := strings.ToLower(u.Hostname())
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case host == "app.slack.com":
		// /client/<team>/<channel>/thread/<channel>-<ts>
		if len(parts) == 5 && parts[0] == "client" && parts[3] == "thread" && teamRe.MatchString(parts[1]) && channelRe.MatchString(parts[2]) {
			ch, ts, ok := strings.Cut(parts[4], "-")
			if ok && ch == parts[2] && tsRe.MatchString(ts) {
				return Ref{Workspace: parts[1], Channel: ch, ThreadTS: ts, MessageTS: ts}, nil
			}
		}
	case hostRe.MatchString(host):
		// /archives/<channel>/p<ts without dot>
		if len(parts) == 3 && parts[0] == "archives" && channelRe.MatchString(parts[1]) {
			m := pTSRe.FindStringSubmatch(parts[2])
			if m == nil {
				break
			}
			r := Ref{Workspace: host, Channel: parts[1], MessageTS: m[1] + "." + m[2]}
			r.ThreadTS = r.MessageTS
			if t := u.Query().Get("thread_ts"); t != "" {
				if !tsRe.MatchString(t) {
					return Ref{}, fmt.Errorf("invalid thread_ts %q in link", t)
				}
				r.ThreadTS = t
			}
			return r, nil
		}
	}
	return Ref{}, ErrNotThread
}

func validWorkspace(s string) bool { return hostRe.MatchString(s) || teamRe.MatchString(s) }
