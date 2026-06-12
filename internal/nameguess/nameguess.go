// Package nameguess suggests the nearest valid name for a mistyped field or
// operation. It exists because the toolchain's primary consumers are agents:
// an error that names the rejected input but not the intended one costs a
// full guess-and-retry round trip per call.
package nameguess

import (
	"slices"
	"sort"
	"strings"
)

// synonymClasses groups verbs that plugins use interchangeably across the
// ecosystem (issue.show vs page.get). Tokens in one class count as shared
// when scoring candidates.
var synonymClasses = [][]string{
	{"get", "show", "fetch"},
	{"list", "ls"},
	{"delete", "remove", "rm"},
	{"update", "edit"},
	{"search", "find"},
}

// Synonyms returns the other members of token's verb-synonym class, or nil
// when the token has none.
func Synonyms(token string) []string {
	token = strings.ToLower(strings.TrimSpace(token))
	class := synonymClass(token)
	if class < 0 {
		return nil
	}
	out := make([]string, 0, len(synonymClasses[class])-1)
	for _, member := range synonymClasses[class] {
		if member != token {
			out = append(out, member)
		}
	}
	return out
}

func synonymClass(token string) int {
	for i, class := range synonymClasses {
		if slices.Contains(class, token) {
			return i
		}
	}
	return -1
}

// confidentScore is the likeness floor for a single "did you mean" suggestion;
// a wrong confident guess is worse than none.
const confidentScore = 16

// Nearest returns the candidate most plausibly meant by got, or "" when no
// candidate is close enough to suggest with confidence.
func Nearest(got string, candidates []string) string {
	matches := rank(got, candidates, 1, confidentScore)
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// CloseMatches returns up to max candidates ranked by likeness to got:
// per-token alignment with verb synonyms (issue_key→key, issue.get→issue.show)
// plus typo tolerance. Looser than Nearest — meant for "close matches: …"
// lists, not single suggestions. Ties break lexicographically so output is
// deterministic.
func CloseMatches(got string, candidates []string, max int) []string {
	return rank(got, candidates, max, 1)
}

func rank(got string, candidates []string, max, minScore int) []string {
	got = strings.ToLower(strings.TrimSpace(got))
	if got == "" || len(candidates) == 0 || max <= 0 {
		return nil
	}
	type scored struct {
		name  string
		score int
	}
	var ranked []scored
	for _, candidate := range candidates {
		name := strings.TrimSpace(candidate)
		if name == "" || strings.EqualFold(name, got) {
			continue
		}
		if s := likeness(got, strings.ToLower(name)); s >= minScore {
			ranked = append(ranked, scored{name: name, score: s})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].name < ranked[j].name
	})
	out := make([]string, 0, max)
	for _, m := range ranked {
		out = append(out, m.name)
		if len(out) == max {
			break
		}
	}
	return out
}

// likeness scores how plausibly candidate was meant when got was typed.
// 0 means "not close enough to suggest". Tokens are aligned greedily: exact
// match 20, verb synonym 16, per-token typo 12; every unmatched token on
// either side costs 4 — so coverage of BOTH names matters (`key` ↔ `issue_key`
// scores high; `issue` matching one token of a five-token op scores low) and
// a swapped final verb (issue.get → issue.show) beats a raw-edit-distance
// near-miss (issue.get → issue.list).
func likeness(got, candidate string) int {
	gotTokens := tokens(got)
	candTokens := tokens(candidate)
	used := make([]bool, len(candTokens))
	score, shared := 0, 0
	for _, gt := range gotTokens {
		best, bestAt := 0, -1
		for i, ct := range candTokens {
			if used[i] {
				continue
			}
			pts := 0
			switch {
			case gt == ct:
				pts = 20
			case synonymClass(gt) >= 0 && synonymClass(gt) == synonymClass(ct):
				pts = 16
			default:
				if d := editDistance(gt, ct); d <= tokenTypoBound(gt, ct) {
					pts = 18 - d*2 // d=1 ties the synonym score; sloppier typos rank lower
				}
			}
			if pts > best {
				best, bestAt = pts, i
			}
		}
		if bestAt >= 0 {
			used[bestAt] = true
			shared++
			score += best
		}
	}
	if shared == 0 {
		// Separator-free typo of the whole name (lokitest → loki.test).
		if d := editDistance(got, candidate); d <= tokenTypoBound(got, candidate) {
			return 90 - d
		}
		return 0
	}
	score -= (len(gotTokens) - shared) * 4
	score -= (len(candTokens) - shared) * 4
	if score <= 0 {
		return 0
	}
	return score
}

func tokens(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '.' || r == '-'
	})
}

// tokenTypoBound scales the acceptable typo distance with length: short
// tokens tolerate 1 edit, medium 2, long 3.
func tokenTypoBound(a, b string) int {
	switch n := min(len(a), len(b)); {
	case n <= 4:
		return 1
	case n <= 8:
		return 2
	default:
		return 3
	}
}

// editDistance is optimal-string-alignment distance: Levenshtein plus
// adjacent transposition, so the common shwo→show slip counts as one edit.
func editDistance(a, b string) int {
	if a == b {
		return 0
	}
	rows := make([][]int, len(a)+1)
	for i := range rows {
		rows[i] = make([]int, len(b)+1)
		rows[i][0] = i
	}
	for j := 0; j <= len(b); j++ {
		rows[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d := min(rows[i-1][j]+1, rows[i][j-1]+1, rows[i-1][j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d = min(d, rows[i-2][j-2]+1)
			}
			rows[i][j] = d
		}
	}
	return rows[len(a)][len(b)]
}
