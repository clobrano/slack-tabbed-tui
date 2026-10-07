package creds

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func noEnv(string) string { return "" }

func TestSaveLookupRemove(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "sub", "credentials.json"), Getenv: noEnv}
	if _, err := s.Lookup("acme.slack.com"); !errors.Is(err, ErrNoSession) {
		t.Fatalf("empty store: %v", err)
	}
	acme := Credential{URL: "https://Acme.slack.com/", TeamID: "T1", Token: "xoxc-1", Cookie: "xoxd-1"}
	beta := Credential{URL: "https://beta.slack.com/", TeamID: "T2", Token: "xoxc-2", Cookie: "xoxd-2"}
	for _, c := range []Credential{acme, beta} {
		if err := s.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	fi, err := os.Stat(s.Path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi, err)
	}
	for _, ws := range []string{"acme.slack.com", "ACME.slack.com", "T1"} {
		c, err := s.Lookup(ws)
		if err != nil || c.Token != "xoxc-1" {
			t.Errorf("Lookup(%q) = %+v, %v", ws, c, err)
		}
	}
	if _, err := s.Lookup("gamma.slack.com"); !errors.Is(err, ErrNoSession) {
		t.Errorf("unknown workspace with two sessions: %v", err)
	}

	// A new sign-in to the same team replaces the session.
	acme.Token = "xoxc-1b"
	if err := s.Save(acme); err != nil {
		t.Fatal(err)
	}
	all, _ := s.All()
	if len(all) != 2 || all[0].Token != "xoxc-1b" {
		t.Errorf("after re-save: %+v", all)
	}

	if ok, err := s.Remove("T2"); !ok || err != nil {
		t.Fatalf("Remove: %v %v", ok, err)
	}
	// One session left: it serves any workspace (Enterprise Grid org hosts).
	if c, err := s.Lookup("acme.enterprise.slack.com"); err != nil || c.TeamID != "T1" {
		t.Errorf("single-session fallback: %+v %v", c, err)
	}
	if ok, _ := s.Remove("T2"); ok {
		t.Error("removed twice")
	}
}

func TestEnvOverride(t *testing.T) {
	env := map[string]string{EnvToken: "xoxc-env", EnvCookie: "xoxd-env"}
	s := Store{Path: filepath.Join(t.TempDir(), "none.json"), Getenv: func(k string) string { return env[k] }}
	c, err := s.Lookup("acme.slack.com")
	if err != nil || c.Token != "xoxc-env" || c.Cookie != "xoxd-env" || c.URL != "https://acme.slack.com/" {
		t.Errorf("env session: %+v %v", c, err)
	}
	delete(env, EnvCookie)
	if _, err := s.Lookup("acme.slack.com"); !errors.Is(err, ErrNoSession) {
		t.Errorf("token without cookie should not count: %v", err)
	}
}
