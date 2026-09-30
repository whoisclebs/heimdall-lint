// Package suggest implements the layered "Did you mean?" engine:
// framework-notation mapping first, then a gated fuzzy match ranked by a
// weighted score. Bad suggestions are worse than none, so every candidate must
// pass a plausibility gate before it is scored.
package suggest

import (
	"cmp"
	"slices"
	"strings"

	"github.com/whoisclebs/heimdall-lint/internal/naming"
)

// Tunables. All scoring knobs live here.
const (
	weightEdit   = 0.55
	weightTokens = 0.25
	weightPrefix = 0.10
	weightLength = 0.10

	highConfidence   = 0.85
	maxMediumResults = 3

	// Tokens shorter than this must match exactly to count as a typo.
	minFuzzyTokenLength = 3
)

// tolerance is the plausibility gate: a whole name may differ by one edit per
// charsPerEdit characters, and one edit per charsPerTokenEdit inside a token.
type tolerance struct {
	charsPerEdit      int
	charsPerTokenEdit int
	// minScore is the medium-confidence threshold: below it, suggest nothing.
	minScore float64
}

var (
	// Variable names are long and numerous, so the gate is strict.
	nameTolerance = tolerance{charsPerEdit: 6, charsPerTokenEdit: 4, minScore: 0.70}
	// Enum values are a short closed list, so a looser gate is safe.
	valueTolerance = tolerance{charsPerEdit: 3, charsPerTokenEdit: 3, minScore: 0.60}
)

type Kind int

const (
	KindNone Kind = iota
	// KindCase: only letter case differs.
	KindCase
	// KindProperty: written in framework property notation.
	KindProperty
	// KindTypo: a fuzzy match.
	KindTypo
)

type Result struct {
	Kind        Kind
	Suggestions []string
}

type Suggester struct {
	names    []string
	known    map[string]struct{}
	strategy naming.Strategy
	gate     tolerance
}

// New builds a suggester for environment variable names.
func New(names []string, strategy naming.Strategy) *Suggester {
	return build(names, strategy, nameTolerance)
}

// NewForValues builds a suggester for the allowed values of an enum.
func NewForValues(values []string) *Suggester {
	return build(values, naming.Spring{}, valueTolerance)
}

func build(names []string, strategy naming.Strategy, gate tolerance) *Suggester {
	known := make(map[string]struct{}, len(names))
	for _, name := range names {
		known[name] = struct{}{}
	}
	sorted := slices.Sorted(slices.Values(names))
	return &Suggester{names: sorted, known: known, strategy: strategy, gate: gate}
}

func (s *Suggester) Suggest(unknown string) Result {
	if exact := s.exactMatches(unknown); len(exact) > 0 {
		kind := KindCase
		if s.strategy.IsPropertyNotation(unknown) {
			kind = KindProperty
		}
		return Result{Kind: kind, Suggestions: exact}
	}
	return s.fuzzy(unknown)
}

// exactMatches returns the declared names the unknown name maps to. Property
// notation is accepted only when exactly one candidate exists ("unequivocal").
func (s *Suggester) exactMatches(unknown string) []string {
	var matches []string
	for _, candidate := range s.strategy.EnvironmentNames(unknown) {
		if _, declared := s.known[candidate]; declared && !slices.Contains(matches, candidate) {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return nil
	}
	return matches
}

type scored struct {
	name  string
	score float64
}

func (s *Suggester) fuzzy(unknown string) Result {
	target := s.strategy.Normalize(unknown)

	var candidates []scored
	for _, name := range s.names {
		if !s.gate.plausible(target, name) {
			continue
		}
		if score := Score(target, name); score >= s.gate.minScore {
			candidates = append(candidates, scored{name, score})
		}
	}
	if len(candidates) == 0 {
		return Result{}
	}
	slices.SortFunc(candidates, func(a, b scored) int {
		return cmp.Or(cmp.Compare(b.score, a.score), cmp.Compare(a.name, b.name))
	})

	limit := maxMediumResults
	if candidates[0].score >= highConfidence {
		limit = 1
		for _, other := range candidates[1:] {
			if other.score >= highConfidence {
				limit++
			}
		}
	}
	limit = min(limit, len(candidates))

	suggestions := make([]string, limit)
	for i := range suggestions {
		suggestions[i] = candidates[i].name
	}
	return Result{Kind: KindTypo, Suggestions: suggestions}
}

// Score combines edit similarity, token similarity, common prefix and length
// similarity into a value in [0, 1].
func Score(a, b string) float64 {
	return weightEdit*similarity(a, b) +
		weightTokens*tokenSimilarity(a, b) +
		weightPrefix*prefixSimilarity(a, b) +
		weightLength*lengthSimilarity(a, b)
}

// plausible is the gate: a typo changes little, and any token it changes must
// still look like the original token. This is what keeps DATABASE_PORT from
// being suggested for DATABASE_HOST.
func (g tolerance) plausible(a, b string) bool {
	longest := max(len(a), len(b))
	if damerauLevenshtein(a, b) > max(1, longest/g.charsPerEdit) {
		return false
	}
	left, right := strings.Split(a, "_"), strings.Split(b, "_")
	if len(left) != len(right) {
		return true
	}
	for i := range left {
		if left[i] != right[i] && !g.tokensLookAlike(left[i], right[i]) {
			return false
		}
	}
	return true
}

func (g tolerance) tokensLookAlike(a, b string) bool {
	if min(len(a), len(b)) < minFuzzyTokenLength {
		return false
	}
	return damerauLevenshtein(a, b) <= max(1, max(len(a), len(b))/g.charsPerTokenEdit)
}

func tokenSimilarity(a, b string) float64 {
	left, right := strings.Split(a, "_"), strings.Split(b, "_")
	total := 0.0
	for _, token := range left {
		best := 0.0
		for _, other := range right {
			best = max(best, similarity(token, other))
		}
		total += best
	}
	return total / float64(max(len(left), len(right)))
}

func prefixSimilarity(a, b string) float64 {
	common := 0
	for common < min(len(a), len(b)) && a[common] == b[common] {
		common++
	}
	return float64(common) / float64(max(len(a), len(b)))
}

func lengthSimilarity(a, b string) float64 {
	longest := max(len(a), len(b))
	return 1 - float64(max(len(a), len(b))-min(len(a), len(b)))/float64(longest)
}
