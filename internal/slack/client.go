package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client calls the Slack Web API as the user of a browser session.
type Client struct {
	HTTP *http.Client
	// BaseURL is the API root, e.g. "https://acme.slack.com/api/". The
	// workspace's own host is used, as the web client does, so that
	// Enterprise Grid sessions reach the right workspace.
	BaseURL string
	Token   string // xoxc-…
	Cookie  string // value of the d cookie (xoxd-…), as stored by the browser
}

// New returns a client for the workspace at workspaceURL
// ("https://acme.slack.com/").
func New(workspaceURL, token, cookie string) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		BaseURL: strings.TrimSuffix(workspaceURL, "/") + "/api/",
		Token:   token,
		Cookie:  cookie,
	}
}

// Error is a failed API call: Slack's "error" code, or an HTTP failure.
type Error struct {
	Method string
	Code   string // e.g. "invalid_auth", "channel_not_found", "ratelimited"
	Status int    // HTTP status when the call failed before Slack answered
	// RetryAfter is set when Slack rate-limited the call.
	RetryAfter time.Duration
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("slack %s: HTTP %d", e.Method, e.Status)
	}
	return fmt.Sprintf("slack %s: %s", e.Method, e.Code)
}

// Auth reports whether the session is no longer valid (logged out,
// expired, revoked), so the user has to sign in again.
func (e *Error) Auth() bool {
	switch e.Code {
	case "invalid_auth", "not_authed", "token_revoked", "token_expired", "account_inactive":
		return true
	}
	return e.Status == http.StatusUnauthorized
}

// RateLimited reports whether Slack rejected the call for its rate limit.
func (e *Error) RateLimited() bool {
	return e.Code == "ratelimited" || e.Status == http.StatusTooManyRequests
}

// Call invokes an API method with form parameters and decodes the
// response into out, which may be nil. A response with "ok": false is
// returned as *Error.
func (c *Client) Call(ctx context.Context, method string, params url.Values, out any) error {
	form := url.Values{}
	for k, v := range params {
		form[k] = v
	}
	form.Set("token", c.Token)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+method, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "slack-tabbed-tui")
	if c.Cookie != "" {
		req.Header.Set("Cookie", "d="+c.Cookie)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusTooManyRequests {
		return &Error{Method: method, Code: "ratelimited", Status: res.StatusCode, RetryAfter: retryAfter(res.Header)}
	}
	if res.StatusCode >= 300 {
		return &Error{Method: method, Status: res.StatusCode}
	}
	var head struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return fmt.Errorf("slack %s: decoding response: %w", method, err)
	}
	if !head.OK {
		if head.Error == "" {
			head.Error = "unknown_error"
		}
		return &Error{Method: method, Code: head.Error, RetryAfter: retryAfter(res.Header)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("slack %s: decoding response: %w", method, err)
	}
	return nil
}

func retryAfter(h http.Header) time.Duration {
	if n, err := strconv.Atoi(h.Get("Retry-After")); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}

type meta struct {
	ResponseMetadata struct {
		NextCursor string `json:"next_cursor"`
	} `json:"response_metadata"`
}

// AuthTest checks the session and says who and where it belongs to.
func (c *Client) AuthTest(ctx context.Context) (AuthInfo, error) {
	var a AuthInfo
	err := c.Call(ctx, "auth.test", nil, &a)
	return a, err
}

// Replies returns a thread: the parent message, then every reply, oldest
// first. With oldest set, only messages after it are returned (the
// parent is always included by Slack).
func (c *Client) Replies(ctx context.Context, channel, threadTS, oldest string) ([]Message, error) {
	var all []Message
	cursor := ""
	for {
		p := url.Values{"channel": {channel}, "ts": {threadTS}, "limit": {"200"}}
		if oldest != "" {
			p.Set("oldest", oldest)
		}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		var res struct {
			meta
			Messages []Message `json:"messages"`
			HasMore  bool      `json:"has_more"`
		}
		if err := c.Call(ctx, "conversations.replies", p, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Messages...)
		if cursor = res.ResponseMetadata.NextCursor; cursor == "" {
			return all, nil
		}
	}
}

// ConversationInfo returns a channel, private channel or DM.
func (c *Client) ConversationInfo(ctx context.Context, channel string) (Conversation, error) {
	var res struct {
		Channel Conversation `json:"channel"`
	}
	err := c.Call(ctx, "conversations.info", url.Values{"channel": {channel}}, &res)
	return res.Channel, err
}

// UserInfo returns one user, bot user included.
func (c *Client) UserInfo(ctx context.Context, id string) (User, error) {
	var res struct {
		User User `json:"user"`
	}
	err := c.Call(ctx, "users.info", url.Values{"user": {id}}, &res)
	return res.User, err
}

// Users returns every member of the workspace: people, bots and app
// users, deactivated ones included (User.Deleted).
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var all []User
	cursor := ""
	for {
		p := url.Values{"limit": {"200"}}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		var res struct {
			meta
			Members []User `json:"members"`
		}
		if err := c.Call(ctx, "users.list", p, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Members...)
		if cursor = res.ResponseMetadata.NextCursor; cursor == "" {
			return all, nil
		}
	}
}

// UserGroups returns the workspace's user groups (@handles for teams).
func (c *Client) UserGroups(ctx context.Context) ([]UserGroup, error) {
	var res struct {
		UserGroups []UserGroup `json:"usergroups"`
	}
	err := c.Call(ctx, "usergroups.list", nil, &res)
	return res.UserGroups, err
}

// PostReply posts text as a reply in a thread. With broadcast, the reply
// is also sent to the channel.
func (c *Client) PostReply(ctx context.Context, channel, threadTS, text string, broadcast bool) (Message, error) {
	p := url.Values{"channel": {channel}, "thread_ts": {threadTS}, "text": {text}}
	if broadcast {
		p.Set("reply_broadcast", "true")
	}
	var res struct {
		Message Message `json:"message"`
	}
	err := c.Call(ctx, "chat.postMessage", p, &res)
	return res.Message, err
}

// ChannelMembers returns the user IDs of a conversation's members, at
// most maxPages pages of 200 (0: all).
func (c *Client) ChannelMembers(ctx context.Context, channel string, maxPages int) ([]string, error) {
	var all []string
	cursor := ""
	for page := 1; ; page++ {
		p := url.Values{"channel": {channel}, "limit": {"200"}}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		var res struct {
			meta
			Members []string `json:"members"`
		}
		if err := c.Call(ctx, "conversations.members", p, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Members...)
		if cursor = res.ResponseMetadata.NextCursor; cursor == "" || maxPages > 0 && page >= maxPages {
			return all, nil
		}
	}
}

// MyChannels returns the public and private channels the user is in.
func (c *Client) MyChannels(ctx context.Context) ([]Conversation, error) {
	var all []Conversation
	cursor := ""
	for {
		p := url.Values{"types": {"public_channel,private_channel"}, "exclude_archived": {"true"}, "limit": {"200"}}
		if cursor != "" {
			p.Set("cursor", cursor)
		}
		var res struct {
			meta
			Channels []Conversation `json:"channels"`
		}
		if err := c.Call(ctx, "users.conversations", p, &res); err != nil {
			return nil, err
		}
		all = append(all, res.Channels...)
		if cursor = res.ResponseMetadata.NextCursor; cursor == "" {
			return all, nil
		}
	}
}

// CustomEmoji returns the workspace's custom emoji names (aliases
// included).
func (c *Client) CustomEmoji(ctx context.Context) ([]string, error) {
	var res struct {
		Emoji map[string]string `json:"emoji"`
	}
	if err := c.Call(ctx, "emoji.list", nil, &res); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(res.Emoji))
	for n := range res.Emoji {
		names = append(names, n)
	}
	return names, nil
}
