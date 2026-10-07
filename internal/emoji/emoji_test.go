package emoji

import "testing"

func TestLookupAndRender(t *testing.T) {
	for name, want := range map[string]string{"eyes": "👀", "+1": "👍", "thumbsup": "👍", "+1::skin-tone-3": "👍", "tada": "🎉"} {
		if got, ok := Lookup(name); !ok || got != want {
			t.Errorf("Lookup(%q) = %q, %v", name, got, ok)
		}
	}
	if _, ok := Lookup("our-custom-emoji"); ok {
		t.Error("custom emoji found")
	}
	tests := map[string]string{
		"ship it :rocket: :tada:":        "ship it 🚀 🎉",
		"nice :+1::skin-tone-2: thanks":  "nice 👍 thanks",
		"custom :partyparrot: stays":     "custom :partyparrot: stays",
		"time 10:30:45 and a: b":         "time 10:30:45 and a: b",
		"url https://x.example:8080/a:b": "url https://x.example:8080/a:b",
		":eyes::eyes:":                   "👀👀",
		"no colons":                      "no colons",
		"trailing :":                     "trailing :",
	}
	for in, want := range tests {
		if got := Render(in); got != want {
			t.Errorf("Render(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearch(t *testing.T) {
	got := Search("thumbs", 5)
	if len(got) < 2 || got[0].Char != "👍" || got[1].Char != "👎" {
		t.Errorf("Search(thumbs) = %v", got)
	}
	// Prefix matches first, one entry per emoji.
	got = Search("rocket", 10)
	if len(got) == 0 || got[0].Name != "rocket" {
		t.Errorf("Search(rocket) = %v", got)
	}
	seen := map[string]bool{}
	for _, e := range Search("smile", 50) {
		if seen[e.Char] {
			t.Errorf("duplicate %v", e)
		}
		seen[e.Char] = true
	}
	if n := len(Search("a", 8)); n != 8 {
		t.Errorf("limit: %d", n)
	}
}
