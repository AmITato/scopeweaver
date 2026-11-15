package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/blend"
	"github.com/Autumn-27/artex/locale"
	actool "github.com/Autumn-27/norma/tool"
)

// Style tools expose the house style described in docs/opsec-blend.md §5. The
// target's own conventions are captured as observations, scored into a grammar,
// and used as the source of every name we create: a new prefix of our own is an
// event, a name in their pattern is not.

// classNames renders the supported classes for an error message or description.
func classNames() string {
	classes := blend.Classes()
	out := make([]string, 0, len(classes))
	for _, class := range classes {
		out = append(out, string(class))
	}
	return strings.Join(out, ", ")
}

// styleRecord adds real examples of the target's own naming. Batch entries are
// preferred so one harvest does not cost one tool call per object.
func (t *ToolSet) styleRecord() actool.CoreTool {
	return writeTool(
		"style_record",
		locale.Text(t.language, "Record examples of the target's OWN naming so later names can imitate them. Use values you read from the target (branches, commit subjects, workflow files, IAM users/roles, tags, security-group names/descriptions, S3 prefixes), never names we invented.\n")+
			locale.Text(t.language, "class: one of ")+classNames()+".\n"+
			locale.Text(t.language, "value: the exact observed string. source: where you read it (e.g. github:branches, .github/workflows/jira-check.yml, iam:list-users).\n")+
			locale.Text(t.language, "More samples raise confidence; about three are needed before naming is allowed. Use entries for several observations, or the single-item fields. Returns the stored confidence per class."),
		obj(map[string]any{
			"entries": map[string]any{"type": "array", "description": locale.Text(t.language, "Batch [{class,value,source}]"), "items": obj(map[string]any{
				"class":  str(locale.Text(t.language, "Artifact class")),
				"value":  str(locale.Text(t.language, "Exact observed value")),
				"source": str(locale.Text(t.language, "Where it was read")),
			}, "class", "value")},
			"class":  str(locale.Text(t.language, "Single item: artifact class")),
			"value":  str(locale.Text(t.language, "Single item: exact observed value")),
			"source": str(locale.Text(t.language, "Single item: where it was read")),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "style_record requires task context")), nil
			}
			type entry struct {
				Class  string `json:"class"`
				Value  string `json:"value"`
				Source string `json:"source"`
			}
			var a struct {
				Entries []entry `json:"entries"`
				entry
			}
			_ = json.Unmarshal(in, &a)
			items := a.Entries
			if len(items) == 0 {
				items = []entry{a.entry}
			}
			st := blend.FromJSON(t.as.TaskHouseStyle(t.taskID))
			errs := map[string]string{}
			touched := map[blend.Class]bool{}
			accepted := 0
			for i, e := range items {
				class := blend.Class(strings.TrimSpace(e.Class))
				value := strings.TrimSpace(e.Value)
				switch {
				case !blend.ValidClass(class):
					errs[strconv.Itoa(i)] = locale.Text(locale.FromContext(ctx), "unknown class, use one of ") + classNames()
				case value == "":
					errs[strconv.Itoa(i)] = locale.Text(locale.FromContext(ctx), "value is required")
				default:
					st.Merge(blend.Observation{Class: class, Value: value, Source: strings.TrimSpace(e.Source)})
					touched[class] = true
					accepted++
				}
			}
			out := map[string]any{"accepted": accepted, "stored": map[string]float64{}}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			if accepted == 0 {
				return jsonResult(out)
			}
			if err := t.as.SetHouseStyle(t.taskID, st.JSON()); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			stored := out["stored"].(map[string]float64)
			for class := range touched {
				if cs := st.Classes[class]; cs != nil {
					stored[string(class)] = cs.Confidence
				}
			}
			return jsonResult(out)
		},
	)
}

// styleGet returns the stored grammar and what can and cannot be used to generate.
func (t *ToolSet) styleGet() actool.CoreTool {
	return readTool(
		"style_get",
		locale.Text(t.language, "Read this task's learned house style. Returns the style object plus per-class {confidence,template,regex,samples,ticket_keys,examples,usable} and an unlearned list of classes below the generate floor.\n")+
			locale.Text(t.language, "Call it before creating external objects and again after recording more samples. A class in unlearned needs style_record before blend_name will serve it."),
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "style_get requires task context")), nil
			}
			st := blend.FromJSON(t.as.TaskHouseStyle(t.taskID))
			classes := map[string]any{}
			unlearned := []string{}
			for _, class := range blend.Classes() {
				cs := st.Classes[class]
				if cs == nil {
					unlearned = append(unlearned, string(class))
					continue
				}
				usable := cs.Confidence >= blend.GenerateFloor
				classes[string(class)] = map[string]any{
					"confidence":  cs.Confidence,
					"template":    cs.Template,
					"regex":       cs.Regex,
					"samples":     cs.Samples,
					"ticket_keys": cs.TicketKeys,
					"separator":   cs.Separator,
					"case":        cs.Case,
					"examples":    cs.Examples,
					"usable":      usable,
				}
				if !usable {
					unlearned = append(unlearned, string(class))
				}
			}
			return jsonResult(map[string]any{
				"style":          st,
				"classes":        classes,
				"unlearned":      unlearned,
				"generate_floor": blend.GenerateFloor,
			})
		},
	)
}

// blendName renders a name in the learned grammar for one class.
func (t *ToolSet) blendName() actool.CoreTool {
	return readTool(
		"blend_name",
		locale.Text(t.language, "Generate a name that conforms to the target's learned conventions. Use it instead of inventing external names; never fall back to an i<NNNN>-style prefix.\n")+
			locale.Text(t.language, "class: artifact class. purpose: short description of what the object is for (it becomes the slug/subject). seq: optional ticket number or sequence override.\n")+
			locale.Text(t.language, "Returns {name,conforms,alternatives}. conforms is false when the result fails the target's own validator; use a conforming alternative or record more observations."),
		obj(map[string]any{
			"class":   str(locale.Text(t.language, "Artifact class: ") + classNames()),
			"purpose": str(locale.Text(t.language, "Short purpose of the object")),
			"seq":     intp(locale.Text(t.language, "Optional ticket number or sequence override")),
		}, "class", "purpose"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "blend_name requires task context")), nil
			}
			var a struct {
				Class   string `json:"class"`
				Purpose string `json:"purpose"`
				Seq     int    `json:"seq"`
			}
			_ = json.Unmarshal(in, &a)
			class := blend.Class(strings.TrimSpace(a.Class))
			if !blend.ValidClass(class) {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "unknown class, use one of ") + classNames()), nil
			}
			st := blend.FromJSON(t.as.TaskHouseStyle(t.taskID))
			if cs := st.Classes[class]; cs == nil || cs.Confidence < blend.GenerateFloor {
				return actool.Errorf(locale.Text(locale.FromContext(ctx),
					"style class %q is not learned yet. Record at least three real examples with style_record first (for a branch, read the repository's branches and their ticket prefixes), then call blend_name again.",
					string(class))), nil
			}
			purpose := strings.TrimSpace(a.Purpose)
			if purpose == "" {
				purpose = string(class)
			}
			name, err := st.Name(class, purpose, a.Seq)
			if err != nil {
				return actool.Errorf(locale.Text(locale.FromContext(ctx),
					"cannot generate a %s name yet (%v). Record more examples with style_record and retry.", string(class), err)), nil
			}
			conforms := st.Validate(class, name) == nil
			alts := []string{}
			seen := map[string]bool{name: true}
			for i := 1; i <= 8 && len(alts) < 3; i++ {
				cand, err := st.Name(class, purpose, a.Seq+i)
				if err != nil || seen[cand] {
					continue
				}
				seen[cand] = true
				if st.Validate(class, cand) == nil {
					alts = append(alts, cand)
				}
			}
			out := map[string]any{"name": name, "conforms": conforms, "alternatives": alts}
			if !conforms {
				note := locale.Text(locale.FromContext(ctx), "the generated name fails the stored validator; use a conforming alternative or record more observations with style_record")
				if len(alts) > 0 {
					note = locale.Text(locale.FromContext(ctx), "the generated name fails the stored validator; use one of the conforming alternatives")
				}
				out["note"] = note
			}
			return jsonResult(out)
		},
	)
}

// styleClear resets the grammar to the neutral default.
func (t *ToolSet) styleClear() actool.CoreTool {
	return writeTool(
		"style_clear",
		locale.Text(t.language, "Reset this task's house style to the neutral default. Use it when the harvest was wrong or belonged to a different target; recorded examples are dropped and must be recorded again."),
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "style_clear requires task context")), nil
			}
			if err := t.as.SetHouseStyle(t.taskID, blend.Default().JSON()); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"cleared": true})
		},
	)
}

// styleHarvest pulls observations from read-only sources in one call. GitHub is
// unauthenticated unless GITHUB_TOKEN or GH_TOKEN is set; AWS uses the CLI the
// worker already has. Failures are reported per source, never fatal to the other.
func (t *ToolSet) styleHarvest() actool.CoreTool {
	return writeTool(
		"style_harvest",
		locale.Text(t.language, "Harvest the target's naming conventions read-only and merge them into the house style: repo (owner/name) reads branches, workflow filenames, commit subjects and any branch-name regex in their CI; account reads IAM users/roles, security-group names/descriptions and Name/Project/Environment tags. Both are optional; supply at least one.\n")+
			locale.Text(t.language, "Use it once per target area before creating anything. Returns per-class observation counts and confidence; failures for one source are reported without discarding the other."),
		obj(map[string]any{
			"repo":    str(locale.Text(t.language, "GitHub repository as owner/name")),
			"account": str(locale.Text(t.language, "AWS account ID to describe")),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "style_harvest requires task context")), nil
			}
			var a struct {
				Repo    string `json:"repo"`
				Account string `json:"account"`
			}
			_ = json.Unmarshal(in, &a)
			a.Repo = strings.TrimSpace(a.Repo)
			a.Account = strings.TrimSpace(a.Account)
			if a.Repo == "" && a.Account == "" {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "style_harvest needs repo or account")), nil
			}

			var observations []blend.Observation
			errs := map[string]string{}
			if a.Repo != "" {
				owner, name, ok := strings.Cut(a.Repo, "/")
				if !ok || owner == "" || name == "" {
					errs["repo"] = "repo must be owner/name"
				} else {
					src := &blend.GitHubSource{BaseURL: os.Getenv("GITHUB_API_URL"), Token: firstNonEmpty(os.Getenv("GITHUB_TOKEN"), os.Getenv("GH_TOKEN"))}
					got, err := src.Repo(ctx, owner, name)
					if err != nil {
						errs["repo"] = err.Error()
					}
					observations = append(observations, got...)
				}
			}
			if a.Account != "" {
				run := func(ctx context.Context, args ...string) ([]byte, error) {
					return exec.CommandContext(ctx, "aws", args...).Output()
				}
				got, err := blend.HarvestAWS(ctx, run, a.Account)
				if err != nil {
					errs["account"] = err.Error()
				}
				observations = append(observations, got...)
			}

			style := blend.FromJSON(t.as.TaskHouseStyle(t.taskID))
			style = blend.MergeObservations(style, observations)
			if err := t.as.SetHouseStyle(t.taskID, style.JSON()); err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			counts := map[string]int{}
			for _, o := range observations {
				counts[string(o.Class)]++
			}
			out := map[string]any{
				"recorded":  counts,
				"total":     len(observations),
				"unlearned": unlearnedClasses(style),
			}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		},
	)
}

// unlearnedClasses lists classes that still cannot generate a name.
func unlearnedClasses(style *blend.Style) []string {
	var out []string
	for _, class := range blend.Classes() {
		if cs := style.Classes[class]; cs == nil || (cs.Confidence < blend.GenerateFloor && cs.Regex == "") {
			out = append(out, string(class))
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
