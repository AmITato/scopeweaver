package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/locale"
)

// Engagement-profile/stealth edits are small JSON documents; 64 KiB leaves room
// for a harvested naming grammar without accepting arbitrary payloads.
const maxTaskOpsecRequestSize = 64 << 10

// updateTaskOpsec changes mode/cover_story/house_style/stealth on a live task.
// It follows updateTaskMetadata's shape (presence pointers, trimmed/capped text)
// and the constraint CRUD's beginTaskOperation barrier, so a task deletion cannot
// race the write. Runtime state, description and goal are untouched.
func (s *Server) updateTaskOpsec(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, http.StatusConflict, "The task is being deleted; opsec settings cannot be changed")
		return
	}
	defer s.engine.decInflight(t.ID)
	r.Body = http.MaxBytesReader(w, r.Body, maxTaskOpsecRequestSize)
	var request struct {
		Mode       *string         `json:"mode"`
		CoverStory *string         `json:"cover_story"`
		HouseStyle json.RawMessage `json:"house_style"`
		Stealth    json.RawMessage `json:"stealth"`
	}
	if err := decode(r, &request); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeErr(w, http.StatusRequestEntityTooLarge, "Request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, err)
		}
		return
	}
	if request.Mode == nil && request.CoverStory == nil && request.HouseStyle == nil && request.Stealth == nil {
		writeErr(w, http.StatusBadRequest, "Provide at least one of mode, cover_story, house_style or stealth")
		return
	}
	if request.Mode != nil {
		mode, ok := db.NormalizeTaskMode(*request.Mode)
		if !ok {
			writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "Task mode must be pentest or op"))
			return
		}
		request.Mode = &mode
	}
	if request.CoverStory != nil {
		coverStory := strings.TrimSpace(*request.CoverStory)
		if utf8.RuneCountInString(coverStory) > maxTaskNameRunes {
			writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "Task cover story must be at most %d characters", maxTaskNameRunes))
			return
		}
		request.CoverStory = &coverStory
	}
	houseStyle, err := db.NormalizeTaskOpsecJSON(request.HouseStyle)
	if err != nil {
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "house_style must be a JSON object"))
		return
	}
	stealth, err := db.NormalizeTaskOpsecJSON(request.Stealth)
	if err != nil {
		writeErr(w, http.StatusBadRequest, locale.Text(responseLanguage(w), "stealth must be a JSON object"))
		return
	}
	task, err := s.m.UpdateTaskOpsec(t.ID, db.TaskPatch{
		Mode: request.Mode, CoverStory: request.CoverStory,
		HouseStyle: houseStyle, Stealth: stealth,
	})
	if err != nil {
		if errors.Is(err, db.ErrTaskModeInvalid) || errors.Is(err, db.ErrTaskOpsecInvalid) {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if task == nil {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, taskDTO(task, s.resolvedTaskStatus(task)))
}
