// Package config resolves slack-tabbed-tui's XDG paths.
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const app = "slack-tabbed-tui"

// Paths are the files and directories slack-tabbed-tui uses.
type Paths struct {
	ConfigDir  string // $XDG_CONFIG_HOME/slack-tabbed-tui
	StateDir   string // $XDG_STATE_HOME/slack-tabbed-tui
	CacheDir   string // $XDG_CACHE_HOME/slack-tabbed-tui
	RuntimeDir string // $XDG_RUNTIME_DIR/slack-tabbed-tui
}

// Watchlist is the path of the watchlist file.
func (p Paths) Watchlist() string { return filepath.Join(p.ConfigDir, "watch") }

// ConfigFile is the path of config.toml.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.toml") }

// Credentials is the session file used when no keyring is available.
func (p Paths) Credentials() string { return filepath.Join(p.ConfigDir, "credentials.json") }

// Snapshot is the persisted state, shared with other tools.
func (p Paths) Snapshot() string { return filepath.Join(p.StateDir, "snapshot.json") }

// Log is where an auto-started daemon writes its output.
func (p Paths) Log() string { return filepath.Join(p.StateDir, "daemon.log") }

// Browser is the profile of the browser used to sign in.
func (p Paths) Browser() string { return filepath.Join(p.StateDir, "browser") }

// Socket is the daemon's Unix socket.
func (p Paths) Socket() string { return filepath.Join(p.RuntimeDir, "daemon.sock") }

// Lock is the single-instance lock file.
func (p Paths) Lock() string { return filepath.Join(p.RuntimeDir, "daemon.lock") }

// Directory is the cache of a workspace's users, groups and channels.
func (p Paths) Directory(teamID string) string {
	return filepath.Join(p.CacheDir, teamID, "directory.json")
}

// DefaultPaths resolves paths from the XDG environment variables.
func DefaultPaths() Paths {
	home, _ := os.UserHomeDir()
	xdg := func(env, fallback string) string {
		if v := os.Getenv(env); v != "" && filepath.IsAbs(v) {
			return v
		}
		return fallback
	}
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" || !filepath.IsAbs(runtime) {
		runtime = filepath.Join(os.TempDir(), fmt.Sprintf("%s-%d", app, os.Getuid()))
	}
	return Paths{
		ConfigDir:  filepath.Join(xdg("XDG_CONFIG_HOME", filepath.Join(home, ".config")), app),
		StateDir:   filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), app),
		CacheDir:   filepath.Join(xdg("XDG_CACHE_HOME", filepath.Join(home, ".cache")), app),
		RuntimeDir: filepath.Join(runtime, app),
	}
}
