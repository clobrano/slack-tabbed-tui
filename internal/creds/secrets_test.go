package creds

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type memSecrets struct {
	m    map[string]string
	fail bool
}

func (s *memSecrets) Set(k, v string) error {
	if s.fail {
		return errors.New("locked")
	}
	s.m[k] = v
	return nil
}

func (s *memSecrets) Get(k string) (string, error) {
	v, ok := s.m[k]
	if !ok {
		return "", errors.New("missing")
	}
	return v, nil
}

func (s *memSecrets) Delete(k string) error { delete(s.m, k); return nil }

func TestKeyring(t *testing.T) {
	sec := &memSecrets{m: map[string]string{}}
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json"), Secrets: sec, Getenv: noEnv}
	if err := s.Save(Credential{URL: "https://acme.slack.com/", TeamID: "T1", Token: "xoxc-1", Cookie: "xoxd-1"}); err != nil {
		t.Fatal(err)
	}
	file, _ := os.ReadFile(s.Path)
	if strings.Contains(string(file), "xoxc-1") || strings.Contains(string(file), "xoxd-1") || !strings.Contains(string(file), `"keyring": true`) {
		t.Errorf("secrets leaked to the file:\n%s", file)
	}
	c, err := s.Lookup("acme.slack.com")
	if err != nil || c.Token != "xoxc-1" || c.Cookie != "xoxd-1" {
		t.Fatalf("Lookup = %+v %v", c, err)
	}

	// Secret gone from the keyring: a clear error, not an empty token.
	delete(sec.m, "T1")
	if _, err := s.Lookup("acme.slack.com"); !errors.Is(err, ErrNoSession) || !strings.Contains(err.Error(), "keyring") {
		t.Errorf("missing secret: %v", err)
	}

	// Remove clears the keyring too.
	_ = s.Save(Credential{URL: "https://acme.slack.com/", TeamID: "T1", Token: "xoxc-2", Cookie: "xoxd-2"})
	if ok, err := s.Remove("acme.slack.com"); !ok || err != nil || len(sec.m) != 0 {
		t.Errorf("Remove: %v %v, keyring %v", ok, err, sec.m)
	}
}

func TestKeyringUnavailable(t *testing.T) {
	sec := &memSecrets{m: map[string]string{}, fail: true}
	s := Store{Path: filepath.Join(t.TempDir(), "credentials.json"), Secrets: sec, Getenv: noEnv}
	if err := s.Save(Credential{URL: "https://acme.slack.com/", TeamID: "T1", Token: "xoxc-1", Cookie: "xoxd-1"}); err != nil {
		t.Fatal(err)
	}
	c, err := s.Lookup("T1")
	if err != nil || c.Token != "xoxc-1" || c.Keyring {
		t.Errorf("file fallback: %+v %v", c, err)
	}
}

func TestSecretTool(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	// A stand-in for secret-tool keeping secrets in files named after
	// the last attribute value.
	script := `#!/bin/sh
cmd=$1; shift
for a in "$@"; do last=$a; done
f="` + store + `/$last"
case $cmd in
store) mkdir -p "` + store + `"; cat > "$f";;
lookup) [ -f "$f" ] || exit 1; cat "$f";;
clear) rm -f "$f";;
esac
`
	path := filepath.Join(dir, "secret-tool")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	st := SecretTool{Path: path}
	if err := st.Set("T1", `{"token":"x"}`); err != nil {
		t.Fatal(err)
	}
	if v, err := st.Get("T1"); err != nil || v != `{"token":"x"}` {
		t.Errorf("Get = %q %v", v, err)
	}
	if err := st.Delete("T1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("T1"); err == nil {
		t.Error("Get after Delete succeeded")
	}

	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if NewSecretTool() != nil {
		t.Error("keyring without a D-Bus session")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/x")
	t.Setenv("PATH", dir)
	if got, ok := NewSecretTool().(SecretTool); !ok || got.Path != path {
		t.Errorf("NewSecretTool = %#v", got)
	}
}
