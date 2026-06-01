package fluxplaneplugin

import (
	"context"
	"sort"
	"strings"
	"testing"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
)

func TestTokenizeTextDropsStopWords(t *testing.T) {
	got := tokenizeText("Check the jira ticket for the deployment of the new feature")
	want := []string{"check", "jira", "ticket", "deployment", "new", "feature"}
	if !equalSorted(got, want) {
		t.Errorf("tokenizeText = %v, want %v", got, want)
	}
}

func TestIntentIndexScoresPluginName(t *testing.T) {
	idx := &intentIndex{}
	idx.add("gitlab", "gitlab", pluginKeywordWeight)
	idx.add("mr", "gitlab_mr", pluginKeywordWeight)
	idx.add("mr", "gitlab", pluginKeywordWeight)

	matches := idx.matches("check the gitlab mr")
	if !contains(matches, "gitlab") {
		t.Errorf("matches = %v, want gitlab", matches)
	}
	if !contains(matches, "gitlab_mr") {
		t.Errorf("matches = %v, want gitlab_mr", matches)
	}
}

func TestIntentIndexThresholdRejectsGenericOnlyMatches(t *testing.T) {
	idx := &intentIndex{}
	// Without any plugin/identifier keyword, the message can't activate anything.
	matches := idx.matches("check the logs please")
	if len(matches) != 0 {
		t.Errorf("matches = %v, want none (no plugin keyword)", matches)
	}

	// Once a plugin keyword is indexed, it fires when the user names it.
	idx.add("loki", "loki", pluginKeywordWeight)
	matches = idx.matches("check the loki logs please")
	if !contains(matches, "loki") {
		t.Errorf("matches = %v, want loki", matches)
	}
}

// Regression for the runtime over-activation observed in coder: with the old
// `bucket[set] += weight` accumulator, a token that appeared in N op names
// would stack to N*pluginKeywordWeight per set, so a single hit in the user
// message could clear arbitrarily high thresholds. Max semantics caps the
// per-(token,set) contribution at pluginKeywordWeight.
func TestIntentIndexDoesNotStackRepeatedAdds(t *testing.T) {
	idx := &intentIndex{}
	for i := 0; i < 100; i++ {
		idx.add("mr", "gitlab_mr", pluginKeywordWeight)
	}
	score := idx.score("mr")["gitlab_mr"]
	if score != pluginKeywordWeight {
		t.Errorf("score after 100 adds = %v, want %v (max semantics)", score, pluginKeywordWeight)
	}
}

func TestIntentDeriverEmitsAssertionForMatchedSet(t *testing.T) {
	idx := &intentIndex{}
	idx.add("jira", "jira", pluginKeywordWeight)
	idx.add("issue", "jira", pluginKeywordWeight)
	deriver := intentDeriver{index: idx}

	assertions, err := deriver.Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			ID:      "obs-1",
			Kind:    "channel.message",
			Content: "check jira to find XXX",
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 1 {
		t.Fatalf("len(assertions) = %d, want 1; got %#v", len(assertions), assertions)
	}
	a := assertions[0]
	if a.Kind != AssertionKindDexIntent {
		t.Errorf("Kind = %q, want %q", a.Kind, AssertionKindDexIntent)
	}
	if a.Target != "jira" {
		t.Errorf("Target = %q, want jira", a.Target)
	}
	if a.Subject.Kind != coreevidence.SubjectCapability || a.Subject.Name != "jira" {
		t.Errorf("Subject = %#v", a.Subject)
	}
	if a.Confidence <= 0 || a.Confidence > 1 {
		t.Errorf("Confidence = %v, want in (0, 1]", a.Confidence)
	}
	if len(a.ObservationIDs) != 1 || a.ObservationIDs[0] != "obs-1" {
		t.Errorf("ObservationIDs = %v, want [obs-1]", a.ObservationIDs)
	}
}

func TestIntentDeriverEmitsForMultipleSetsInOneMessage(t *testing.T) {
	idx := &intentIndex{}
	idx.add("jira", "jira", pluginKeywordWeight)
	idx.add("loki", "loki", pluginKeywordWeight)
	deriver := intentDeriver{index: idx}

	assertions, err := deriver.Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			ID:      "obs-2",
			Kind:    "channel.message",
			Content: "investigate jira ticket and check loki logs",
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	targets := assertionTargets(assertions)
	if !contains(targets, "jira") || !contains(targets, "loki") {
		t.Errorf("targets = %v, want jira + loki", targets)
	}
}

func TestIntentDeriverIgnoresWrongObservationKind(t *testing.T) {
	idx := &intentIndex{}
	idx.add("jira", "jira", pluginKeywordWeight)
	deriver := intentDeriver{index: idx}

	assertions, err := deriver.Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			Kind:    "endpoint.health",
			Content: "check jira",
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 0 {
		t.Errorf("len(assertions) = %d, want 0 for non-channel kind", len(assertions))
	}
}

func TestIntentDeriverHandlesMapContent(t *testing.T) {
	idx := &intentIndex{}
	idx.add("jira", "jira", pluginKeywordWeight)
	deriver := intentDeriver{index: idx}

	assertions, err := deriver.Derive(context.Background(), runtimeevidence.AssertionDeriveRequest{
		Observations: []coreevidence.Observation{{
			Kind:    "channel.message",
			Content: map[string]any{"text": "please check jira"},
		}},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(assertions) != 1 || assertions[0].Target != "jira" {
		t.Errorf("assertions = %#v, want one jira target", assertions)
	}
}

func TestReactionsForActivationSets(t *testing.T) {
	rules := reactionsForActivationSets([]string{"jira", "loki"})
	if len(rules) != 2 {
		t.Fatalf("len(rules) = %d, want 2", len(rules))
	}
	for _, rule := range rules {
		if rule.When.Assertion != AssertionKindDexIntent {
			t.Errorf("rule.When.Assertion = %q, want %q", rule.When.Assertion, AssertionKindDexIntent)
		}
		if len(rule.Actions) != 1 || rule.Actions[0].Kind != corereaction.ActionEnableActivationSet {
			t.Errorf("rule actions = %#v, want one EnableActivationSet", rule.Actions)
		}
		if rule.Actions[0].ActivationSet != rule.When.Target {
			t.Errorf("rule action target = %q, want %q", rule.Actions[0].ActivationSet, rule.When.Target)
		}
	}
}

func TestBuildIntentBundleSkipsEmptyIndex(t *testing.T) {
	// nil engine returns an empty index -> no intent bundle emitted.
	bundle, ok := buildIntentBundle(context.Background(), nil)
	if ok {
		t.Errorf("buildIntentBundle returned ok=true for nil engine: %#v", bundle)
	}
}

func equalSorted(a, b []string) bool {
	x := append([]string(nil), a...)
	y := append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}

func assertionTargets(assertions []coreevidence.Assertion) []string {
	out := make([]string, 0, len(assertions))
	for _, a := range assertions {
		out = append(out, a.Target)
	}
	return out
}

// silence unused if helpers shift later.
var _ = strings.TrimSpace
