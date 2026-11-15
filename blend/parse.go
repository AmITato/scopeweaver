package blend

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

// maxStoredExamples is how many raw samples a ClassStyle keeps for re-derivation.
const maxStoredExamples = 5

// slugMark stands in for a digit run while a sample is being segmented. It is a
// single rune so index arithmetic on the working copy stays valid.
const slugMark rune = '\x01'

var (
	// ticketPrefix is the shape of a ticket token at the start of a sample.
	ticketPrefix = regexp.MustCompile(`^([A-Z]{2,5})-([0-9]{2,6})([-_/. ]|$)`)
	// fileExtension is a short final suffix such as .yml or .json.
	fileExtension = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,4}$`)
	// regexAlternatives finds the ticket-key alternation of a validator.
	regexAlternatives = regexp.MustCompile(`\((?:\?:)?((?:[A-Z]{2,5}\|)+[A-Z]{2,5})\)`)
	// regexTicketKey is one acceptable key.
	regexTicketKey = regexp.MustCompile(`^[A-Z]{2,5}$`)
	// trailingPunctuation is kept verbatim outside the slug.
	trailingPunctuation = ".,;:!?"
)

// Parse groups observations by class and derives the dominant convention of
// each, together with its confidence. Damage is tolerated: unusable samples are
// counted but never become a template. Parse is deterministic for a given
// observation order.
func Parse(observations []Observation) *Style {
	style := Default()
	style.Classes = make(map[Class]*ClassStyle)
	style.ObservedAt = time.Now().UTC()

	grouped := make(map[Class][]Observation)
	var order []Class
	for _, obs := range observations {
		class := Class(strings.TrimSpace(string(obs.Class)))
		if class == "" || !ValidClass(class) {
			continue
		}
		if _, seen := grouped[class]; !seen {
			order = append(order, class)
		}
		grouped[class] = append(grouped[class], obs)
		if source := strings.TrimSpace(obs.Source); source != "" {
			style.Sources = appendUnique(style.Sources, source)
		}
	}

	for _, class := range order {
		style.Classes[class] = derive(class, grouped[class])
	}
	return style
}

// derive builds one class style from a batch of observations.
func derive(class Class, observations []Observation) *ClassStyle {
	cs := &ClassStyle{Samples: len(observations)}
	var values, only []string
	for _, obs := range observations {
		pattern, regexOnly := splitRegex(obs)
		if pattern != "" && cs.Regex == "" {
			cs.Regex = pattern
		}
		if len(cs.Examples) < maxStoredExamples {
			if value := obs.Value; strings.TrimSpace(value) != "" && !regexOnly {
				cs.Examples = append(cs.Examples, value)
			}
		}
		if regexOnly {
			if skeleton := regexSkeleton(pattern); skeleton != "" {
				only = append(only, skeleton)
			}
			continue
		}
		values = append(values, obs.Value)
	}
	recompute(cs, class, values, only)
	return cs
}

// recompute replaces a class's derived fields from raw values and regex-only
// skeletons. Examples and Samples are owned by the caller.
func recompute(cs *ClassStyle, class Class, values, regexOnly []string) {
	keys := append([]string(nil), cs.TicketKeys...)
	if pattern := cs.Regex; pattern != "" {
		keys = append(keys, ticketKeysFromRegex(pattern)...)
	}
	for _, value := range values {
		if key, _, _, ok := matchTicket(value, keys); ok {
			keys = appendUnique(keys, key)
		}
	}
	keys = dedupe(keys)
	cs.TicketKeys = keys

	var shapes []string
	for _, value := range values {
		if shape, _, ok := normalizeSample(class, value, keys); ok {
			shapes = append(shapes, shape)
		}
	}
	shapes = append(shapes, regexOnly...)

	cs.Separator = dominantSeparator(values)
	cs.Case = dominantCase(values)
	if len(shapes) == 0 {
		cs.Template = ""
		cs.Confidence = confidence(cs.Samples, 0)
		return
	}

	counts := make(map[string]int, len(shapes))
	best := ""
	for _, shape := range shapes {
		counts[shape]++
		if best == "" || counts[shape] > counts[best] {
			best = shape
		}
	}
	cs.Template = best
	agreement := float64(counts[best]) / float64(len(shapes))
	cs.Confidence = confidence(cs.Samples, agreement)
}

// splitRegex separates an observation's validator, if any, from its concrete
// value. A regex in Source annotates a concrete sample; a regex in Value is the
// whole sample.
func splitRegex(obs Observation) (pattern string, regexOnly bool) {
	const marker = "regex:"
	source := strings.TrimSpace(obs.Source)
	if len(source) >= len(marker) && strings.EqualFold(source[:len(marker)], marker) {
		pattern = trimPattern(source[len(marker):])
		value := strings.TrimSpace(obs.Value)
		if len(value) >= len(marker) && strings.EqualFold(value[:len(marker)], marker) {
			regexOnly = true
		}
		return pattern, regexOnly
	}
	value := strings.TrimSpace(obs.Value)
	if len(value) >= len(marker) && strings.EqualFold(value[:len(marker)], marker) {
		return trimPattern(value[len(marker):]), true
	}
	return "", false
}

func trimPattern(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) >= 2 {
		first, last := pattern[0], pattern[len(pattern)-1]
		if first == last && (first == '/' || first == '`' || first == '"' || first == '\'') {
			pattern = strings.TrimSpace(pattern[1 : len(pattern)-1])
		}
	}
	return pattern
}

func ticketKeysFromRegex(pattern string) []string {
	var keys []string
	for _, match := range regexAlternatives.FindAllStringSubmatch(pattern, -1) {
		for _, key := range strings.Split(match[1], "|") {
			if regexTicketKey.MatchString(key) {
				keys = appendUnique(keys, key)
			}
		}
	}
	return keys
}

// matchTicket finds a ticket token at the start of value. The optional extra
// keys are matched case-insensitively and keep their observed spelling.
func matchTicket(value string, extra []string) (key, separator string, end int, ok bool) {
	best := -1
	if loc := ticketPrefix.FindStringSubmatchIndex(value); loc != nil {
		key = value[loc[2]:loc[3]]
		end = loc[1]
		if loc[6] >= 0 {
			separator = value[loc[6]:loc[7]]
		}
		best = end
	}
	for _, candidate := range extra {
		if candidate == "" {
			continue
		}
		re, err := regexp.Compile(`(?i)^` + regexp.QuoteMeta(candidate) + `-([0-9]{2,6})([-_/. ]|$)`)
		if err != nil {
			continue
		}
		loc := re.FindStringSubmatchIndex(value)
		if loc == nil || loc[1] <= best {
			continue
		}
		key = value[:len(candidate)]
		end = loc[1]
		separator = ""
		if loc[4] >= 0 {
			separator = value[loc[4]:loc[5]]
		}
		best = end
	}
	return key, separator, end, best > 0
}

// normalizeSample reduces one value to a shape. It replaces the ticket token,
// digit runs and the sample's own slug tail; extensions and trailing punctuation
// survive so generated names keep them. It reports false for damage.
func normalizeSample(class Class, raw string, keys []string) (shape, ticketKey string, ok bool) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\t", " "))
	value = strings.Join(strings.Fields(value), " ")
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '"' || first == '\'' || first == '`') {
			value = strings.TrimSpace(value[1 : len(value)-1])
		}
	}
	if value == "" {
		return "", "", false
	}

	// Trailing sentence punctuation is part of the convention, not the slug.
	cut := len(value)
	for cut > 0 && strings.ContainsRune(trailingPunctuation, rune(value[cut-1])) {
		cut--
	}
	punct := value[cut:]
	value = strings.TrimRight(value[:cut], " ")
	if strings.TrimSpace(value) == "" {
		return "", "", false
	}

	// Keep a short final extension so workflows and S3 keys stay recognisable.
	extension := ""
	if loc := fileExtension.FindStringIndex(value); loc != nil && loc[0] > 0 {
		if hasAlnum(value[:loc[0]]) {
			extension = value[loc[0]:]
			value = value[:loc[0]]
		}
	}

	prefix := ""
	if key, separator, end, hit := matchTicket(value, keys); hit {
		ticketKey = key
		prefix = "{TICKET}" + separator
		value = value[end:]
	}

	value = strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return slugMark
		}
		return r
	}, value)
	value = collapseDigits(value)

	shape = ""
	if prefix != "" {
		// A ticket already anchors the shape; everything after it is prose.
		if hasLetter(value) || hasAlnum(value) {
			shape = "{Slug}"
		}
	} else {
		shape = buildShape(class, value)
	}
	if shape == "" {
		if prefix == "" {
			return "", ticketKey, false
		}
		shape = prefix
	} else if prefix != "" {
		shape = prefix + shape
	}
	shape += extension + punct
	shape = strings.ReplaceAll(shape, string(slugMark), "{N}")
	return shape, ticketKey, true
}

// collapseDigits squeezes a run of digit markers to one.
func collapseDigits(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	previous := false
	for _, r := range value {
		if r == slugMark {
			if previous {
				continue
			}
			previous = true
		} else {
			previous = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

type segment struct{ start, end int }

func isSeparator(r rune) bool {
	switch r {
	case '/', '-', '_', ' ', '.':
		return true
	}
	return false
}

func segments(value string) []segment {
	var out []segment
	start := -1
	for i, r := range value {
		if isSeparator(r) {
			if start >= 0 {
				out = append(out, segment{start, i})
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, segment{start, len(value)})
	}
	return out
}

// buildShape replaces the prose tail of a value with {Slug}, preserving at most
// one leading structural token (a ticket is handled before this call).
func buildShape(class Class, value string) string {
	if value == "" || !hasAlnum(value) {
		return ""
	}
	if !hasLetter(value) {
		return value
	}
	parts := segments(value)
	if len(parts) == 0 {
		return ""
	}
	if len(parts) == 1 {
		return "{Slug}"
	}
	first := value[parts[0].start:parts[0].end]
	if !keepPrefix(class, first) {
		return "{Slug}"
	}
	separator := value[parts[0].end:parts[1].start]
	return first + separator + "{Slug}"
}

// keepPrefix decides whether a leading segment is fixed structure rather than
// prose. Digit runs, short uppercase tokens and a few class-specific stems stay.
func keepPrefix(class Class, segment string) bool {
	if strings.ContainsRune(segment, slugMark) {
		return true
	}
	if len(segment) >= 2 && len(segment) <= 5 && isUpper(segment) {
		return true
	}
	if !isLowerLetters(segment) || len(segment) > 12 {
		return false
	}
	switch class {
	case ClassBranch, ClassCommitSubject, ClassMarker:
		return true
	case ClassIAMUser, ClassIAMRole:
		switch segment {
		case "svc", "app", "api", "sg":
			return true
		}
	}
	return false
}

func hasAlnum(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}) >= 0
}

func hasLetter(value string) bool {
	return strings.IndexFunc(value, unicode.IsLetter) >= 0
}

func isUpper(value string) bool {
	for _, r := range value {
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return value != ""
}

func isLowerLetters(value string) bool {
	for _, r := range value {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return value != ""
}

// dominantCase classifies the case of each usable sample and returns the most
// common mode. Ties prefer lower, then upper, then title, then as-is.
func dominantCase(values []string) string {
	order := []string{"lower", "upper", "title", "asis"}
	counts := make(map[string]int, len(order))
	for _, value := range values {
		if mode := caseOf(value); mode != "" {
			counts[mode]++
		}
	}
	best := "asis"
	for _, mode := range order {
		if counts[mode] > counts[best] {
			best = mode
		}
	}
	if counts[best] == 0 {
		return "asis"
	}
	return best
}

func caseOf(value string) string {
	var letters strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) {
			letters.WriteRune(r)
		}
	}
	text := letters.String()
	if text == "" {
		return ""
	}
	if text == strings.ToUpper(text) {
		if text == strings.ToLower(text) {
			return "asis"
		}
		return "upper"
	}
	if text == strings.ToLower(text) {
		return "lower"
	}
	words := strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) == 0 {
		return "asis"
	}
	upperStarts, lowerStarts := 0, 0
	for _, word := range words {
		runes := []rune(word)
		if len(runes) == 0 {
			continue
		}
		if unicode.IsLower(runes[0]) {
			lowerStarts++
		} else {
			upperStarts++
		}
	}
	// Minor words ("to", "of") are normal in title-shaped names.
	if upperStarts*3 >= len(words)*2 && upperStarts > lowerStarts {
		return "title"
	}
	return "asis"
}

// dominantSeparator counts separators that sit between two word characters.
// Decimal points and a final extension dot do not count.
func dominantSeparator(values []string) string {
	order := []string{"/", "-", "_", " ", "."}
	counts := make(map[string]int, len(order))
	for _, value := range values {
		runes := []rune(value)
		for i := 0; i < len(runes); {
			if !isSeparator(runes[i]) {
				i++
				continue
			}
			start, separator := i, runes[i]
			for i < len(runes) && isSeparator(runes[i]) {
				// A run is one boundary; keep the first separator character.
				i++
			}
			end := i
			prev, next := rune(0), rune(0)
			if start > 0 {
				prev = runes[start-1]
			}
			if end < len(runes) {
				next = runes[end]
			}
			if separator == '.' {
				if unicode.IsDigit(prev) && unicode.IsDigit(next) {
					continue
				}
				if fileExtension.MatchString(string(runes[start:])) {
					continue
				}
			}
			wordBefore := unicode.IsLetter(prev) || unicode.IsDigit(prev)
			wordAfter := unicode.IsLetter(next) || unicode.IsDigit(next)
			if !wordBefore && !wordAfter {
				// A structureless value can still carry a separator convention.
				counts[string(separator)]++
				continue
			}
			if !wordBefore || !wordAfter {
				continue
			}
			counts[string(separator)]++
		}
	}
	best, bestCount := "", 0
	for _, separator := range order {
		if counts[separator] > bestCount {
			best, bestCount = separator, counts[separator]
		}
	}
	return best
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

func dedupe(list []string) []string {
	out := make([]string, 0, len(list))
	for _, value := range list {
		if value != "" {
			out = appendUnique(out, value)
		}
	}
	return out
}
