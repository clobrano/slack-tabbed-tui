// Package creds stores the browser sessions slack-tabbed-tui signs in
// with, one per workspace, in a file private to the user.
package creds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Credential is a signed-in browser session for one workspace.
type Credential struct {
	// Workspace is the workspace host, e.g. "acme.slack.com".
	Workspace    string `json:"workspace"`
	URL          string `json:"url"` // "https://acme.slack.com/"
	TeamID       string `json:"team_id"`
	Team         string `json:"team"`
	EnterpriseID string `json:"enterprise_id,omitempty"`
	UserID       string `json:"user_id"`
	User         string `json:"user"`
	Token        string `json:"token,omitempty"`  // xoxc-…
	Cookie       string `json:"cookie,omitempty"` // the d cookie (xoxd-…), or a whole Cookie header
	UserAgent    string `json:"user_agent,omitempty"`
	// Keyring is true when Token and Cookie are kept in the keyring
	// rather than in the file.
	Keyring bool `json:"keyring,omitempty"`
}

// key names the secret of a session in the keyring.
func (c Credential) key() string {
	if c.TeamID != "" {
		return c.TeamID
	}
	return c.Workspace
}

type secret struct {
	Token  string `json:"token"`
	Cookie string `json:"cookie"`
}

// Environment variables that override the stored session.
const (
	EnvToken  = "SLACK_TOKEN"
	EnvCookie = "SLACK_COOKIE"
)

// ErrNoSession means there is no session for the workspace.
var ErrNoSession = errors.New("not signed in")

// Store keeps the sessions in a JSON file with mode 0600; with Secrets
// set, the tokens and cookies go to the keyring instead.
type Store struct {
	Path    string
	Secrets Secrets
	// Getenv reads the environment; nil means os.Getenv.
	Getenv func(string) string
}

// All returns the stored sessions, with their secrets read from the
// keyring. A session whose secret cannot be read has an empty Token.
func (s Store) All() ([]Credential, error) {
	all, err := s.load()
	if err != nil {
		return nil, err
	}
	for i, c := range all {
		if !c.Keyring || s.Secrets == nil {
			continue
		}
		raw, err := s.Secrets.Get(c.key())
		if err != nil {
			continue
		}
		var sec secret
		if json.Unmarshal([]byte(raw), &sec) == nil {
			all[i].Token, all[i].Cookie = sec.Token, sec.Cookie
		}
	}
	return all, nil
}

func (s Store) load() ([]Credential, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var all []Credential
	if err := json.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("%s: %w", s.Path, err)
	}
	return all, nil
}

// Lookup returns the session for a workspace, given as a host
// ("acme.slack.com") or a team ID. When only one session is stored it is
// used for any workspace, so that links to an Enterprise Grid org host
// reach the only signed-in workspace. $SLACK_TOKEN and $SLACK_COOKIE,
// when both set, take precedence over the file.
func (s Store) Lookup(workspace string) (Credential, error) {
	getenv := s.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	if t, c := getenv(EnvToken), getenv(EnvCookie); t != "" && c != "" {
		cr := Credential{Workspace: workspace, Token: t, Cookie: c}
		if strings.Contains(workspace, ".") {
			cr.URL = "https://" + workspace + "/"
		} else {
			cr.URL = "https://slack.com/"
		}
		return cr, nil
	}
	all, err := s.All()
	if err != nil {
		return Credential{}, err
	}
	found := func(c Credential) (Credential, error) {
		if c.Token == "" {
			return c, fmt.Errorf("%w to %s: the session is not in the keyring any more; run `slack-tabbed-tui auth %s`", ErrNoSession, c.Workspace, c.URL)
		}
		return c, nil
	}
	for _, c := range all {
		if strings.EqualFold(c.Workspace, workspace) || c.TeamID == workspace {
			return found(c)
		}
	}
	if len(all) == 1 {
		return found(all[0])
	}
	return Credential{}, fmt.Errorf("%w to %s: run `slack-tabbed-tui auth https://%s`", ErrNoSession, workspace, hostHint(workspace))
}

// Canonical returns the workspace host for a team ID when a session for
// that team is stored, so that every link form of a thread names the
// same workspace; anything else is returned as is.
func (s Store) Canonical(workspace string) string {
	if strings.Contains(workspace, ".") {
		return workspace
	}
	all, err := s.load()
	if err != nil {
		return workspace
	}
	for _, c := range all {
		if c.TeamID == workspace && c.Workspace != "" {
			return c.Workspace
		}
	}
	return workspace
}

func hostHint(ws string) string {
	if strings.Contains(ws, ".") {
		return ws
	}
	return "<workspace>.slack.com"
}

// Save stores c, replacing the session for the same team.
func (s Store) Save(c Credential) error {
	if c.Workspace == "" && c.URL != "" {
		if u, err := url.Parse(c.URL); err == nil {
			c.Workspace = strings.ToLower(u.Hostname())
		}
	}
	c.Keyring = false
	if s.Secrets != nil {
		data, _ := json.Marshal(secret{Token: c.Token, Cookie: c.Cookie})
		if s.Secrets.Set(c.key(), string(data)) == nil {
			c.Token, c.Cookie, c.Keyring = "", "", true
		}
		// Otherwise the file keeps them, as without a keyring.
	}
	all, err := s.load()
	if err != nil {
		return err
	}
	replaced := false
	for i, old := range all {
		if old.TeamID == c.TeamID && c.TeamID != "" || strings.EqualFold(old.Workspace, c.Workspace) {
			all[i], replaced = c, true
			break
		}
	}
	if !replaced {
		all = append(all, c)
	}
	return s.write(all)
}

// Remove forgets the session for a workspace (host or team ID). It
// reports whether one was stored.
func (s Store) Remove(workspace string) (bool, error) {
	all, err := s.load()
	if err != nil {
		return false, err
	}
	kept := all[:0]
	for _, c := range all {
		if !strings.EqualFold(c.Workspace, workspace) && c.TeamID != workspace {
			kept = append(kept, c)
		} else if c.Keyring && s.Secrets != nil {
			_ = s.Secrets.Delete(c.key())
		}
	}
	if len(kept) == len(all) {
		return false, nil
	}
	return true, s.write(kept)
}

func (s Store) write(all []Credential) error {
	data, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path)
}
