package agent

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/locale"
	actool "github.com/Autumn-27/norma/tool"
)

// phaseSet advances the engagement phase. An op task starts passive: only
// intents tagged phase=passive are claimable, so reconnaissance that touches
// nothing runs first. Advancing to active unlocks the rest.
func (t *ToolSet) phaseSet() actool.CoreTool {
	return writeTool(
		"phase_set",
		locale.Text(t.language, "Set this task's engagement phase.\n")+
			locale.Text(t.language, "passive: only intents tagged phase=passive run (silent reconnaissance: certificate transparency, DNS history, archives, code and credential leaks, public listings).\n")+
			locale.Text(t.language, "active: normal execution; anything reachable is fair game.\n")+
			locale.Text(t.language, "Advance to active once passive work is exhausted or explicitly skipped, or when the objective already needs a touch. Returns the stored phase and the count of intents still waiting on the phase."),
		obj(map[string]any{
			"phase": str(locale.Text(t.language, "passive, active or persisted")),
		}, "phase"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "phase_set requires task context")), nil
			}
			var a struct {
				Phase string `json:"phase"`
			}
			_ = json.Unmarshal(in, &a)
			phase, err := t.as.SetTaskPhase(t.taskID, a.Phase)
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			out := map[string]any{"phase": phase}
			if phase == db.TaskPhasePassive {
				if fr, err := t.ts.Frontier(200); err == nil {
					waiting := 0
					for _, n := range fr {
						if db.IntentPhase(n) != db.TaskPhasePassive {
							waiting++
						}
					}
					out["waiting_active_intents"] = waiting
				}
			}
			return jsonResult(out)
		},
	)
}
