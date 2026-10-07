package slack

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// AuthInfo is the answer of auth.test.
type AuthInfo struct {
	URL          string `json:"url"` // "https://acme.slack.com/"
	Team         string `json:"team"`
	TeamID       string `json:"team_id"`
	User         string `json:"user"`
	UserID       string `json:"user_id"`
	EnterpriseID string `json:"enterprise_id,omitempty"`
}

// Message is one message of a thread.
type Message struct {
	Type       string      `json:"type"`
	Subtype    string      `json:"subtype,omitempty"`
	TS         string      `json:"ts"`
	ThreadTS   string      `json:"thread_ts,omitempty"`
	User       string      `json:"user,omitempty"`
	BotID      string      `json:"bot_id,omitempty"`
	Username   string      `json:"username,omitempty"` // set by some bots
	BotProfile *BotProfile `json:"bot_profile,omitempty"`
	Text       string      `json:"text"`
	Edited     *struct {
		User string `json:"user"`
		TS   string `json:"ts"`
	} `json:"edited,omitempty"`
	ReplyCount  int             `json:"reply_count,omitempty"`
	Reactions   []Reaction      `json:"reactions,omitempty"`
	Files       []File          `json:"files,omitempty"`
	Attachments []Attachment    `json:"attachments,omitempty"`
	Blocks      json.RawMessage `json:"blocks,omitempty"`
}

// Time is when the message was posted.
func (m Message) Time() time.Time { return TSTime(m.TS) }

// IsBot reports whether a bot or app posted the message.
func (m Message) IsBot() bool { return m.BotID != "" || m.Subtype == "bot_message" }

// BotProfile names the app behind a bot message.
type BotProfile struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	AppID string `json:"app_id"`
}

// Reaction is one emoji on a message, with who used it.
type Reaction struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Users []string `json:"users"`
}

// File is an attachment uploaded to a message.
type File struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	Mimetype   string `json:"mimetype"`
	Size       int64  `json:"size"`
	URLPrivate string `json:"url_private"`
	Permalink  string `json:"permalink"`
}

// Attachment is a legacy message attachment (bots and unfurls).
type Attachment struct {
	Fallback  string `json:"fallback"`
	Pretext   string `json:"pretext"`
	Title     string `json:"title"`
	TitleLink string `json:"title_link"`
	Text      string `json:"text"`
	FromURL   string `json:"from_url"`
}

// Conversation is a channel, a private channel, a DM or a group DM.
type Conversation struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsChannel bool   `json:"is_channel"`
	IsGroup   bool   `json:"is_group"`
	IsIM      bool   `json:"is_im"`
	IsMPIM    bool   `json:"is_mpim"`
	IsPrivate bool   `json:"is_private"`
	IsMember  bool   `json:"is_member"`
	User      string `json:"user,omitempty"` // the other user of a DM
}

// User is a member of the workspace: a person, a bot user or an app user.
type User struct {
	ID        string `json:"id"`
	TeamID    string `json:"team_id"`
	Name      string `json:"name"` // the legacy handle
	Deleted   bool   `json:"deleted"`
	IsBot     bool   `json:"is_bot"`
	IsAppUser bool   `json:"is_app_user"`
	Profile   struct {
		DisplayName string `json:"display_name"`
		RealName    string `json:"real_name"`
	} `json:"profile"`
}

// Label is how Slack shows the user: display name, else real name, else
// handle.
func (u User) Label() string {
	for _, s := range []string{u.Profile.DisplayName, u.Profile.RealName, u.Name} {
		if s != "" {
			return s
		}
	}
	return u.ID
}

// UserGroup is a mentionable group of users (@team).
type UserGroup struct {
	ID     string `json:"id"`
	Handle string `json:"handle"`
	Name   string `json:"name"`
}

// TSTime converts a Slack timestamp ("1700000000.123456") to a time.
func TSTime(ts string) time.Time {
	sec, frac, _ := strings.Cut(ts, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil {
		return time.Time{}
	}
	us, _ := strconv.ParseInt((frac + "000000")[:6], 10, 64)
	return time.Unix(s, us*1000)
}
