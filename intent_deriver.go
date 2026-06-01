package fluxplaneplugin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	coreevidence "github.com/fluxplane/fluxplane-core/core/evidence"
	corereaction "github.com/fluxplane/fluxplane-core/core/reaction"
	"github.com/fluxplane/fluxplane-core/core/resource"
	runtimeevidence "github.com/fluxplane/fluxplane-core/runtime/evidence"
)

// AssertionKindDexIntent is emitted by the intent deriver whenever an
// observation's tokens cross the score threshold for an activation set. The
// assertion's Target is the activation-set name; one reaction rule per set
// (see reactionsForActivationSets) turns those into enable actions.
const AssertionKindDexIntent = "dex.intent.detected"

// intentDeriver tokenizes channel-message observation text and emits a
// dex.intent.detected assertion per activation set that scores above the
// intent threshold. Coder reaction rules then enable those surfaces — no
// model-initiated surface_prepare needed on the first turn.
type intentDeriver struct {
	index *intentIndex
}

var _ runtimeevidence.AssertionDeriver = intentDeriver{}

// observationKinds are the observation kinds the deriver wants to see.
// Mirrors plugins/native/task/plugin.go's parallel-intent deriver.
var intentDeriverObservationKinds = []string{"channel.message", "session.continuation"}

func (intentDeriver) Spec() coreevidence.AssertionDeriverSpec {
	return coreevidence.AssertionDeriverSpec{
		Name:             "fluxplaneplugin.dex_intent",
		Description:      "Maps channel-message tokens to dex activation sets via tokenized op names and descriptions.",
		ObservationKinds: append([]string(nil), intentDeriverObservationKinds...),
	}
}

// Derive scores each incoming observation text against the keyword index and
// emits one assertion per matched activation set.
func (d intentDeriver) Derive(_ context.Context, req runtimeevidence.AssertionDeriveRequest) ([]coreevidence.Assertion, error) {
	if d.index == nil || len(d.index.keywords) == 0 {
		return nil, nil
	}
	var out []coreevidence.Assertion
	for _, observation := range req.Observations {
		if !matchesObservationKind(observation.Kind) {
			continue
		}
		text := observationText(observation.Content)
		if text == "" {
			continue
		}
		scored := d.index.score(text)
		if len(scored) == 0 {
			continue
		}
		ids := observationIDs(observation.ID)
		for set, score := range scored {
			out = append(out, coreevidence.Assertion{
				Kind:           AssertionKindDexIntent,
				Target:         set,
				Subject:        coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: set},
				Scope:          observation.Scope,
				Environment:    observation.Environment,
				Source:         "fluxplaneplugin.dex_intent",
				Confidence:     normalizeConfidence(score),
				ObservationIDs: ids,
			})
		}
	}
	return out, nil
}

func matchesObservationKind(kind string) bool {
	for _, want := range intentDeriverObservationKinds {
		if want == kind {
			return true
		}
	}
	return false
}

// observationText flattens an observation Content value into a single
// scoreable string. Mirrors plugins/native/task/plugin.go:observationText.
func observationText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if text, ok := typed["text"].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return strings.TrimSpace(fmt.Sprint(typed))
		}
		return string(data)
	}
}

func observationIDs(id string) []string {
	if id == "" {
		return nil
	}
	return []string{id}
}

// normalizeConfidence clamps the raw score into [0,1]. The score is just an
// accumulator; downstream uses Confidence comparatively, not absolutely.
func normalizeConfidence(score float64) float64 {
	if score <= 0 {
		return 0
	}
	// Map the threshold to 0.5 and saturate at 3*threshold → 1.0 so a single
	// strong match reads as "moderately confident" and multiple stacking
	// matches read as "high confidence".
	c := 0.5 + (score-intentThreshold)/(2*intentThreshold)
	if c < 0 {
		c = 0
	}
	if c > 1 {
		c = 1
	}
	return c
}

// reactionsForActivationSets builds one reaction rule per activation-set
// name in the index. Each rule fires on AssertionKindDexIntent + Target=<set>
// and enables the corresponding activation set.
//
// We emit one rule per set rather than a single wildcard rule because
// corereaction.Action is statically typed with its target (no dynamic
// "activate whatever the assertion points at" action kind today).
func reactionsForActivationSets(sets []string) []corereaction.Rule {
	out := make([]corereaction.Rule, 0, len(sets))
	for _, set := range sets {
		set := set
		out = append(out, corereaction.Rule{
			Name:        "fluxplaneplugin.dex_intent." + set,
			Description: "Auto-activate the " + set + " surface when dex intent tokens are detected.",
			When: corereaction.Matcher{
				Assertion: AssertionKindDexIntent,
				Target:    set,
				Subject:   coreevidence.Subject{Kind: coreevidence.SubjectCapability, Name: set},
			},
			Actions: []corereaction.Action{enableActivationSetAction(set)},
		})
	}
	return out
}

func enableActivationSetAction(name string) corereaction.Action {
	return corereaction.Action{
		Kind:          corereaction.ActionEnableActivationSet,
		ActivationSet: name,
	}
}

// ensure resource is referenced so future linker pruning doesn't drop it
// when only the assertion side is exercised in tests.
var _ = resource.PluginRef{}
