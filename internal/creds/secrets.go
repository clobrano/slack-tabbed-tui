package creds

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Secrets keeps the token and cookie out of the credentials file.
type Secrets interface {
	Set(key, secret string) error
	Get(key string) (string, error)
	Delete(key string) error
}

// SecretTool stores secrets in the desktop keyring (GNOME Keyring,
// KWallet, KeePassXC, … through the Secret Service) with libsecret's
// secret-tool command.
type SecretTool struct{ Path string }

// NewSecretTool returns the keyring, or nil when secret-tool or a
// D-Bus session is missing (then the credentials file holds the
// secrets).
func NewSecretTool() Secrets {
	if os.Getenv("DBUS_SESSION_BUS_ADDRESS") == "" {
		return nil
	}
	p, err := exec.LookPath("secret-tool")
	if err != nil {
		return nil
	}
	return SecretTool{Path: p}
}

func (s SecretTool) attrs(key string) []string {
	return []string{"application", "slack-tabbed-tui", "workspace", key}
}

func (s SecretTool) run(stdin string, args ...string) (string, error) {
	cmd := exec.Command(s.Path, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("secret-tool %s: %v %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Set stores a secret under key.
func (s SecretTool) Set(key, secret string) error {
	args := append([]string{"store", "--label=slack-tabbed-tui " + key}, s.attrs(key)...)
	_, err := s.run(secret, args...)
	return err
}

// Get returns the secret stored under key.
func (s SecretTool) Get(key string) (string, error) {
	out, err := s.run("", append([]string{"lookup"}, s.attrs(key)...)...)
	if err == nil && out == "" {
		err = fmt.Errorf("no secret for %s in the keyring", key)
	}
	return out, err
}

// Delete removes the secret stored under key.
func (s SecretTool) Delete(key string) error {
	_, err := s.run("", append([]string{"clear"}, s.attrs(key)...)...)
	return err
}
