package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/locale"
	actool "github.com/Autumn-27/norma/tool"
)

// Residue tools back the external-object ledger described in docs/opsec-blend.md §6.
// A worker records every object it creates, the wrap-up clears what would be
// noticed, and the planner/operator can keep what should survive. The ledger is a
// cleanup worklist, not an audit log: an imperfect row beats no row.

func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// residueAdd records one or more created objects. Batch entries are preferred so a
// single provisioning step does not cost one tool call per object.
func (t *ToolSet) residueAdd() actool.CoreTool {
	return writeTool(
		"residue_add",
		locale.Text(t.language, "Record external objects YOU created in this run so they can be cleaned up later. Use it immediately after creating a branch, workflow, IAM user/key/role, keypair, security group, instance, tag, S3 object, host marker, scheduled task or database object.\n")+
			locale.Text(t.language, "kind: short class such as github_branch, github_workflow, iam_user, iam_key, iam_role, ec2_keypair, ec2_security_group, ec2_instance, ec2_tag, s3_object, host_marker, scheduled_task.\n")+
			locale.Text(t.language, "target: owning system (host, account, repository, bucket). locator: the exact handle needed to find it again (ref, ARN, name, key, path).\n")+
			locale.Text(t.language, "rank: obvious (a probe workflow, scratch marker, throwaway user — blocks completion), plausible (could be normal work, may be kept), keep (access we depend on, never removed). Default obvious.\n")+
			locale.Text(t.language, "removable: false when the object is load-bearing right now. Use entries for several objects, or the single-item fields."),
		obj(map[string]any{
			"entries":   map[string]any{"type": "array", "description": locale.Text(t.language, "Batch [{kind,target,locator,rank,removable,fiction}]"), "items": map[string]any{"type": "object"}},
			"kind":      str(locale.Text(t.language, "Single item: object class")),
			"target":    str(locale.Text(t.language, "Single item: owning system")),
			"locator":   str(locale.Text(t.language, "Single item: exact handle to find it again")),
			"rank":      str(locale.Text(t.language, "Single item: obvious/plausible/keep (default obvious)")),
			"fiction":   str(locale.Text(t.language, "Single item: which cover story this object supports")),
			"removable": boolSchema(locale.Text(t.language, "Single item: false when load-bearing")),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "residue_add requires task context")), nil
			}
			type entry struct {
				Kind      string `json:"kind"`
				Target    string `json:"target"`
				Locator   string `json:"locator"`
				Rank      string `json:"rank"`
				Fiction   string `json:"fiction"`
				Removable *bool  `json:"removable"`
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
			var added []map[string]any
			errs := map[string]string{}
			for i, e := range items {
				removable := true
				if e.Removable != nil {
					removable = *e.Removable
				}
				id, err := t.as.AddResidue(t.taskID, e.Kind, e.Target, e.Locator, e.Fiction, e.Rank, removable)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				added = append(added, map[string]any{"id": id})
			}
			out := map[string]any{"added": added}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		},
	)
}

// residueList returns the ledger, open rows first.
func (t *ToolSet) residueList() actool.CoreTool {
	return readTool(
		"residue_list",
		locale.Text(t.language, "List this task's residue ledger. Open rows come first. Use it before concluding a wrap-up, and to find the exact locator of a row you are about to remove.\n")+
			locale.Text(t.language, "Returns {residue:[{id,kind,target,locator,rank,removable,removed_at}],open_obvious}. include_removed also returns cleaned rows."),
		obj(map[string]any{
			"include_removed": boolSchema(locale.Text(t.language, "Also return rows already marked removed")),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "residue_list requires task context")), nil
			}
			var a struct {
				IncludeRemoved bool `json:"include_removed"`
			}
			_ = json.Unmarshal(in, &a)
			rows, err := t.as.ListResidue(t.taskID, a.IncludeRemoved)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			open, _, err := t.as.ResidueCompletionGate(t.taskID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"residue": rows, "open_obvious": open})
		},
	)
}

// residueSet edits rank, removable or locator.
func (t *ToolSet) residueSet() actool.CoreTool {
	return writeTool(
		"residue_set",
		locale.Text(t.language, "Change a residue row. Use rank=keep for access that must survive, rank=plausible when the object plausibly belongs, or removable=false while it is still load-bearing.\n")+
			locale.Text(t.language, "Returns the updated row."),
		obj(map[string]any{
			"id":        intp(locale.Text(t.language, "Residue row id")),
			"rank":      str(locale.Text(t.language, "obvious/plausible/keep")),
			"removable": boolSchema(locale.Text(t.language, "false when load-bearing")),
			"locator":   str(locale.Text(t.language, "Corrected locator")),
			"fiction":   str(locale.Text(t.language, "Corrected cover-story note")),
		}, "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "residue_set requires task context")), nil
			}
			var a struct {
				ID        int64   `json:"id"`
				Rank      *string `json:"rank"`
				Removable *bool   `json:"removable"`
				Locator   *string `json:"locator"`
				Fiction   *string `json:"fiction"`
			}
			_ = json.Unmarshal(in, &a)
			row, err := t.as.SetResidue(a.ID, residuePatch(a.Rank, a.Removable, a.Locator, a.Fiction))
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(row)
		},
	)
}

// residueRemove marks an object cleaned up.
func (t *ToolSet) residueRemove() actool.CoreTool {
	return writeTool(
		"residue_remove",
		locale.Text(t.language, "Mark a residue row cleaned up after you actually removed the object (deleted branch, user, keypair, object, marker). Idempotent. Returns the row."),
		obj(map[string]any{
			"id": intp(locale.Text(t.language, "Residue row id")),
		}, "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "residue_remove requires task context")), nil
			}
			var a struct {
				ID int64 `json:"id"`
			}
			_ = json.Unmarshal(in, &a)
			row, err := t.as.RemoveResidue(a.ID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(row)
		},
	)
}

// residueKeepAll is the one-shot override when the operator accepts the footprint.
func (t *ToolSet) residueKeepAll() actool.CoreTool {
	return writeTool(
		"residue_keep_all",
		locale.Text(t.language, "Downgrade every open residue row to rank=keep, so completion is no longer blocked. Use it when the remaining footprint is accepted as-is."),
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil || t.taskID <= 0 {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "residue_keep_all requires task context")), nil
			}
			n, err := t.as.KeepAllResidue(t.taskID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"kept": n})
		},
	)
}

func residuePatch(rank *string, removable *bool, locator, fiction *string) db.ResiduePatch {
	return db.ResiduePatch{Rank: rank, Removable: removable, Locator: locator, Fiction: fiction}
}

// residueBlocker reports whether the completion gate is holding and returns a
// compact list for the prove_goal reply.
func (t *ToolSet) residueBlocker(ctx context.Context) (bool, string) {
	if t.as == nil || t.taskID <= 0 {
		return false, ""
	}
	open, blocked, err := t.as.ResidueCompletionGate(t.taskID)
	if err != nil || !blocked {
		return false, ""
	}
	var b strings.Builder
	if rows, err := t.as.OpenObviousResidue(t.taskID, 5); err == nil {
		for _, r := range rows {
			fmt.Fprintf(&b, "\n- #%d %s %s", r.ID, r.Kind, r.Locator)
		}
	}
	return true, fmt.Sprintf(locale.Text(locale.FromContext(ctx),
		"%d obvious residue object(s) are still open. Remove them with residue_remove (%s), or call residue_keep_all to accept the footprint."),
		open, strings.TrimSpace(b.String()))
}
