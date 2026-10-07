// Package directory keeps a workspace's users, user groups and channels,
// cached on disk, to show names instead of IDs and to complete @, # and
// mentions.
package directory

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/clobrano/slack-tabbed-tui/internal/slack"
)

// API is the part of the Slack client the directory needs.
type API interface {
	UserInfo(ctx context.Context, id string) (slack.User, error)
	ConversationInfo(ctx context.Context, id string) (slack.Conversation, error)
	Users(ctx context.Context) ([]slack.User, error)
	UserGroups(ctx context.Context) ([]slack.UserGroup, error)
}

// Directory is safe for concurrent use. It implements slack.Names.
type Directory struct {
	path string

	mu       sync.RWMutex
	users    map[string]slack.User
	groups   map[string]slack.UserGroup
	channels map[string]slack.Conversation
}

type file struct {
	Users    []slack.User         `json:"users"`
	Groups   []slack.UserGroup    `json:"groups"`
	Channels []slack.Conversation `json:"channels"`
}

// Load reads the cache at path; a missing or unreadable cache is an
// empty directory, filled again from Slack.
func Load(path string) *Directory {
	d := &Directory{
		path:     path,
		users:    map[string]slack.User{},
		groups:   map[string]slack.UserGroup{},
		channels: map[string]slack.Conversation{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return d
	}
	var f file
	if json.Unmarshal(data, &f) != nil {
		return d
	}
	for _, u := range f.Users {
		d.users[u.ID] = u
	}
	for _, g := range f.Groups {
		d.groups[g.ID] = g
	}
	for _, c := range f.Channels {
		d.channels[c.ID] = c
	}
	return d
}

// Save writes the cache.
func (d *Directory) Save() error {
	if d.path == "" {
		return nil
	}
	d.mu.RLock()
	var f file
	for _, u := range d.users {
		f.Users = append(f.Users, u)
	}
	for _, g := range d.groups {
		f.Groups = append(f.Groups, g)
	}
	for _, c := range d.channels {
		f.Channels = append(f.Channels, c)
	}
	d.mu.RUnlock()
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return err
	}
	tmp := d.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, d.path)
}

// Sync replaces users and groups with the full lists from Slack. On a
// large Enterprise Grid this takes a while; names already cached keep
// working meanwhile.
func (d *Directory) Sync(ctx context.Context, api API) error {
	users, err := api.Users(ctx)
	if err != nil {
		return err
	}
	groups, err := api.UserGroups(ctx)
	var se *slack.Error
	if err != nil && !(errors.As(err, &se) && se.Code == "missing_scope") {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.users = make(map[string]slack.User, len(users))
	for _, u := range users {
		d.users[u.ID] = u
	}
	if err == nil {
		d.groups = make(map[string]slack.UserGroup, len(groups))
		for _, g := range groups {
			d.groups[g.ID] = g
		}
	}
	return nil
}

// Resolve fetches the users and channels that are not cached yet. It
// keeps going on errors and returns the first one.
func (d *Directory) Resolve(ctx context.Context, api API, userIDs, channelIDs []string) error {
	var first error
	for _, id := range userIDs {
		if d.User(id) != nil {
			continue
		}
		u, err := api.UserInfo(ctx, id)
		if err != nil {
			first = cmpErr(first, err)
			continue
		}
		d.PutUser(u)
	}
	for _, id := range channelIDs {
		d.mu.RLock()
		_, ok := d.channels[id]
		d.mu.RUnlock()
		if ok {
			continue
		}
		c, err := api.ConversationInfo(ctx, id)
		if err != nil {
			first = cmpErr(first, err)
			continue
		}
		d.mu.Lock()
		d.channels[id] = c
		d.mu.Unlock()
		if c.IsIM && c.User != "" {
			if err := d.Resolve(ctx, api, []string{c.User}, nil); err != nil {
				first = cmpErr(first, err)
			}
		}
	}
	return first
}

func cmpErr(first, err error) error {
	if first != nil {
		return first
	}
	return err
}

// PutUser adds or updates a user (e.g. from a user_change event).
func (d *Directory) PutUser(u slack.User) {
	d.mu.Lock()
	d.users[u.ID] = u
	d.mu.Unlock()
}

// User returns a cached user, or nil.
func (d *Directory) User(id string) *slack.User {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if u, ok := d.users[id]; ok {
		return &u
	}
	return nil
}

// UserName is the label Slack shows for a user, or "".
func (d *Directory) UserName(id string) string {
	if u := d.User(id); u != nil {
		return u.Label()
	}
	return ""
}

// GroupHandle is a user group's @handle without the @, or "".
func (d *Directory) GroupHandle(id string) string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.groups[id].Handle
}

// ChannelName is "general" for a channel, the other person's name for a
// DM, or "".
func (d *Directory) ChannelName(id string) string {
	d.mu.RLock()
	c, ok := d.channels[id]
	d.mu.RUnlock()
	switch {
	case !ok:
		return ""
	case c.IsIM:
		if n := d.UserName(c.User); n != "" {
			return n
		}
		return c.User
	default:
		return c.Name
	}
}

// Channel returns a cached conversation, or nil.
func (d *Directory) Channel(id string) *slack.Conversation {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if c, ok := d.channels[id]; ok {
		return &c
	}
	return nil
}

// Author is who a message is from: the user's name, or the bot or app
// name for bot messages.
func (d *Directory) Author(m slack.Message) string {
	if m.User != "" {
		if n := d.UserName(m.User); n != "" {
			return n
		}
	}
	if m.BotProfile != nil && m.BotProfile.Name != "" {
		return m.BotProfile.Name
	}
	if m.Username != "" {
		return m.Username
	}
	if m.User != "" {
		return m.User
	}
	return strings.TrimSpace(m.BotID)
}

// Mentioned returns the user IDs a thread needs names for: authors,
// reactors and users mentioned in the text.
func Mentioned(msgs []slack.Message) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, m := range msgs {
		add(m.User)
		for _, r := range m.Reactions {
			for _, u := range r.Users {
				add(u)
			}
		}
		for s := m.Text; ; {
			i := strings.Index(s, "<@")
			if i < 0 {
				break
			}
			s = s[i+2:]
			end := strings.IndexAny(s, "|>")
			if end < 0 {
				break
			}
			add(s[:end])
		}
	}
	return ids
}
