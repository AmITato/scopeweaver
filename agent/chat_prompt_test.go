package agent

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/Autumn-27/artex/locale"
)

func TestChatPromptPreservesRoleOnMissingOrInvalidTemplate(t *testing.T) {
	previous := PromptOverride
	t.Cleanup(func() { PromptOverride = previous })

	roles := map[string]string{
		"auto": autoDefaultTmpl, "pentest": pentestDefaultTmpl,
		"reporter": ReporterDefaultPrompt, "retester": RetesterDefaultPrompt,
		"custom": DefaultAssistantPrompt,
	}
	for role, fallback := range roles {
		for _, lang := range locale.Languages() {
			for _, tc := range []struct {
				name, body string
				exists     bool
			}{
				{name: "missing"},
				{name: "empty", exists: true},
				{name: "unknown variable", body: "{{.UnknownVariable}}", exists: true},
				{name: "invalid syntax", body: "{{if}}", exists: true},
			} {
				t.Run(role+"/"+string(lang)+"/"+tc.name, func(t *testing.T) {
					PromptOverride = func(string) (string, bool) { return tc.body, tc.exists }
					got := chatSystem(role, "/data", "/work", lang)
					want := locale.Text(lang, fallback) + chatWorkDirSpec("/work", lang)
					if got != want {
						t.Fatal("chat lost its role default or file output rules")
					}
				})
			}
			t.Run(role+"/"+string(lang)+"/edited template", func(t *testing.T) {
				PromptOverride = func(string) (string, bool) { return "EDITED {{.DataDir}} {{.Now}}", true }
				got := chatSystem(role, "/data", "/work", lang)
				if !strings.HasPrefix(got, "EDITED /data ") || strings.Contains(got, "{{") || !strings.HasSuffix(got, chatWorkDirSpec("/work", lang)) {
					t.Fatal("valid edited prompt was not rendered intact")
				}
			})
		}
	}
}

func TestRenderSystemReportsInvalidTemplateWithoutLoggingPrompt(t *testing.T) {
	previous := PromptOverride
	output := log.Writer()
	t.Cleanup(func() {
		PromptOverride = previous
		log.SetOutput(output)
	})
	var logs bytes.Buffer
	log.SetOutput(&logs)
	for _, lang := range locale.Languages() {
		for _, tc := range []struct {
			name, body, diagnostic string
		}{
			{name: "text", body: "PRIVATE_PROMPT_TEXT {{.UnknownVariable}}", diagnostic: "[prompt] agent=%q: template execution failed; using role default"},
			{name: "parse expression", body: "{{PRIVATE_PROMPT_TEXT}}", diagnostic: "[prompt] agent=%q: template parsing failed; using role default"},
			{name: "execute expression", body: `{{index .DataDir "PRIVATE_PROMPT_TEXT"}}`, diagnostic: "[prompt] agent=%q: template execution failed; using role default"},
		} {
			t.Run(string(lang)+"/"+tc.name, func(t *testing.T) {
				PromptOverride = func(string) (string, bool) { return tc.body, true }
				logs.Reset()
				if got := chatSystem("pentest", "/data", "/work", lang); !strings.HasPrefix(got, locale.Text(lang, pentestDefaultTmpl)) {
					t.Fatal("invalid prompt lost the pentest role")
				}
				message, ok := locale.Raw(lang, tc.diagnostic)
				if !ok || !strings.Contains(logs.String(), fmt.Sprintf(message, "pentest")) {
					t.Fatalf("invalid prompt did not produce a translated log entry for %s", lang)
				}
				if strings.Contains(logs.String(), "PRIVATE_PROMPT_TEXT") {
					t.Fatal("render failure logged the prompt body")
				}
			})
		}
	}
}

// TestChatSystemOpsecTail: chatSystem stays byte-identical for pentest, while an
// op task appends the static stealth tail after the file output rules and before
// the caller's output-language instruction.
func TestChatSystemOpsecTail(t *testing.T) {
	previous := PromptOverride
	PromptOverride = nil
	t.Cleanup(func() { PromptOverride = previous })

	for _, role := range []string{"auto", "pentest", "reporter", "retester", "custom"} {
		for _, lang := range locale.Languages() {
			pentest := chatSystem(role, "/data", "/work", lang)
			if strings.Contains(pentest, "Stealth operation") {
				t.Fatalf("%s/%s pentest chat got an opsec tail", role, lang)
			}
			op := chatSystemFor(role, "/data", "/work", ProfileOp, lang)
			want := pentest + opsecBlock(ProfileOp, lang)
			if op != want {
				t.Fatalf("%s/%s op chat tail is not exactly the code-owned block", role, lang)
			}
			if strings.Index(op, chatWorkDirSpec("/work", lang)) > strings.Index(op, opsecBlock(ProfileOp, lang)) {
				t.Fatalf("%s/%s opsec tail precedes the file output rules", role, lang)
			}
		}
	}
}
