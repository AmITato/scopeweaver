package db

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/Autumn-27/artex/locale"
)

// Residue ranks. Obvious residue is a mark anyone would notice (probe workflow,
// scratch marker, throwaway user); plausible residue can pass as normal work; keep
// is load-bearing access that must survive the wrap-up. See docs/opsec-blend.md §6.
const (
	ResidueRankObvious   = "obvious"
	ResidueRankPlausible = "plausible"
	ResidueRankKeep      = "keep"
)

// ErrResidueOpen reports completion refused because obvious residue is still open.
var ErrResidueOpen = locale.NewError("open residue remains")

// Residue is one created external object tracked for cleanup.
type Residue struct {
	ID                int64      `json:"id"`
	TaskID            int64      `json:"task_id"`
	Kind              string     `json:"kind"`
	Target            string     `json:"target"`
	Locator           string     `json:"locator"`
	Fiction           string     `json:"fiction"`
	Rank              string     `json:"rank"`
	Removable         bool       `json:"removable"`
	EvidenceFindingID *int64     `json:"evidence_finding_id,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	RemovedAt         *time.Time `json:"removed_at,omitempty"`
}

// NormalizeResidueRank validates a rank, defaulting empty to obvious.
func NormalizeResidueRank(rank string) (string, bool) {
	switch strings.TrimSpace(rank) {
	case "", ResidueRankObvious:
		return ResidueRankObvious, true
	case ResidueRankPlausible:
		return ResidueRankPlausible, true
	case ResidueRankKeep:
		return ResidueRankKeep, true
	}
	return "", false
}

// AddResidue records one created object. target is the system that owns it (host,
// account, repository) and locator the exact handle needed to find it again.
func (d *DB) AddResidue(taskID int64, kind, target, locator, fiction, rank string, removable bool) (int64, error) {
	if taskID <= 0 {
		return 0, locale.Errorf("residue requires task context")
	}
	kind = strings.TrimSpace(kind)
	target = strings.TrimSpace(target)
	locator = strings.TrimSpace(locator)
	if kind == "" || locator == "" {
		return 0, locale.Errorf("residue requires kind and locator")
	}
	normalized, ok := NormalizeResidueRank(rank)
	if !ok {
		return 0, locale.Errorf("residue rank must be obvious, plausible or keep")
	}
	var id int64
	err := d.QueryRow(`INSERT INTO task_residue(task_id, kind, target, locator, fiction, rank, removable)
VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		taskID, kind, target, locator, strings.TrimSpace(fiction), normalized, removable).Scan(&id)
	return id, err
}

const residueCols = `id, task_id, kind, target, locator, fiction, rank, removable,
COALESCE(evidence_finding_id, 0), created_at, removed_at`

func scanResidue(sc interface{ Scan(...any) error }) (*Residue, error) {
	var r Residue
	var evidence int64
	if err := sc.Scan(&r.ID, &r.TaskID, &r.Kind, &r.Target, &r.Locator, &r.Fiction, &r.Rank,
		&r.Removable, &evidence, &r.CreatedAt, &r.RemovedAt); err != nil {
		return nil, err
	}
	if evidence > 0 {
		r.EvidenceFindingID = &evidence
	}
	return &r, nil
}

// ListResidue returns a task's ledger, newest first. Removed rows are skipped
// unless includeRemoved is set.
func (d *DB) ListResidue(taskID int64, includeRemoved bool) ([]*Residue, error) {
	rows, err := d.Query(`SELECT `+residueCols+` FROM task_residue
WHERE task_id=$1 AND ($2::boolean OR removed_at IS NULL)
ORDER BY removed_at NULLS FIRST, id DESC`, taskID, includeRemoved)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Residue
	for rows.Next() {
		r, err := scanResidue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResiduePatch is the editable part of a ledger row.
type ResiduePatch struct {
	Rank      *string
	Removable *bool
	Locator   *string
	Fiction   *string
}

// SetResidue applies a partial update and returns the row.
func (d *DB) SetResidue(id int64, patch ResiduePatch) (*Residue, error) {
	var rank *string
	if patch.Rank != nil {
		normalized, ok := NormalizeResidueRank(*patch.Rank)
		if !ok {
			return nil, locale.Errorf("residue rank must be obvious, plausible or keep")
		}
		rank = &normalized
	}
	return scanResidue(d.QueryRow(`UPDATE task_residue SET
rank = CASE WHEN $2::boolean THEN $3 ELSE rank END,
removable = CASE WHEN $4::boolean THEN $5 ELSE removable END,
locator = CASE WHEN $6::boolean THEN $7 ELSE locator END,
fiction = CASE WHEN $8::boolean THEN $9 ELSE fiction END
WHERE id=$1 RETURNING `+residueCols,
		id, patch.Rank != nil, rank, patch.Removable != nil, patch.Removable,
		patch.Locator != nil, strings.TrimSpace(deref(patch.Locator)),
		patch.Fiction != nil, strings.TrimSpace(deref(patch.Fiction))))
}

// RemoveResidue marks the object gone. Idempotent.
func (d *DB) RemoveResidue(id int64) (*Residue, error) {
	return scanResidue(d.QueryRow(`UPDATE task_residue SET removed_at=COALESCE(removed_at, now())
WHERE id=$1 RETURNING `+residueCols, id))
}

// DeleteResidue removes the ledger row itself (object state untouched).
func (d *DB) DeleteResidue(id int64) error {
	_, err := d.Exec(`DELETE FROM task_residue WHERE id=$1`, id)
	return err
}

// CountOpenObviousResidue counts removable, still-present, obvious residue.
func (d *DB) CountOpenObviousResidue(taskID int64) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM task_residue
WHERE task_id=$1 AND removed_at IS NULL AND removable AND rank=$2`, taskID, ResidueRankObvious).Scan(&n)
	return n, err
}

// OpenObviousResidue lists the blocking rows for a completion refusal message.
func (d *DB) OpenObviousResidue(taskID int64, limit int) ([]*Residue, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := d.Query(`SELECT `+residueCols+` FROM task_residue
WHERE task_id=$1 AND removed_at IS NULL AND removable AND rank=$2
ORDER BY id ASC LIMIT $3`, taskID, ResidueRankObvious, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Residue
	for rows.Next() {
		r, err := scanResidue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// KeepAllResidue downgrades every open row to keep, the one-shot operator override.
func (d *DB) KeepAllResidue(taskID int64) (int64, error) {
	res, err := d.Exec(`UPDATE task_residue SET rank=$2 WHERE task_id=$1 AND removed_at IS NULL`, taskID, ResidueRankKeep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ResidueCompletionGate reports whether completion must wait. It is false for
// pentest tasks and for tasks with no blocking rows, so historical behaviour holds.
func (d *DB) ResidueCompletionGate(taskID int64) (open int, blocked bool, err error) {
	if d.TaskOpsec(taskID).Mode != TaskModeOp {
		return 0, false, nil
	}
	open, err = d.CountOpenObviousResidue(taskID)
	if err != nil {
		return 0, false, err
	}
	return open, open > 0, nil
}

// AssetStore delegation keeps the tool layer on the store it already holds.
func (s *AssetStore) AddResidue(taskID int64, kind, target, locator, fiction, rank string, removable bool) (int64, error) {
	return s.db.AddResidue(taskID, kind, target, locator, fiction, rank, removable)
}
func (s *AssetStore) ListResidue(taskID int64, includeRemoved bool) ([]*Residue, error) {
	return s.db.ListResidue(taskID, includeRemoved)
}
func (s *AssetStore) SetResidue(id int64, patch ResiduePatch) (*Residue, error) {
	return s.db.SetResidue(id, patch)
}
func (s *AssetStore) RemoveResidue(id int64) (*Residue, error) { return s.db.RemoveResidue(id) }
func (s *AssetStore) KeepAllResidue(taskID int64) (int64, error) {
	return s.db.KeepAllResidue(taskID)
}
func (s *AssetStore) ResidueCompletionGate(taskID int64) (int, bool, error) {
	return s.db.ResidueCompletionGate(taskID)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

var _ = sql.ErrNoRows

// OpenObviousResidue is the AssetStore view used by the prove_goal reply.
func (s *AssetStore) OpenObviousResidue(taskID int64, limit int) ([]*Residue, error) {
	return s.db.OpenObviousResidue(taskID, limit)
}

// SetHouseStyle persists a task's learned naming grammar (tasks.house_style).
// The style tools already hold an AssetStore, not a DB, so they need this view.
// See docs/opsec-blend.md §5.
func (s *AssetStore) SetHouseStyle(taskID int64, raw json.RawMessage) error {
	if taskID <= 0 {
		return locale.Errorf("house style requires task context")
	}
	normalized, err := NormalizeTaskOpsecJSON(raw)
	if err != nil {
		return err
	}
	_, err = s.db.UpdateTask(taskID, TaskPatch{HouseStyle: normalized})
	return err
}

// TaskHouseStyle loads a task's raw stored grammar ("{}" when never harvested).
func (s *AssetStore) TaskHouseStyle(taskID int64) json.RawMessage {
	return s.db.TaskOpsec(taskID).HouseStyle
}
