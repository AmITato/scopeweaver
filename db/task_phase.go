package db

import (
	"encoding/json"
	"strings"

	"github.com/Autumn-27/artex/locale"
)

// Engagement phases. Passive work is silent reconnaissance; active work touches
// the target. An op task starts passive and a claim gate keeps active intents
// queued until the planner advances the phase. See docs/opsec-blend.md §9.
const (
	TaskPhasePassive   = "passive"
	TaskPhaseActive    = "active"
	TaskPhasePersisted = "persisted"
)

// NormalizeTaskPhase validates a phase, defaulting empty to active.
func NormalizeTaskPhase(phase string) (string, bool) {
	switch strings.TrimSpace(phase) {
	case "", TaskPhaseActive:
		return TaskPhaseActive, true
	case TaskPhasePassive:
		return TaskPhasePassive, true
	case TaskPhasePersisted:
		return TaskPhasePersisted, true
	}
	return "", false
}

// TaskPhase reads a task's phase (active when unset or on error).
func (d *DB) TaskPhase(taskID int64) string {
	if taskID <= 0 {
		return TaskPhaseActive
	}
	var phase string
	if err := d.QueryRow(`SELECT COALESCE(phase,'active') FROM tasks WHERE id=$1`, taskID).Scan(&phase); err != nil {
		return TaskPhaseActive
	}
	if normalized, ok := NormalizeTaskPhase(phase); ok {
		return normalized
	}
	return TaskPhaseActive
}

// SetTaskPhase persists a phase and returns the stored value.
func (d *DB) SetTaskPhase(taskID int64, phase string) (string, error) {
	normalized, ok := NormalizeTaskPhase(phase)
	if !ok {
		return "", locale.Errorf("task phase must be passive, active or persisted")
	}
	if taskID <= 0 {
		return "", locale.Errorf("task phase requires task context")
	}
	if _, err := d.Exec(`UPDATE tasks SET phase=$2 WHERE id=$1`, taskID, normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

// AssetStore delegations.
func (s *AssetStore) TaskPhase(taskID int64) string { return s.db.TaskPhase(taskID) }
func (s *AssetStore) SetTaskPhase(taskID int64, phase string) (string, error) {
	return s.db.SetTaskPhase(taskID, phase)
}

// IntentPhase returns an intent's declared phase, defaulting to active so an
// untagged intent is never treated as silent work.
func IntentPhase(n *Node) string {
	if n == nil || len(n.Payload) == 0 {
		return TaskPhaseActive
	}
	var payload struct {
		Phase string `json:"phase"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return TaskPhaseActive
	}
	if normalized, ok := NormalizeTaskPhase(payload.Phase); ok {
		return normalized
	}
	return TaskPhaseActive
}
