package slack

import "testing"

type fakeNames map[string]string

func (f fakeNames) UserName(id string) string    { return f[id] }
func (f fakeNames) ChannelName(id string) string { return f[id] }
func (f fakeNames) GroupHandle(id string) string { return f[id] }

func TestPlainText(t *testing.T) {
	names := fakeNames{"U1": "alice", "C1": "general", "S1": "oncall"}
	tests := []struct{ in, want string }{
		{"hi <@U1>!", "hi @alice!"},
		{"hi <@U9>", "hi @U9"},
		{"hi <@U9|bob>", "hi @bob"},
		{"see <#C1>", "see #general"},
		{"see <#C2|random>", "see #random"},
		{"<!here> <!channel> <!everyone>", "@here @channel @everyone"},
		{"ping <!subteam^S1>", "ping @oncall"},
		{"ping <!subteam^S2|@devs>", "ping @devs"},
		{"at <!date^1700000000^{date}|Nov 14>", "at Nov 14"},
		{"<https://example.com>", "https://example.com"},
		{"<https://example.com|the docs>", "the docs (https://example.com)"},
		{"<https://example.com?a=1&amp;b=2|x>", "x (https://example.com?a=1&b=2)"},
		{"<mailto:a@b.c|a@b.c>", "mailto:a@b.c"},
		{"1 &lt; 2 &amp;&amp; 3 &gt; 2", "1 < 2 && 3 > 2"},
		{"*bold* _it_ `code`", "*bold* _it_ `code`"},
		{"unclosed < bracket", "unclosed < bracket"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := PlainText(tt.in, names); got != tt.want {
			t.Errorf("PlainText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if got := PlainText("hi <@U1>", nil); got != "hi @U1" {
		t.Errorf("nil names: %q", got)
	}
}

func TestEscape(t *testing.T) {
	if got := Escape("a < b & c > d"); got != "a &lt; b &amp; c &gt; d" {
		t.Errorf("Escape = %q", got)
	}
	if got := PlainText(Escape("x <y> & z"), nil); got != "x <y> & z" {
		t.Errorf("round trip = %q", got)
	}
}
