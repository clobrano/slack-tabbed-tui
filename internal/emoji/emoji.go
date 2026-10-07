// Package emoji knows Slack's standard emoji names (from iamcal's
// emoji-data, the set Slack uses): it renders :name: as the emoji and
// finds names for completion.
package emoji

import (
	"strings"
	"sync"
)

// Emoji is a name and the emoji it stands for.
type Emoji struct {
	Name string
	Char string
}

var (
	once   sync.Once
	byName map[string]string
)

func index() {
	byName = make(map[string]string, len(table))
	for _, e := range table {
		byName[e.Name] = e.Char
	}
}

// Lookup returns the emoji for a standard name ("eyes", "+1"). A skin
// tone suffix ("+1::skin-tone-2") is ignored.
func Lookup(name string) (string, bool) {
	once.Do(index)
	name, _, _ = strings.Cut(name, "::")
	c, ok := byName[name]
	return c, ok
}

// Render replaces every :name: of a standard emoji in text with the
// emoji. Unknown names (custom emoji) are left as they are.
func Render(text string) string {
	if !strings.Contains(text, ":") {
		return text
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(text, ':')
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		j := strings.IndexByte(text[i+1:], ':')
		if j <= 0 {
			b.WriteString(text)
			return b.String()
		}
		name := text[i+1 : i+1+j]
		end := i + 1 + j + 1
		// A skin tone: :+1::skin-tone-2:
		if strings.HasPrefix(text[end:], ":skin-tone-") {
			if k := strings.IndexByte(text[end+1:], ':'); k > 0 {
				end += 1 + k + 1
			}
		}
		if c, ok := Lookup(name); ok && validName(name) {
			b.WriteString(text[:i])
			b.WriteString(c)
			text = text[end:]
			continue
		}
		// Not an emoji: keep the first colon and look again after it.
		b.WriteString(text[:i+1])
		text = text[i+1:]
	}
}

func validName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '+' || r == '\'') {
			return false
		}
	}
	return true
}

// Search returns up to limit standard emoji whose name starts with
// query, then those that contain it, in Slack's order; one entry per
// emoji (the first matching name).
func Search(query string, limit int) []Emoji {
	query = strings.ToLower(query)
	var prefix, inside []Emoji
	seen := map[string]bool{}
	for _, e := range table {
		if seen[e.Char] {
			continue
		}
		switch {
		case strings.HasPrefix(e.Name, query):
			prefix = append(prefix, e)
			seen[e.Char] = true
		case strings.Contains(e.Name, query):
			inside = append(inside, e)
			seen[e.Char] = true
		}
		if len(prefix) >= limit {
			break
		}
	}
	out := append(prefix, inside...)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}
