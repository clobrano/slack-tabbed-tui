package slack

import "strings"

// Names resolves IDs found in message text to display names. Each
// method returns "" when the ID is unknown.
type Names interface {
	UserName(id string) string
	ChannelName(id string) string
	GroupHandle(id string) string
}

// PlainText renders Slack's message markup as text a person reads:
// "<@U1>" becomes "@Alice", "<#C1|general>" "#general", "<!here>" "@here",
// "<https://x|label>" "label (https://x)", and &amp; &lt; &gt; are
// unescaped. Formatting marks (*bold*, _italic_, `code`) are kept.
func PlainText(s string, names Names) string {
	if names == nil {
		names = noNames{}
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '<')
		if i < 0 {
			b.WriteString(unescape(s))
			return b.String()
		}
		j := strings.IndexByte(s[i:], '>')
		if j < 0 {
			b.WriteString(unescape(s))
			return b.String()
		}
		b.WriteString(unescape(s[:i]))
		b.WriteString(token(s[i+1:i+j], names))
		s = s[i+j+1:]
	}
}

func token(t string, names Names) string {
	body, label, hasLabel := strings.Cut(t, "|")
	label = unescape(label)
	switch {
	case strings.HasPrefix(body, "@"):
		id := body[1:]
		if n := names.UserName(id); n != "" {
			return "@" + n
		}
		if hasLabel {
			return "@" + strings.TrimPrefix(label, "@")
		}
		return "@" + id
	case strings.HasPrefix(body, "#"):
		id := body[1:]
		if hasLabel && label != "" {
			return "#" + label
		}
		if n := names.ChannelName(id); n != "" {
			return "#" + n
		}
		return "#" + id
	case strings.HasPrefix(body, "!subteam^"):
		id := strings.TrimPrefix(body, "!subteam^")
		if hasLabel && label != "" {
			return "@" + strings.TrimPrefix(label, "@")
		}
		if n := names.GroupHandle(id); n != "" {
			return "@" + n
		}
		return "@" + id
	case strings.HasPrefix(body, "!"):
		cmd, _, _ := strings.Cut(body[1:], "^")
		switch cmd {
		case "here", "channel", "everyone":
			return "@" + cmd
		}
		// !date^…|fallback and anything newer: show the fallback.
		if hasLabel {
			return label
		}
		return cmd
	}
	url := unescape(body)
	if !hasLabel || label == "" || label == url || "mailto:"+label == url {
		return url
	}
	return label + " (" + url + ")"
}

type noNames struct{}

func (noNames) UserName(string) string    { return "" }
func (noNames) ChannelName(string) string { return "" }
func (noNames) GroupHandle(string) string { return "" }

var unescaper = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")

func unescape(s string) string { return unescaper.Replace(s) }

// Escape prepares user-typed text for sending: Slack requires &, < and >
// to be escaped in message text.
func Escape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
