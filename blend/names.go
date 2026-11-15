package blend

import (
	"errors"
	"fmt"
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// maxNameLength is the hard limit for a generated or validated name.
const maxNameLength = 100

var (
	// ErrUnlearned means the class was not observed well enough to generate.
	ErrUnlearned = errors.New("blend: class convention not learned")
	// ErrNoCandidate means every bounded variation was rejected by the target's
	// own validator.
	ErrNoCandidate = errors.New("blend: no generated name matches the validator")
	// ErrNonConforming means a supplied name breaks a learned rule.
	ErrNonConforming = errors.New("blend: name does not conform to the learned convention")
)

// Name renders a name for c that follows the learned convention. It is pure and
// deterministic: the same style, purpose and seq always produce the same bytes.
// A validator, when present, is authoritative and drives bounded retries.
func (s *Style) Name(c Class, purpose string, seq int) (string, error) {
	cs, err := s.classStyle(c)
	if err != nil {
		return "", err
	}

	var validator *regexp.Regexp
	if pattern := strings.TrimSpace(cs.Regex); pattern != "" {
		// A damaged validator is ignored rather than blocking generation.
		validator, _ = regexp.Compile(pattern)
	}
	if cs.Confidence < GenerateFloor && validator == nil {
		return "", fmt.Errorf("%w: %s confidence %.2f", ErrUnlearned, c, cs.Confidence)
	}

	template := strings.TrimSpace(cs.Template)
	skeleton := ""
	if validator != nil {
		skeleton = regexSkeleton(validator.String())
	}
	if template == "" {
		template = skeleton
	}
	if template == "" && validator == nil {
		return "", fmt.Errorf("%w: %s has no template", ErrUnlearned, c)
	}

	keys := append([]string(nil), cs.TicketKeys...)
	if validator != nil {
		if fromValidator := ticketKeysFromRegex(validator.String()); len(fromValidator) > 0 {
			// The validator is authoritative; never prefer a key it rejects.
			keys = fromValidator
		}
	}
	keys = dedupe(keys)
	key := chooseTicketKey(keys, purpose, seq)

	base := slugify(purpose)
	if base == "" {
		base = "change"
	}
	slugs := slugVariants(base)
	numbers := numberVariants(purpose, seq)

	templates := []string{}
	if template != "" {
		templates = append(templates, template)
	}
	if skeleton != "" && skeleton != template {
		templates = append(templates, skeleton)
	}

	modes := []string{cs.Case, "lower", "asis", "title", "upper"}
	modes = dedupe(modes)
	separators := []string{cs.Separator, "-", " ", ""}
	separators = dedupe(separators)

	for _, tpl := range templates {
		for _, slug := range slugs {
			slugAttempts := 0
			for _, separator := range separators {
				for _, mode := range modes {
					for _, number := range numbers {
						if slugAttempts >= 96 {
							break
						}
						slugAttempts++
						candidate := renderTemplate(tpl, applyCase(slug, mode, separator), key, number)
						candidate = clip(candidate, maxNameLength)
						if validator == nil || validator.MatchString(candidate) {
							return candidate, nil
						}
					}
				}
			}
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoCandidate, c)
}

// Validate checks name against a class's validator and learned rules. The
// validator wins over case and separator checks when one is stored.
func (s *Style) Validate(c Class, name string) error {
	cs, err := s.classStyle(c)
	if err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: empty name", ErrNonConforming)
	}
	if pattern := strings.TrimSpace(cs.Regex); pattern != "" {
		if re, compileErr := regexp.Compile(pattern); compileErr == nil {
			if !re.MatchString(name) {
				return fmt.Errorf("%w: %q does not match %s", ErrNonConforming, name, pattern)
			}
			return nil
		}
	}
	if cs.Confidence < GenerateFloor {
		return fmt.Errorf("%w: %s confidence %.2f", ErrUnlearned, c, cs.Confidence)
	}
	if !caseAllowed(cs.Case, name) {
		return fmt.Errorf("%w: %q is not %s case", ErrNonConforming, name, cs.Case)
	}
	if !separatorAllowed(cs.Separator, name) {
		return fmt.Errorf("%w: %q does not use separator %q", ErrNonConforming, name, cs.Separator)
	}
	return nil
}

func (s *Style) classStyle(c Class) (*ClassStyle, error) {
	class := Class(strings.TrimSpace(string(c)))
	if s == nil || len(s.Classes) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrUnlearned, class)
	}
	cs := s.Classes[class]
	if cs == nil {
		return nil, fmt.Errorf("%w: %s", ErrUnlearned, class)
	}
	return cs, nil
}

// renderTemplate fills the placeholders. TICKET carries its own number so the
// rendered name never depends on the template having a following {N}.
func renderTemplate(template, slug, key, number string) string {
	ticket := strings.Trim(key, "-")
	if number != "" {
		ticket += "-" + number
	}
	out := strings.ReplaceAll(template, "{KEY}", strings.Trim(key, "-"))
	out = strings.ReplaceAll(out, "{TICKET}", ticket)
	out = strings.ReplaceAll(out, "{Slug}", slug)
	out = strings.ReplaceAll(out, "{N}", number)
	return out
}

func applyCase(slug, mode, separator string) string {
	out := slug
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "upper":
		out = strings.ToUpper(out)
	case "title":
		parts := strings.Split(out, "-")
		for i, part := range parts {
			if part == "" {
				continue
			}
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			parts[i] = string(runes)
		}
		out = strings.Join(parts, "-")
	}
	if separator != "" && separator != "-" {
		out = strings.ReplaceAll(out, "-", separator)
	}
	return out
}

// slugVariants shortens prose from the tail so a validator can be satisfied
// without losing the leading token.
func slugVariants(slug string) []string {
	out := []string{slug}
	parts := strings.Split(slug, "-")
	start := len(parts) - 1
	if len(parts) > 6 {
		start = 5
	}
	for length := start; length >= 1; length-- {
		value := strings.Join(parts[:length], "-")
		if value != "" && value != slug {
			out = appendUnique(out, value)
		}
	}
	out = appendUnique(out, "change")
	return out
}

// numberVariants starts with the requested or purpose-derived number and then
// tries a bounded set of alternatives for validators with width rules.
func numberVariants(purpose string, seq int) []string {
	var out []string
	if seq > 0 {
		out = append(out, strconv.Itoa(seq))
	}
	short := 0
	if parsed, err := strconv.Atoi(shortNumber(purpose)); err == nil {
		short = parsed
	}
	if short > 0 {
		out = append(out, strconv.Itoa(short), strconv.Itoa(short+1), strconv.Itoa(short+2))
	}
	out = append(out, twoDigitNumber(purpose))
	out = append(out, plausibleNumber(purpose, 0))
	out = append(out, plausibleNumber(purpose, 1))
	out = append(out, plausibleNumber6(purpose))
	if seq > 0 {
		for step := 1; step <= 4; step++ {
			out = append(out, strconv.Itoa(seq+step))
		}
	}
	out = append(out, plausibleNumber(purpose, 2))
	out = append(out, plausibleNumber(purpose, 4))
	out = append(out, plausibleNumber(purpose, 5))
	return dedupe(out)
}

// plausibleNumber6 derives a stable six-digit ticket number.
func plausibleNumber6(purpose string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(purpose))
	return strconv.Itoa(100000 + int(hash.Sum32()%900000))
}

func twoDigitNumber(purpose string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(purpose))
	return strconv.Itoa(10 + int(hash.Sum32()%90))
}

// plausibleNumber derives a stable four- or five-digit number from purpose.
func plausibleNumber(purpose string, salt int) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(purpose))
	if salt != 0 {
		_, _ = hash.Write([]byte{0, byte(salt)})
	}
	value := hash.Sum32()
	if salt%2 == 0 {
		return strconv.Itoa(1000 + int(value%9000))
	}
	return strconv.Itoa(10000 + int(value%90000))
}

func shortNumber(purpose string) string {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(purpose))
	return strconv.Itoa(100 + int(hash.Sum32()%900))
}

func chooseTicketKey(keys []string, purpose string, seq int) string {
	if len(keys) == 0 {
		return "DEV"
	}
	if seq > 0 {
		return keys[(seq-1)%len(keys)]
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(purpose))
	return keys[int(hash.Sum32()%uint32(len(keys)))]
}

// clip enforces the length cap on the slug side and keeps a trailing file
// extension and the leading ticket.
func clip(name string, limit int) string {
	runes := []rune(name)
	if len(runes) <= limit {
		return name
	}
	suffix := ""
	if loc := fileExtension.FindStringIndex(name); loc != nil && loc[0] > 0 && loc[0] < len(name)-1 {
		suffix = name[loc[0]:]
	}
	budget := limit - len([]rune(suffix))
	if budget < 1 {
		budget, suffix = limit, ""
	}
	stem := []rune(strings.TrimSuffix(name, suffix))
	if len(stem) > budget {
		cut := budget
		for i := budget; i > 1; i-- {
			if i < len(stem) && (isSeparator(stem[i]) || strings.ContainsRune(trailingPunctuation, stem[i])) {
				cut = i
				break
			}
		}
		stem = stem[:cut]
	}
	stem = []rune(strings.TrimRight(string(stem), "-_/. "))
	out := string(stem) + suffix
	if runes := []rune(out); len(runes) > limit {
		out = string(runes[:limit])
	}
	return out
}

func caseAllowed(mode, name string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "asis":
		return true
	case "lower":
		return name == strings.ToLower(name)
	case "upper":
		return name == strings.ToUpper(name)
	case "title":
		words := strings.FieldsFunc(name, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r)
		})
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
		return lowerStarts == 0 || upperStarts >= lowerStarts*2
	}
	return true
}

func separatorAllowed(separator, name string) bool {
	if separator == "" {
		return true
	}
	others := "-_/ "
	if separator != "." {
		others = strings.ReplaceAll(others, separator, "")
	}
	if strings.ContainsAny(name, others) {
		return false
	}
	if separator == "." {
		return strings.Contains(name, ".")
	}
	if strings.Contains(name, separator) {
		return true
	}
	// A single-segment name cannot carry a separator.
	return !strings.ContainsAny(name, "-_/ ")
}

// regexSkeleton turns the literal and simple quantified parts of a validator
// back into a renderable template. It stops at constructs it cannot model.
func regexSkeleton(pattern string) string {
	pattern = strings.TrimSpace(pattern)
	pattern = strings.TrimPrefix(pattern, "^")
	pattern = strings.TrimSuffix(pattern, "$")
	for {
		switch {
		case strings.HasPrefix(pattern, "(?i)"):
			pattern = pattern[4:]
		case strings.HasPrefix(pattern, "(?m)"):
			pattern = pattern[4:]
		case strings.HasPrefix(pattern, "(?s)"):
			pattern = pattern[4:]
		default:
			goto parse
		}
	}
parse:
	var b strings.Builder
	skipNumber := false
	dropSeparator := false
	for i := 0; i < len(pattern); {
		if dropSeparator {
			if isSeparator(rune(pattern[i])) {
				i++
				continue
			}
			dropSeparator = false
		}
		switch pattern[i] {
		case '(':
			inner, next, ok := groupBody(pattern, i)
			if !ok {
				goto finish
			}
			inner = strings.TrimPrefix(inner, "?:")
			options := strings.Split(inner, "|")
			allKeys := true
			for _, option := range options {
				if !regexTicketKey.MatchString(option) {
					allKeys = false
					break
				}
			}
			switch {
			case allKeys && len(options) > 1 && nextIsNumber(pattern, next):
				// The ticket owns the number that follows it.
				b.WriteString("{TICKET}")
				skipNumber = true
				dropSeparator = true
			case len(options) > 0 && regexLiteral.MatchString(options[0]):
				b.WriteString(options[0])
			default:
				b.WriteString("{Slug}")
			}
			i = consumeQuantifier(pattern, next)
		case '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				goto finish
			}
			class := pattern[i : i+end+1]
			i = consumeQuantifier(pattern, i+end+1)
			switch {
			case strings.Contains(class, "a-z") || strings.Contains(class, "A-Z"):
				b.WriteString("{Slug}")
			case skipNumber:
				skipNumber = false
				dropSeparator = false
			default:
				b.WriteString("{N}")
			}
		case '\\':
			if i+1 >= len(pattern) {
				i++
				continue
			}
			escape := pattern[i+1]
			i += 2
			switch escape {
			case 'd':
				i = consumeQuantifier(pattern, i)
				if skipNumber {
					skipNumber = false
					dropSeparator = false
				} else {
					b.WriteString("{N}")
				}
			case 'w':
				i = consumeQuantifier(pattern, i)
				b.WriteString("{Slug}")
			case 'a', 'A', 'z', 'Z', 'b', 's':
			default:
				b.WriteByte(escape)
				i = consumeQuantifier(pattern, i)
			}
		case '.':
			b.WriteByte('.')
			i++
		case '?', '*', '+', '{', '}', '|', ')':
			i++
		default:
			b.WriteByte(pattern[i])
			i++
		}
	}
finish:
	out := b.String()
	out = strings.ReplaceAll(out, "{Slug}{Slug}", "{Slug}")
	out = strings.ReplaceAll(out, "{N}{N}", "{N}")
	return out
}

var regexLiteral = regexp.MustCompile(`^[A-Za-z0-9 _./-]+$`)

// nextIsNumber reports whether the next meaningful regex atom is a digit class.
func nextIsNumber(pattern string, i int) bool {
	for i < len(pattern) && isSeparator(rune(pattern[i])) {
		i++
	}
	if i >= len(pattern) {
		return false
	}
	switch pattern[i] {
	case '\\':
		return i+1 < len(pattern) && pattern[i+1] == 'd'
	case '[':
		end := strings.IndexByte(pattern[i:], ']')
		if end < 0 {
			return false
		}
		class := pattern[i : i+end+1]
		return !strings.Contains(class, "a-z") && !strings.Contains(class, "A-Z")
	}
	return false
}

func groupBody(pattern string, start int) (inner string, next int, ok bool) {
	depth := 0
	inClass := false
	for i := start; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			i++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '(':
			if !inClass {
				depth++
			}
		case ')':
			if !inClass {
				depth--
				if depth == 0 {
					return pattern[start+1 : i], i + 1, true
				}
			}
		}
	}
	return "", 0, false
}

func consumeQuantifier(pattern string, i int) int {
	if i >= len(pattern) {
		return i
	}
	switch pattern[i] {
	case '+', '*', '?':
		return i + 1
	case '{':
		if end := strings.IndexByte(pattern[i:], '}'); end >= 0 {
			return i + end + 1
		}
	}
	return i
}
