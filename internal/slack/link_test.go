package slack

import "testing"

func TestParseRef(t *testing.T) {
	tests := []struct {
		in   string
		want Ref
	}{
		{"https://acme.slack.com/archives/C0123ABC/p1700000000123456",
			Ref{"acme.slack.com", "C0123ABC", "1700000000.123456", "1700000000.123456"}},
		{"  https://acme.slack.com/archives/C0123ABC/p1700000000999999?thread_ts=1700000000.123456&cid=C0123ABC\n",
			Ref{"acme.slack.com", "C0123ABC", "1700000000.123456", "1700000000.999999"}},
		{"https://Acme.Enterprise.slack.com/archives/G0123ABC/p1700000000123456",
			Ref{"acme.enterprise.slack.com", "G0123ABC", "1700000000.123456", "1700000000.123456"}},
		{"https://acme.slack.com/archives/D0123ABC/p1700000000123456/",
			Ref{"acme.slack.com", "D0123ABC", "1700000000.123456", "1700000000.123456"}},
		{"https://app.slack.com/client/T0123ABC/C0123ABC/thread/C0123ABC-1700000000.123456",
			Ref{"T0123ABC", "C0123ABC", "1700000000.123456", "1700000000.123456"}},
		{"thread:acme.slack.com/C0123ABC/1700000000.123456",
			Ref{"acme.slack.com", "C0123ABC", "1700000000.123456", "1700000000.123456"}},
		{"thread:T0123ABC/C0123ABC/1700000000.123456",
			Ref{"T0123ABC", "C0123ABC", "1700000000.123456", "1700000000.123456"}},
	}
	for _, tt := range tests {
		got, err := ParseRef(tt.in)
		if err != nil {
			t.Errorf("ParseRef(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestParseRefRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"hello",
		"https://example.com/archives/C0123ABC/p1700000000123456",
		"https://evil.slack.com.example.com/archives/C0123ABC/p1700000000123456",
		"https://acme.slack.com/archives/C0123ABC",
		"https://acme.slack.com/archives/C0123ABC/p17000000001234",
		"https://acme.slack.com/archives/x0123/p1700000000123456",
		"https://acme.slack.com/archives/C0123ABC/p1700000000123456?thread_ts=bad",
		"https://app.slack.com/client/T0123ABC/C0123ABC",
		"https://app.slack.com/client/T0123ABC/C0123ABC/thread/C9999999-1700000000.123456",
		"thread:acme.slack.com/C0123ABC",
		"thread:example.com/C0123ABC/1700000000.123456",
		"ftp://acme.slack.com/archives/C0123ABC/p1700000000123456",
	} {
		if r, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) = %+v, want error", in, r)
		}
	}
}

func TestRefIDRoundTrip(t *testing.T) {
	for _, in := range []string{
		"https://acme.slack.com/archives/C0123ABC/p1700000000999999?thread_ts=1700000000.123456",
		"https://app.slack.com/client/T0123ABC/C0123ABC/thread/C0123ABC-1700000000.123456",
	} {
		r, err := ParseRef(in)
		if err != nil {
			t.Fatal(err)
		}
		back, err := ParseRef(r.ID())
		if err != nil || back.ID() != r.ID() {
			t.Errorf("ID %q does not round-trip: %+v, %v", r.ID(), back, err)
		}
		link, err := ParseRef(r.Permalink())
		if err != nil || link.ID() != r.ID() {
			t.Errorf("Permalink %q does not round-trip: %+v, %v", r.Permalink(), link, err)
		}
	}
}
