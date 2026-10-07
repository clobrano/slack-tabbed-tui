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
	Token        string `json:"token"`  // xoxc-…
	Cookie       string `json:"cookie"` // the d cookie, xoxd-…
}

// Environment variables that override the stored session.
const (
	EnvToken  = "SLACK_TOKEN"
	EnvCookie = "SLACK_COOKIE"
)

// ErrNoSession means there is no session for the workspace.
var ErrNoSession = errors.New("not signed in")

// Store keeps the sessions in a JSON file with mode 0600.
type Store struct {
	Path string
	// Getenv reads the environment; nil means os.Getenv.
	Getenv func(string) string
}

// All returns the stored sessions.
func (s Store) All() ([]Credential, error) {
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
	for _, c := range all {
		if strings.EqualFold(c.Workspace, workspace) || c.TeamID == workspace {
			return c, nil
		}
	}
	if len(all) == 1 {
		return all[0], nil
	}
	return Credential{}, fmt.Errorf("%w to %s: run `slack-tabbed-tui auth https://%s`", ErrNoSession, workspace, hostHint(workspace))
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
	all, err := s.All()
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
	all, err := s.All()
	if err != nil {
		return false, err
	}
	kept := all[:0]
	for _, c := range all {
		if !strings.EqualFold(c.Workspace, workspace) && c.TeamID != workspace {
			kept = append(kept, c)
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
