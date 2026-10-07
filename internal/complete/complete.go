// Package complete finds what @, # and : complete to in the reply box,
// ranked the way Slack ranks them: people in the thread first, then the
// channel's members, then everyone else; bots, apps and user groups
// included.
package complete

import (
	"sort"
	"strings"
	"unicode"

	"github.com/clobrano/slack-tabbed-tui/internal/emoji"
	"github.com/clobrano/slack-tabbed-tui/internal/model"
	"github.com/clobrano/slack-tabbed-tui/internal/slack"
)

// Directory is where names come from.
type Directory interface {
	Users() []slack.User
	Groups() []slack.UserGroup
	Channels() []slack.Conversation
	CustomEmoji() []string
}

// Where is the conversation being written in.
type Where struct {
	// Participants are the thread's authors, most recent first.
	Participants []string
	// Members are the channel's members.
	Members []string
	// DM is true in a direct message, where @here and @channel mean
	// nothing.
	DM bool
	// Me is the signed-in user, never proposed.
	Me string
}

// Limit is how many candidates are returned at most.
const Limit = 8

// Tiers, best first.
const (
	tierParticipant = iota
	tierMember
	tierGroup
	tierSpecial
	tierOther
)

type scored struct {
	c     model.Candidate
	tier  int
	score int // lower is better
	order int // tie-break: participant recency, then name
	name  string
}

// People completes "@query": users, bots, app users, user groups and
// @here / @channel / @everyone.
func People(dir Directory, w Where, query string) []model.Candidate {
	q := fold(query)
	rank := map[string]int{}
	for i, id := range w.Participants {
		if _, ok := rank[id]; !ok {
			rank[id] = i
		}
	}
	member := map[string]bool{}
	for _, id := range w.Members {
		member[id] = true
	}
	var hits []scored
	for _, u := range dir.Users() {
		if u.Deleted || u.ID == w.Me || u.ID == "USLACKBOT" && q == "" {
			continue
		}
		score, ok := match(q, u.Profile.DisplayName, u.Profile.RealName, u.Name)
		if !ok {
			continue
		}
		c := model.Candidate{Kind: "user", Label: "@" + u.Label(), Token: "<@" + u.ID + ">", Detail: detail(u)}
		if u.IsBot || u.IsAppUser {
			c.Kind = "bot"
		}
		s := scored{c: c, tier: tierOther, score: score, name: fold(u.Label())}
		if r, ok := rank[u.ID]; ok {
			s.tier, s.order = tierParticipant, r
		} else if member[u.ID] {
			s.tier = tierMember
		}
		hits = append(hits, s)
	}
	for _, g := range dir.Groups() {
		score, ok := match(q, g.Handle, g.Name)
		if !ok {
			continue
		}
		hits = append(hits, scored{
			c:    model.Candidate{Kind: "group", Label: "@" + g.Handle, Token: "<!subteam^" + g.ID + ">", Detail: g.Name},
			tier: tierGroup, score: score, name: fold(g.Handle),
		})
	}
	if !w.DM {
		for i, sp := range []struct{ name, detail string }{
			{"here", "notify every active member of the channel"},
			{"channel", "notify every member of the channel"},
			{"everyone", "notify every member of the workspace"},
		} {
			if strings.HasPrefix(sp.name, q) {
				hits = append(hits, scored{
					c:    model.Candidate{Kind: "special", Label: "@" + sp.name, Token: "<!" + sp.name + ">", Detail: sp.detail},
					tier: tierSpecial, order: i,
				})
			}
		}
	}
	return best(hits)
}

func detail(u slack.User) string {
	var parts []string
	if u.Profile.RealName != "" && u.Profile.RealName != u.Label() {
		parts = append(parts, u.Profile.RealName)
	}
	if u.Name != "" && u.Name != u.Label() {
		parts = append(parts, "@"+u.Name)
	}
	if u.IsBot || u.IsAppUser {
		parts = append(parts, "app")
	}
	return strings.Join(parts, " · ")
}

// Channels completes "#query" with the channels the user knows.
func Channels(dir Directory, query string) []model.Candidate {
	q := fold(query)
	var hits []scored
	for _, c := range dir.Channels() {
		score, ok := match(q, c.Name)
		if !ok {
			continue
		}
		d := ""
		if c.IsPrivate {
			d = "private"
		}
		tier := tierOther
		if c.IsMember {
			tier = tierMember
		}
		hits = append(hits, scored{
			c:    model.Candidate{Kind: "channel", Label: "#" + c.Name, Token: "<#" + c.ID + ">", Detail: d},
			tier: tier, score: score, name: c.Name,
		})
	}
	return best(hits)
}

// Emoji completes ":query": standard emoji, then custom ones.
func Emoji(dir Directory, query string) []model.Candidate {
	q := strings.ToLower(query)
	var out []model.Candidate
	for _, e := range emoji.Search(q, Limit) {
		out = append(out, model.Candidate{Kind: "emoji", Label: ":" + e.Name + ":", Token: ":" + e.Name + ":", Emoji: e.Char})
	}
	var custom []string
	for _, n := range dir.CustomEmoji() {
		if strings.Contains(n, q) {
			custom = append(custom, n)
		}
	}
	sort.Slice(custom, func(i, j int) bool {
		pi, pj := strings.HasPrefix(custom[i], q), strings.HasPrefix(custom[j], q)
		if pi != pj {
			return pi
		}
		return custom[i] < custom[j]
	})
	for _, n := range custom {
		out = append(out, model.Candidate{Kind: "emoji", Label: ":" + n + ":", Token: ":" + n + ":", Detail: "custom"})
	}
	// Prefix matches of either kind first.
	sort.SliceStable(out, func(i, j int) bool {
		return strings.HasPrefix(out[i].Label, ":"+q) && !strings.HasPrefix(out[j].Label, ":"+q)
	})
	if len(out) > Limit {
		out = out[:Limit]
	}
	return out
}

func best(hits []scored) []model.Candidate {
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.score != b.score && (a.score == 0) != (b.score == 0) {
			return a.score == 0 // a name starting with the query beats any tier
		}
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.score != b.score {
			return a.score < b.score
		}
		if a.order != b.order {
			return a.order < b.order
		}
		return a.name < b.name
	})
	out := make([]model.Candidate, 0, min(len(hits), Limit))
	for i := 0; i < len(hits) && i < Limit; i++ {
		out = append(out, hits[i].c)
	}
	return out
}

// match reports whether q matches one of the names, and how well: 0 when
// a name starts with q, 1 when a later word of a name does (as in Slack,
// not in the middle of a word). An empty q matches everything.
func match(q string, names ...string) (int, bool) {
	if q == "" {
		return 0, true
	}
	bestScore := -1
	for _, n := range names {
		if n == "" {
			continue
		}
		f := fold(n)
		s := -1
		switch {
		case strings.HasPrefix(f, q):
			s = 0
		case wordPrefix(f, q):
			s = 1
		}
		if s >= 0 && (bestScore < 0 || s < bestScore) {
			bestScore = s
		}
	}
	return bestScore, bestScore >= 0
}

func wordPrefix(s, q string) bool {
	for i, r := range s {
		if i > 0 && (r == ' ' || r == '.' || r == '-' || r == '_') && strings.HasPrefix(s[i+1:], q) {
			return true
		}
	}
	return false
}

// fold lowercases s and drops accents from common Latin letters, so that
// "jose" finds "José".
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if f, ok := accents[r]; ok {
			b.WriteRune(f)
		} else if !unicode.Is(unicode.Mn, r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var accents = func() map[rune]rune {
	m := map[rune]rune{}
	for base, list := range map[rune]string{
		'a': "àáâãäåāăą", 'c': "çćĉċč", 'd': "ďđ", 'e': "èéêëēĕėęě", 'g': "ĝğġģ",
		'i': "ìíîïĩīĭįı", 'l': "ĺļľŀł", 'n': "ñńņňŉ", 'o': "òóôõöøōŏő", 'r': "ŕŗř",
		's': "śŝşšș", 't': "ţťŧț", 'u': "ùúûüũūŭůűų", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range list {
			m[r] = base
		}
	}
	return m
}()
