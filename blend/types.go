// Package blend learns a target's own naming conventions and generates names that
// conform to them, so created objects look like work the organisation already does.
// See docs/opsec-blend.md §5.
//
// The package is pure: harvesters produce Observations, a Style parses and scores
// them, and Name renders a validated candidate. Nothing here performs I/O.
package blend

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Class is one artifact family with its own convention.
type Class string

const (
	ClassBranch        Class = "branch"
	ClassWorkflow      Class = "workflow"
	ClassCommitSubject Class = "commit_subject"
	ClassIAMUser       Class = "iam_user"
	ClassIAMRole       Class = "iam_role"
	ClassTagValue      Class = "tag_value"
	ClassSGName        Class = "sg_name"
	ClassSGDescription Class = "sg_description"
	ClassMarker        Class = "marker"
	ClassS3Key         Class = "s3_key"
)

// Classes lists every supported class in display order.
func Classes() []Class {
	return []Class{ClassBranch, ClassWorkflow, ClassCommitSubject, ClassIAMUser, ClassIAMRole,
		ClassTagValue, ClassSGName, ClassSGDescription, ClassMarker, ClassS3Key}
}

// Observation is one real example of the target's own naming, with its source.
type Observation struct {
	Class  Class  `json:"class"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// ClassStyle is the learned convention for one class. Template uses {TICKET} and
// {Slug} placeholders; Regex, when present, is the target's own validator and wins
// over everything else.
type ClassStyle struct {
	Samples    int      `json:"samples"`
	Template   string   `json:"template,omitempty"`
	TicketKeys []string `json:"ticket_keys,omitempty"`
	Regex      string   `json:"regex,omitempty"`
	Separator  string   `json:"separator,omitempty"`
	Case       string   `json:"case,omitempty"` // lower | upper | title | asis
	Confidence float64  `json:"confidence"`     // 0..1, from sample count and agreement
	Examples   []string `json:"examples,omitempty"`
}

// Style is the parsed grammar for a target, stored in tasks.house_style.
type Style struct {
	Version    int                   `json:"version"`
	Classes    map[Class]*ClassStyle `json:"classes,omitempty"`
	Sources    []string              `json:"sources,omitempty"`
	ObservedAt time.Time             `json:"observed_at,omitempty"`
}

// StyleVersion is the current serialized shape.
const StyleVersion = 1

// Default returns a neutral style used when nothing was observed. It is marked by
// an empty class map so callers can tell it apart from a harvest.
func Default() *Style { return &Style{Version: StyleVersion} }

// Empty reports whether no class was learned.
func (s *Style) Empty() bool { return s == nil || len(s.Classes) == 0 }

// Clone deep-copies a style so a merge cannot mutate a shared value.
func (s *Style) Clone() *Style {
	if s == nil {
		return Default()
	}
	out := &Style{Version: s.Version, ObservedAt: s.ObservedAt, Sources: append([]string(nil), s.Sources...)}
	out.Classes = make(map[Class]*ClassStyle, len(s.Classes))
	for class, cs := range s.Classes {
		if cs == nil {
			continue
		}
		cp := *cs
		cp.TicketKeys = append([]string(nil), cs.TicketKeys...)
		cp.Examples = append([]string(nil), cs.Examples...)
		out.Classes[class] = &cp
	}
	return out
}

// JSON serializes the style for the task column.
func (s *Style) JSON() json.RawMessage {
	if s == nil {
		return json.RawMessage("{}")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

// FromJSON parses a stored style, falling back to Default on damage.
func FromJSON(raw []byte) *Style {
	if len(raw) == 0 {
		return Default()
	}
	var s Style
	if err := json.Unmarshal(raw, &s); err != nil {
		return Default()
	}
	if s.Classes == nil {
		s.Classes = map[Class]*ClassStyle{}
	}
	if s.Version == 0 {
		s.Version = StyleVersion
	}
	return &s
}

// ValidClass reports whether c is supported.
func ValidClass(c Class) bool {
	for _, known := range Classes() {
		if known == c {
			return true
		}
	}
	return false
}

// Confidence bands. A class is usable for generation only at or above
// GenerateFloor; below LearnFloor it is recorded but not trusted.
const (
	LearnFloor    = 0.2
	GenerateFloor = 0.5
)

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// confidence derives a score from sample count and agreement ratio.
func confidence(samples int, agreement float64) float64 {
	if samples <= 0 {
		return 0
	}
	volume := clamp01(float64(samples) / 12)
	return clamp01(0.35*volume+0.65*clamp01(agreement)) * clamp01(float64(samples)/3+0.34)
}

// sortedKeys returns map keys in stable order.
func sortedKeys[V any](m map[Class]V) []Class {
	out := make([]Class, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// slugify reduces free text to a lowercase dashed token.
func slugify(text string) string {
	var b strings.Builder
	lastDash := true
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
