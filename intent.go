package fluxplaneplugin

import (
	"context"
	"sort"
	"strings"

	dex "github.com/fluxplane/fluxplane-dex"
)

// intentIndex maps tokenized keywords from dex manifests to weighted
// activation-set hints. It is built once at startup from each plugin's
// manifest (operation names + descriptions + aliases) and consulted by
// intentDeriver on every channel-message observation to decide which
// activation sets to enable.
type intentIndex struct {
	// keyword -> activation-set name -> accumulated weight
	keywords map[string]map[string]float64
}

// intentThreshold is the score above which an activation set is considered
// matched by an observation's tokens. Tuned so a single plugin keyword
// (weight 1.0) is enough; a single description token (weight 0.25) plus a
// single generic verb (weight 0.5) is not.
const intentThreshold = 1.0

// pluginKeywordWeight is added to every activation set that contains a token
// derived from the plugin name or an op-name segment.
const pluginKeywordWeight = 1.0

// englishStopWords is a small English stop-word list used to prune
// description tokens. Intentionally minimal — we want to keep nouns and
// domain words; only drop pure connectives.
var englishStopWords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "of": {}, "to": {},
	"for": {}, "with": {}, "in": {}, "on": {}, "at": {}, "by": {}, "as": {},
	"is": {}, "are": {}, "be": {}, "this": {}, "that": {}, "from": {},
	"into": {}, "through": {}, "via": {}, "across": {}, "over": {},
	"per": {}, "any": {}, "all": {}, "one": {}, "two": {}, "each": {},
}

// buildIntentIndex constructs the keyword index from the engine's
// marketplace. Plugins whose manifest can't be fetched are skipped silently
// — they will simply not contribute intent hints (the adapter remains
// callable via explicit surface_prepare even without the deriver).
func buildIntentIndex(ctx context.Context, engine *dex.Engine) *intentIndex {
	idx := &intentIndex{keywords: map[string]map[string]float64{}}
	if engine == nil {
		return idx
	}
	for _, entry := range engine.Marketplace().Plugins() {
		manifest, err := engine.Manifest(ctx, entry.Name)
		if err != nil {
			continue
		}
		// Only the plugin name and its manifest aliases seed the index.
		// Op-name segments (e.g. "mr", "file", "issue") are NOT indexed
		// because they are common engineering vocabulary that would cause
		// innocuous user messages ("list available file system tools") to
		// activate unrelated surfaces (slack_file, system, ...). The
		// aggregate plugin set covers every op of that plugin anyway, so
		// firing the plugin set is sufficient — no need for per-entity
		// auto-activation. Explicit per-entity activation still works via
		// surface_prepare from the LLM.
		idx.add(entry.Name, entry.Name, pluginKeywordWeight)
		for _, alias := range manifest.Aliases {
			idx.add(alias, entry.Name, pluginKeywordWeight)
		}
	}
	return idx
}

// add records the (keyword, set) weight using MAX semantics — repeated calls
// for the same pair do not stack. This means a token that appears in N op
// names contributes at most one pluginKeywordWeight to its set, preventing
// "any single occurrence of a common token activates everything".
func (i *intentIndex) add(keyword, set string, weight float64) {
	keyword = normalizeToken(keyword)
	set = strings.TrimSpace(set)
	if keyword == "" || set == "" || weight == 0 {
		return
	}
	if i.keywords == nil {
		i.keywords = map[string]map[string]float64{}
	}
	bucket := i.keywords[keyword]
	if bucket == nil {
		bucket = map[string]float64{}
		i.keywords[keyword] = bucket
	}
	if existing, ok := bucket[set]; !ok || weight > existing {
		bucket[set] = weight
	}
}

// score walks the tokens of an observation text and accumulates the score
// for each activation set. Returns set name -> total score for sets that
// reach the score threshold.
func (i *intentIndex) score(text string) map[string]float64 {
	if i == nil || len(i.keywords) == 0 {
		return nil
	}
	scores := map[string]float64{}
	for _, token := range tokenizeText(text) {
		bucket, ok := i.keywords[token]
		if !ok {
			continue
		}
		for set, weight := range bucket {
			scores[set] += weight
		}
	}
	for set, total := range scores {
		if total < intentThreshold {
			delete(scores, set)
		}
	}
	return scores
}

// matches is a convenience: returns activation-set names that scored above
// threshold, sorted deterministically.
func (i *intentIndex) matches(text string) []string {
	scored := i.score(text)
	out := make([]string, 0, len(scored))
	for set := range scored {
		out = append(out, set)
	}
	sort.Strings(out)
	return out
}

// activationSets returns every activation-set name known to the index, used
// for emitting one reaction rule per set.
func (i *intentIndex) activationSets() []string {
	if i == nil {
		return nil
	}
	seen := map[string]struct{}{}
	for _, bucket := range i.keywords {
		for set := range bucket {
			seen[set] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for set := range seen {
		out = append(out, set)
	}
	sort.Strings(out)
	return out
}

// tokenizeText splits a free-text string on whitespace and punctuation,
// lowercases, prunes stop-words. Used for both description indexing and
// observation matching.
func tokenizeText(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !isTokenRune(r)
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := normalizeToken(p)
		if t == "" {
			continue
		}
		if _, stop := englishStopWords[t]; stop {
			continue
		}
		out = append(out, t)
	}
	return out
}

func isTokenRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '_':
		return true
	}
	return false
}

func normalizeToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) < 2 {
		// Single-character tokens are noise.
		return ""
	}
	return strings.ToLower(s)
}
