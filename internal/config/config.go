package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Ensure creates the directories, private to the user.
func (p Paths) Ensure() error {
	for _, d := range []string{p.ConfigDir, p.StateDir, p.CacheDir, p.RuntimeDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return os.Chmod(p.RuntimeDir, 0o700)
}

// DefaultStatusTemplate renders e.g. "💬 ●4 @1": unread replies and
// mentions; "!" when the data is stale.
const DefaultStatusTemplate = `{{if or .Unread .Mentions}}💬{{if .Unread}} ●{{.Unread}}{{end}}{{if .Mentions}} @{{.Mentions}}{{end}}{{end}}{{if .Stale}}!{{end}}`

// Config is the content of config.toml.
type Config struct {
	// Interval is how often threads are fetched when live updates are
	// not available for them (and the safety-net resync otherwise).
	Interval time.Duration
	// Browser command used to open links; empty means $BROWSER, then xdg-open.
	Browser string
	// StatusTemplate is a text/template for `slack-tabbed-tui status`.
	StatusTemplate string
	// Notifier selects the notification backend: "desktop", "exec" or "none".
	Notifier string
	// NotifyCommand is run for the "exec" notifier, with the event as JSON
	// on stdin (e.g. for ntfy.sh or a chat webhook).
	NotifyCommand string
	// AutoStart lets clients spawn the daemon when it is not running.
	AutoStart bool
	// Icons is the icon set: "fancy" (Unicode symbols, the default) or
	// "safe" (characters every common monospace font has).
	Icons string
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Interval:       60 * time.Second,
		StatusTemplate: DefaultStatusTemplate,
		Notifier:       "desktop",
		AutoStart:      true,
		Icons:          "fancy",
	}
}

// Load reads config.toml; a missing file yields the defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	kv, err := parseTOML(f)
	if err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	for k, v := range kv {
		var err error
		switch k {
		case "interval", "poll.interval":
			switch x := v.(type) {
			case int64:
				cfg.Interval = time.Duration(x) * time.Second
			case string:
				cfg.Interval, err = time.ParseDuration(x)
			default:
				err = fmt.Errorf("want seconds or a duration string")
			}
			if err == nil && cfg.Interval < 10*time.Second {
				err = fmt.Errorf("must be at least 10s")
			}
		case "browser":
			cfg.Browser, err = asString(v)
		case "status_template", "status.template":
			cfg.StatusTemplate, err = asString(v)
		case "notifier", "notify.backend":
			cfg.Notifier, err = asString(v)
		case "notify_command", "notify.command":
			cfg.NotifyCommand, err = asString(v)
		case "icons", "ui.icons":
			cfg.Icons, err = asString(v)
			if err == nil && cfg.Icons != "fancy" && cfg.Icons != "safe" {
				err = fmt.Errorf("want \"fancy\" or \"safe\"")
			}
		case "autostart", "daemon.autostart":
			b, ok := v.(bool)
			if !ok {
				err = fmt.Errorf("want true or false")
			}
			cfg.AutoStart = b
		default:
			err = fmt.Errorf("unknown key")
		}
		if err != nil {
			return cfg, fmt.Errorf("%s: %s: %w", path, k, err)
		}
	}
	return cfg, nil
}

func asString(v any) (string, error) {
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("want a string")
	}
	return s, nil
}

// parseTOML reads the subset of TOML slack-tabbed-tui needs: comments, [tables]
// (flattened as "table.key"), and string, integer and boolean values.
func parseTOML(f *os.File) (map[string]any, error) {
	out := map[string]any{}
	table := ""
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			table = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n)
		}
		key := strings.TrimSpace(k)
		if table != "" {
			key = table + "." + key
		}
		val, err := parseValue(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out[key] = val
	}
	return out, sc.Err()
}

func stripComment(s string) string {
	inStr := byte(0)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inStr != 0 && c == '\\' && inStr == '"':
			i++
		case inStr != 0 && c == inStr:
			inStr = 0
		case inStr == 0 && (c == '"' || c == '\''):
			inStr = c
		case inStr == 0 && c == '#':
			return s[:i]
		}
	}
	return s
}

func parseValue(s string) (any, error) {
	switch {
	case s == "true":
		return true, nil
	case s == "false":
		return false, nil
	case strings.HasPrefix(s, `"`):
		return strconv.Unquote(s)
	case strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`) && len(s) >= 2:
		return s[1 : len(s)-1], nil
	}
	i, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("unsupported value %q", s)
	}
	return i, nil
}
