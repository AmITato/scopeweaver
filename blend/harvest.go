package blend

import (
	"sort"
	"strings"
	"time"
)

// Harvest limits. They bound what one observation run can cost, not what a style
// may hold: a later run can always add more.
const (
	// MaxObservationsPerClass is the per-class ceiling for one harvest.
	MaxObservationsPerClass = 120
	// MaxWorkflowRegexFiles bounds how many matching CI files a GitHub harvest reads.
	MaxWorkflowRegexFiles = 5
)

// collector accumulates observations for one harvest run, deduplicated and
// capped per class.
type collector struct {
	obs    []Observation
	counts map[Class]int
	seen   map[Class]map[string]bool
	errs   []error
}

func newCollector() *collector {
	return &collector{
		counts: map[Class]int{},
		seen:   map[Class]map[string]bool{},
	}
}

// add records value under class unless the class is full or the value is a
// duplicate. It reports whether the value was recorded.
func (c *collector) add(class Class, value, source string) bool {
	value = strings.TrimSpace(value)
	if value == "" || !ValidClass(class) {
		return false
	}
	if c.counts[class] >= MaxObservationsPerClass {
		return false
	}
	seen := c.seen[class]
	if seen == nil {
		seen = map[string]bool{}
		c.seen[class] = seen
	}
	if seen[value] {
		return false
	}
	seen[value] = true
	c.counts[class]++
	c.obs = append(c.obs, Observation{Class: class, Value: value, Source: strings.TrimSpace(source)})
	return true
}

// full reports whether class has reached the harvest ceiling.
func (c *collector) full(class Class) bool {
	return c.counts[class] >= MaxObservationsPerClass
}

// fail records a non-fatal error.
func (c *collector) fail(err error) {
	if err != nil {
		c.errs = append(c.errs, err)
	}
}

// Merge folds one observation into s, counting the sample and keeping a bounded
// set of examples. It re-derives that class's grammar from its stored examples
// plus the new sample so a merge never leaves a stale template behind.
func (s *Style) Merge(o Observation) *Style {
	if s == nil {
		s = Default()
	}
	class := Class(strings.TrimSpace(string(o.Class)))
	if class == "" || !ValidClass(class) {
		return s
	}
	pattern, regexOnly := splitRegex(o)
	value := strings.TrimSpace(o.Value)
	if value == "" && pattern == "" {
		return s
	}
	if s.Version == 0 {
		s.Version = StyleVersion
	}
	if s.Classes == nil {
		s.Classes = map[Class]*ClassStyle{}
	}
	cs := s.Classes[class]
	if cs == nil {
		cs = &ClassStyle{}
		s.Classes[class] = cs
	}
	if source := strings.TrimSpace(o.Source); source != "" {
		s.Sources = appendUnique(s.Sources, source)
	}
	if pattern != "" {
		cs.Regex = pattern
	}

	values := append([]string(nil), cs.Examples...)
	if !regexOnly && value != "" {
		values = append(values, value)
		if len(cs.Examples) < maxStoredExamples {
			cs.Examples = append(cs.Examples, value)
		}
	}
	var only []string
	if regexOnly {
		if skeleton := regexSkeleton(pattern); skeleton != "" {
			only = append(only, skeleton)
		}
	}
	cs.Samples++
	recompute(cs, class, values, only)
	return s
}

// MergeObservations clones dst, merges every observation and stamps the result
// with the sources it came from and the time of the run. A nil dst starts from
// Default, so the caller always gets an owned value.
func MergeObservations(dst *Style, obs []Observation) *Style {
	out := dst.Clone()
	if out.Version == 0 {
		out.Version = StyleVersion
	}
	sources := map[string]bool{}
	for _, src := range out.Sources {
		if src = strings.TrimSpace(src); src != "" {
			sources[src] = true
		}
	}
	for _, o := range obs {
		out.Merge(o)
		pattern, _ := splitRegex(o)
		if !ValidClass(o.Class) || (strings.TrimSpace(o.Value) == "" && pattern == "") {
			continue
		}
		if src := strings.TrimSpace(o.Source); src != "" {
			sources[src] = true
		}
	}
	out.Sources = out.Sources[:0]
	for src := range sources {
		out.Sources = append(out.Sources, src)
	}
	sort.Strings(out.Sources)
	if len(obs) > 0 {
		out.ObservedAt = time.Now().UTC()
	}
	return out
}

// observedCase classifies the casing of a value for agreement scoring.
func observedCase(value string) string {
	lower, upper, title := true, true, true
	var letters bool
	startOfWord := true
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			letters = true
			lower = false
			if !startOfWord {
				title = false
			}
			startOfWord = false
			continue
		}
		if r >= 'a' && r <= 'z' {
			letters = true
			upper = false
			if startOfWord {
				title = false
			}
			startOfWord = false
			continue
		}
		if r >= '0' && r <= '9' {
			startOfWord = false
			continue
		}
		startOfWord = true
	}
	if !letters {
		return "asis"
	}
	switch {
	case upper:
		return "upper"
	case lower:
		return "lower"
	case title:
		return "title"
	default:
		return "asis"
	}
}

// observedSeparator returns the first separator character in value.
func observedSeparator(value string) string {
	for _, sep := range []string{"-", "_", ".", " "} {
		if strings.Contains(value, sep) {
			return sep
		}
	}
	return ""
}
