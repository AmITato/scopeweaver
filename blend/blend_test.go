package blend

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func observations(class Class, values ...string) []Observation {
	out := make([]Observation, 0, len(values))
	for _, value := range values {
		out = append(out, Observation{Class: class, Value: value, Source: "test harvest"})
	}
	return out
}

func classStyle(t *testing.T, style *Style, class Class) *ClassStyle {
	t.Helper()
	cs := style.Classes[class]
	if cs == nil {
		t.Fatalf("class %s missing from parsed style", class)
	}
	return cs
}

// The engagement's real conventions are the fixture: a ticket-prefixed title
// branch, a plain lowercase workflow, a lower dashed IAM user, a title tag and a
// lowercase SG description sentence.
func TestParseRealConventions(t *testing.T) {
	cases := []struct {
		name      string
		class     Class
		values    []string
		template  string
		separator string
		caseMode  string
		key       string
	}{
		{
			name:      "branch",
			class:     ClassBranch,
			values:    []string{"DEV-38865-AR-Process-Payment-Plan-2.0-to-6.0", "DEV-38866-AR-Process-Refund-Plan-1.0-to-3.0"},
			template:  "{TICKET}-{Slug}",
			separator: "-",
			caseMode:  "title",
			key:       "DEV",
		},
		{
			name:      "workflow",
			class:     ClassWorkflow,
			values:    []string{"crm-contact-search-tests.yml", "deploy-staging-api.yml"},
			template:  "{Slug}.yml",
			separator: "-",
			caseMode:  "lower",
		},
		{
			name:      "iam user",
			class:     ClassIAMUser,
			values:    []string{"artifact-deployer-production", "artifact-viewer-staging"},
			template:  "{Slug}",
			separator: "-",
			caseMode:  "lower",
		},
		{
			name:      "tag",
			class:     ClassTagValue,
			values:    []string{"Tier1 Web Server", "Tier2 Database Primary"},
			template:  "Tier{N} {Slug}",
			separator: " ",
			caseMode:  "title",
		},
		{
			name:      "security group description",
			class:     ClassSGDescription,
			values:    []string{"allows inbound https from the office network to the web tier.", "allows outbound smtp to the mail relay in the dmz."},
			template:  "{Slug}.",
			separator: " ",
			caseMode:  "lower",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			style := Parse(observations(tc.class, tc.values...))
			cs := classStyle(t, style, tc.class)
			if cs.Template != tc.template {
				t.Errorf("template = %q, want %q", cs.Template, tc.template)
			}
			if cs.Separator != tc.separator {
				t.Errorf("separator = %q, want %q", cs.Separator, tc.separator)
			}
			if cs.Case != tc.caseMode {
				t.Errorf("case = %q, want %q", cs.Case, tc.caseMode)
			}
			if cs.Confidence < GenerateFloor {
				t.Errorf("confidence %.3f below GenerateFloor after two agreeing samples", cs.Confidence)
			}
			if len(cs.Examples) != len(tc.values) {
				t.Errorf("examples = %d, want %d", len(cs.Examples), len(tc.values))
			}
			if tc.key != "" {
				found := false
				for _, key := range cs.TicketKeys {
					if key == tc.key {
						found = true
					}
				}
				if !found {
					t.Errorf("ticket keys = %v, want %q", cs.TicketKeys, tc.key)
				}
			}
		})
	}
}

func TestGeneratedNamesFollowRealConventions(t *testing.T) {
	style := Parse(append(append(append(append(
		observations(ClassBranch, "DEV-38865-AR-Process-Payment-Plan-2.0-to-6.0", "DEV-38866-AR-Process-Refund-Plan-1.0-to-3.0"),
		observations(ClassWorkflow, "crm-contact-search-tests.yml", "deploy-staging-api.yml")...),
		observations(ClassIAMUser, "artifact-deployer-production", "artifact-viewer-staging")...),
		observations(ClassTagValue, "Tier1 Web Server", "Tier2 Database Primary")...),
		observations(ClassSGDescription, "allows inbound https from the office network to the web tier.", "allows outbound smtp to the mail relay in the dmz.")...))

	cases := []struct {
		class   Class
		purpose string
		check   func(*testing.T, string)
	}{
		{
			class:   ClassBranch,
			purpose: "Payment Plan 2.0 to 6.0",
			check: func(t *testing.T, name string) {
				if !strings.HasPrefix(name, "DEV-4242-") {
					t.Errorf("branch %q lost its ticket prefix", name)
				}
			},
		},
		{
			class:   ClassWorkflow,
			purpose: "crm contact search tests",
			check: func(t *testing.T, name string) {
				if !strings.HasSuffix(name, ".yml") {
					t.Errorf("workflow %q lost its extension", name)
				}
				if name != strings.ToLower(name) {
					t.Errorf("workflow %q is not lowercase", name)
				}
			},
		},
		{
			class:   ClassIAMUser,
			purpose: "artifact deployer production",
			check: func(t *testing.T, name string) {
				if name != strings.ToLower(name) || !strings.Contains(name, "-") {
					t.Errorf("IAM user %q broke the lowercase dashed convention", name)
				}
			},
		},
		{
			class:   ClassTagValue,
			purpose: "web server",
			check: func(t *testing.T, name string) {
				if !strings.HasPrefix(name, "Tier") || !strings.Contains(name, " ") {
					t.Errorf("tag %q is not a tiered title value: %q", name, name)
				}
			},
		},
		{
			class:   ClassSGDescription,
			purpose: "allows inbound https from the office network",
			check: func(t *testing.T, name string) {
				if name != strings.ToLower(name) || !strings.HasSuffix(name, ".") {
					t.Errorf("description %q is not a lowercase sentence", name)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.class), func(t *testing.T) {
			name, err := style.Name(tc.class, tc.purpose, 4242)
			if err != nil {
				t.Fatalf("Name: %v", err)
			}
			if utf8.RuneCountInString(name) > maxNameLength {
				t.Errorf("name is %d chars: %q", utf8.RuneCountInString(name), name)
			}
			if err := style.Validate(tc.class, name); err != nil {
				t.Errorf("Validate(%q): %v", name, err)
			}
			tc.check(t, name)
		})
	}
}

func TestNameRefusesUnlearned(t *testing.T) {
	one := Parse(observations(ClassBranch, "DEV-38865-AR-Process-Payment-Plan"))
	if _, err := one.Name(ClassBranch, "payment", 0); !errors.Is(err, ErrUnlearned) {
		t.Fatalf("one sample: err = %v, want ErrUnlearned", err)
	}

	if _, err := Default().Name(ClassWorkflow, "deploy", 0); !errors.Is(err, ErrUnlearned) {
		t.Fatalf("default: err = %v, want ErrUnlearned", err)
	}
	if err := Default().Validate(ClassWorkflow, "deploy-staging.yml"); !errors.Is(err, ErrUnlearned) {
		t.Fatalf("default validate: err = %v, want ErrUnlearned", err)
	}

	damaged := Parse(observations(ClassBranch, "!!!", "...", "   "))
	cs := classStyle(t, damaged, ClassBranch)
	if cs.Template != "" {
		t.Errorf("structureless class invented template %q", cs.Template)
	}
	if _, err := damaged.Name(ClassBranch, "payment", 0); !errors.Is(err, ErrUnlearned) {
		t.Fatalf("structureless class: err = %v, want ErrUnlearned", err)
	}

	separators := Parse(observations(ClassBranch, "---", "___"))
	sepStyle := classStyle(t, separators, ClassBranch)
	if sepStyle.Template != "" || sepStyle.Separator != "-" {
		t.Errorf("fallback = template %q separator %q, want empty template and dash", sepStyle.Template, sepStyle.Separator)
	}
	if _, err := separators.Name(ClassBranch, "payment", 0); !errors.Is(err, ErrUnlearned) {
		t.Fatalf("fallback class: err = %v, want ErrUnlearned", err)
	}
}

func TestRegexIsAuthoritativeAndRetried(t *testing.T) {
	validator := `^(DEV|TS)-[0-9]{5}-[a-z]+(?:-[a-z]+){1,2}$`
	obs := []Observation{
		{Class: ClassBranch, Value: "DEV-38865-AR-Payment-Plan", Source: "regex:" + validator},
		{Class: ClassBranch, Value: "MKB-38866-AR-Refund-Plan", Source: "git branches"},
	}
	style := Parse(obs)
	cs := classStyle(t, style, ClassBranch)
	if cs.Regex != validator {
		t.Fatalf("regex = %q, want %q", cs.Regex, validator)
	}
	if len(cs.TicketKeys) == 0 {
		t.Fatal("ticket keys should be recovered from the validator")
	}

	name, err := style.Name(ClassBranch, "invoice export", 1)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	re := regexp.MustCompile(validator)
	if !re.MatchString(name) {
		t.Fatalf("generated %q does not match %s (retries did not run)", name, validator)
	}
	if err := style.Validate(ClassBranch, name); err != nil {
		t.Errorf("Validate(%q): %v", name, err)
	}
}

func TestRegexOnlyObservationCanGenerate(t *testing.T) {
	validator := `^(DEV|TS)-[0-9]{4}-[a-z-]+$`
	style := Parse([]Observation{{
		Class:  ClassBranch,
		Source: "regex:" + validator,
	}})
	cs := classStyle(t, style, ClassBranch)
	if cs.Confidence >= GenerateFloor {
		t.Fatalf("one regex-only sample should be below GenerateFloor: %.3f", cs.Confidence)
	}
	name, err := style.Name(ClassBranch, "audit trail", 1)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !regexp.MustCompile(validator).MatchString(name) {
		t.Fatalf("generated %q does not match %s", name, validator)
	}
}

func TestRegexRetriesAcrossTicketWidths(t *testing.T) {
	for width := 2; width <= 6; width++ {
		t.Run(fmt.Sprintf("width-%d", width), func(t *testing.T) {
			validator := fmt.Sprintf(`^(DEV|TS)-[0-9]{%d}-[a-z-]+$`, width)
			style := Parse([]Observation{{
				Class:  ClassBranch,
				Source: "regex:" + validator,
			}})
			name, err := style.Name(ClassBranch, "audit log", 1)
			if err != nil {
				t.Fatalf("Name: %v", err)
			}
			if !regexp.MustCompile(validator).MatchString(name) {
				t.Fatalf("%q does not match %s", name, validator)
			}
		})
	}
}

func TestRegexShortensLongSlugToFit(t *testing.T) {
	style := Parse([]Observation{{
		Class:  ClassBranch,
		Value:  "DEV-38865-AR-Payment",
		Source: "regex:^DEV-[0-9]{4}-[a-z]+$",
	}})
	purpose := strings.Repeat("incremental payment processing reconciliation ledger export ", 4)
	name, err := style.Name(ClassBranch, purpose, 7)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !regexp.MustCompile(`^DEV-[0-9]{4}-[a-z]+$`).MatchString(name) {
		t.Fatalf("long purpose not shortened for the validator: %q", name)
	}
}

func TestRegexNoCandidate(t *testing.T) {
	style := Default()
	style.Classes = map[Class]*ClassStyle{}
	style.Classes[ClassBranch] = &ClassStyle{
		Samples:    4,
		Template:   "{Slug}",
		Regex:      `^(DEV)-[0-9]+-[A-Z]{30,}$`,
		Separator:  "-",
		Case:       "asis",
		Confidence: 1,
	}
	if _, err := style.Name(ClassBranch, "billing", 5); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("err = %v, want ErrNoCandidate", err)
	}
}

func TestValidateRejectsMismatches(t *testing.T) {
	style := Parse(observations(ClassBranch,
		"DEV-38865-AR-Process-Payment-Plan",
		"DEV-38866-AR-Process-Refund-Plan"))

	if err := style.Validate(ClassBranch, "DEV-38867-AR-Payment-Plan"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	if err := style.Validate(ClassBranch, "dev-38867-ar-Payment-Plan"); !errors.Is(err, ErrNonConforming) {
		t.Fatalf("wrong case: err = %v, want ErrNonConforming", err)
	}
	if err := style.Validate(ClassBranch, "DEV_38867_AR_Payment_Plan"); !errors.Is(err, ErrNonConforming) {
		t.Fatalf("wrong separator: err = %v, want ErrNonConforming", err)
	}
	if err := style.Validate(ClassBranch, ""); !errors.Is(err, ErrNonConforming) {
		t.Fatalf("empty name: err = %v, want ErrNonConforming", err)
	}

	guarded := style.Clone()
	guarded.Classes[ClassBranch].Regex = `^DEV-[0-9]{4}-[a-z-]+$`
	if err := guarded.Validate(ClassBranch, "DEV-1234-Payment-Plan"); !errors.Is(err, ErrNonConforming) {
		t.Fatalf("validator mismatch: err = %v, want ErrNonConforming", err)
	}
	if err := guarded.Validate(ClassBranch, "DEV-1234-payment-plan"); err != nil {
		t.Fatalf("validator match rejected: %v", err)
	}
}

func TestNameTruncatesSlugAndKeepsTicket(t *testing.T) {
	style := Parse(observations(ClassBranch,
		"DEV-38865-AR-Process-Payment-Plan",
		"DEV-38866-AR-Process-Refund-Plan"))
	purpose := strings.Repeat("incremental payment processing reconciliation ", 8)
	name, err := style.Name(ClassBranch, purpose, 9876)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if got := utf8.RuneCountInString(name); got > 100 {
		t.Fatalf("name has %d chars: %q", got, name)
	}
	if !strings.HasPrefix(name, "DEV-9876-") {
		t.Fatalf("ticket not kept: %q", name)
	}
	if err := style.Validate(ClassBranch, name); err != nil {
		t.Errorf("truncated name no longer validates: %v", err)
	}
}

func TestNameIsDeterministic(t *testing.T) {
	obs := observations(ClassBranch,
		"DEV-38865-AR-Process-Payment-Plan",
		"DEV-38866-AR-Process-Refund-Plan")
	one := Parse(obs)
	two := Parse(obs)

	first, err := one.Name(ClassBranch, "Payment plan 2.0", 0)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	for i := 0; i < 10; i++ {
		again, err := one.Name(ClassBranch, "Payment plan 2.0", 0)
		if err != nil {
			t.Fatalf("repeat Name: %v", err)
		}
		if again != first {
			t.Fatalf("repeat %d = %q, want %q", i, again, first)
		}
	}
	other, err := two.Name(ClassBranch, "Payment plan 2.0", 0)
	if err != nil {
		t.Fatalf("second style Name: %v", err)
	}
	if other != first {
		t.Fatalf("separate parse = %q, want %q", other, first)
	}
}

func TestMergeRedrivesAndCapsExamples(t *testing.T) {
	style := Default()
	for i := 0; i < 9; i++ {
		style.Merge(Observation{
			Class:  ClassBranch,
			Value:  fmt.Sprintf("DEV-%d-AR-Payment-Plan-%d", 1000+i, i),
			Source: "git",
		})
	}
	cs := classStyle(t, style, ClassBranch)
	if len(cs.Examples) > 5 {
		t.Fatalf("examples = %d, want at most 5", len(cs.Examples))
	}
	if cs.Template != "{TICKET}-{Slug}" {
		t.Fatalf("merge did not re-derive the template: %q", cs.Template)
	}
	if cs.Confidence < GenerateFloor {
		t.Fatalf("merge confidence %.3f below floor", cs.Confidence)
	}
	if len(style.Sources) != 1 {
		t.Fatalf("sources = %v, want one deduplicated source", style.Sources)
	}

	before := len(cs.Examples)
	for i := 0; i < 30; i++ {
		style.Merge(Observation{Class: ClassBranch, Value: "DEV-9000-AR-New-Plan", Source: "git"})
	}
	if got := len(classStyle(t, style, ClassBranch).Examples); got != before {
		t.Fatalf("repeated merge grew examples from %d to %d", before, got)
	}
	if len(style.Sources) != 1 {
		t.Fatalf("repeated merge duplicated sources: %v", style.Sources)
	}
}

func TestMergeFromDefaultGenerates(t *testing.T) {
	style := Default()
	style.Merge(Observation{Class: ClassWorkflow, Value: "crm-contact-search-tests.yml", Source: "git"})
	if _, err := style.Name(ClassWorkflow, "contact search tests", 0); !errors.Is(err, ErrUnlearned) {
		t.Fatalf("one merge: err = %v, want ErrUnlearned", err)
	}
	style.Merge(Observation{Class: ClassWorkflow, Value: "deploy-staging-api.yml", Source: "git"})
	name, err := style.Name(ClassWorkflow, "contact search tests", 0)
	if err != nil {
		t.Fatalf("two merges: %v", err)
	}
	if !strings.HasSuffix(name, ".yml") {
		t.Errorf("merged workflow %q lost its extension", name)
	}
}

func TestMergeRegexWithoutValue(t *testing.T) {
	style := Default()
	style.Merge(Observation{Class: ClassBranch, Source: "regex:^(DEV)-[0-9]{4}-[a-z-]+$"})
	cs := classStyle(t, style, ClassBranch)
	if cs.Regex == "" {
		t.Fatal("regex not stored")
	}
	if len(style.Sources) != 1 || !strings.Contains(style.Sources[0], "regex:") {
		t.Fatalf("regex source = %v, want it retained", style.Sources)
	}
	name, err := style.Name(ClassBranch, "audit log", 1)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !regexp.MustCompile(cs.Regex).MatchString(name) {
		t.Fatalf("%q does not match %s", name, cs.Regex)
	}
}

func TestParseToleratesDamage(t *testing.T) {
	long := strings.Repeat("some-long-segment-", 400) + "deploy.yml"
	style := Parse([]Observation{
		{Class: ClassBranch, Value: ""},
		{Class: ClassBranch, Value: "   "},
		{Class: ClassBranch, Value: "!!!"},
		{Class: ClassBranch, Value: "DEV-38865-AR-Payment-Plan", Source: "git"},
		{Class: "", Value: "orphan"},
		{Class: "future-class", Value: "something", Source: "future"},
		{Class: ClassWorkflow, Value: long, Source: "fs"},
	})

	if style == nil {
		t.Fatal("Parse returned nil")
	}
	branch := classStyle(t, style, ClassBranch)
	if branch.Samples != 4 {
		t.Errorf("samples = %d, want 4", branch.Samples)
	}
	if branch.Template != "{TICKET}-{Slug}" {
		t.Errorf("template = %q, want {TICKET}-{Slug}", branch.Template)
	}
	if len(branch.Examples) != 2 {
		t.Errorf("examples = %d, want the usable sample plus the raw damage", len(branch.Examples))
	}
	workflow := classStyle(t, style, ClassWorkflow)
	if workflow.Template != "{Slug}.yml" {
		t.Errorf("long workflow template = %q", workflow.Template)
	}
	if style.Classes["future-class"] != nil {
		t.Error("unknown class should be ignored")
	}
	if !Parse(nil).Empty() {
		t.Error("Parse(nil) should be empty")
	}
}

func TestNameWithoutPurposeStillRenders(t *testing.T) {
	style := Parse(observations(ClassIAMUser, "artifact-deployer-production", "artifact-viewer-staging"))
	first, err := style.Name(ClassIAMUser, "", 0)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	second, err := style.Name(ClassIAMUser, "", 0)
	if err != nil {
		t.Fatalf("repeat Name: %v", err)
	}
	if first != second || first != "change" {
		t.Fatalf("empty purpose = %q, %q; want stable change", first, second)
	}
}

func TestMergeHonoursListedTicketKey(t *testing.T) {
	style := Default()
	style.Classes = map[Class]*ClassStyle{
		ClassBranch: {TicketKeys: []string{"dev"}},
	}
	style.Merge(Observation{Class: ClassBranch, Value: "dev-123-payment-plan", Source: "git"})
	style.Merge(Observation{Class: ClassBranch, Value: "dev-124-refund-plan", Source: "git"})
	cs := classStyle(t, style, ClassBranch)
	if cs.Template != "{TICKET}-{Slug}" {
		t.Fatalf("listed lowercase key not treated as a ticket: %q", cs.Template)
	}
	name, err := style.Name(ClassBranch, "refund plan", 1)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !strings.HasPrefix(name, "dev-1-") {
		t.Fatalf("listed key not used: %q", name)
	}
}

func TestNumericSlotUsesSeqWhenGiven(t *testing.T) {
	style := Default()
	style.Classes = map[Class]*ClassStyle{
		ClassTagValue: {
			Samples: 3, Template: "{N} {Slug}", Separator: " ", Case: "asis", Confidence: 1,
		},
	}
	withSeq, err := style.Name(ClassTagValue, "web server", 9)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !strings.HasPrefix(withSeq, "9 ") {
		t.Fatalf("seq not used in {N}: %q", withSeq)
	}
	withoutSeq, err := style.Name(ClassTagValue, "web server", 0)
	if err != nil {
		t.Fatalf("Name: %v", err)
	}
	if !regexp.MustCompile(`^[0-9]{3} `).MatchString(withoutSeq) {
		t.Fatalf("short number not used in {N}: %q", withoutSeq)
	}
}

func TestRegexSkeleton(t *testing.T) {
	cases := map[string]string{
		`^(DEV|TS)-[0-9]{2,5}-[a-z0-9-]+$`: "{TICKET}-{Slug}",
		`^sw-[a-z]+$`:                      "sw-{Slug}",
		`^release-[0-9]+\.tar\.gz$`:        "release-{N}.tar.gz",
	}
	for pattern, want := range cases {
		if got := regexSkeleton(pattern); got != want {
			t.Errorf("regexSkeleton(%q) = %q, want %q", pattern, got, want)
		}
	}
}
