package server

import (
	"context"
	"errors"
	"github.com/Autumn-27/artex/chatstate"
	"github.com/Autumn-27/norma/transcript"
	"path/filepath"
)

func (s *Server) discardUnknownChatRun(ctx context.Context, ref chatstate.SessionRef, inputID string, expected int64) (err error) {
	operationCtx, release, beginErr := s.beginChatMutation(ctx, ref)
	if beginErr != nil {
		return beginErr
	}
	defer release()
	ctx = operationCtx
	run, err := s.m.pg.ChatRunForInput(ctx, inputID)
	if err != nil {
		return err
	}
	if run.Session.GateKey() != ref.GateKey() {
		return chatstate.ErrGone
	}
	ref = run.Session
	token, prior, reserved, err := s.m.pg.BeginChatContextChange(ctx, ref, "discard-"+inputID, expected, "discard", inputID)
	if err != nil {
		return err
	}
	if prior != nil {
		return nil
	}
	defer func() {
		if err != nil {
			_ = s.m.pg.AbortChatContextChange(context.Background(), ref, token)
		}
	}()
	if run.Status != "unknown" {
		return chatstate.ErrConflict
	}
	active, err := s.m.pg.ActiveChatTranscript(ctx, ref)
	if err != nil {
		return err
	}
	store := transcript.NewStore(filepath.Join(s.m.dir, "transcripts"))
	if active.ID != "" {
		if err = chatstate.ValidateTranscriptPrefix(store, *active); err != nil {
			return err
		}
	}
	var records []transcript.Record
	if run.BaselineRecorded {
		records, err = chatstate.LoadChatTranscriptThrough(store, active.TranscriptID, run.BeforeRecordUUID)
	} else {
		records, err = store.Load(store.MainPath(active.TranscriptID))
		if err == nil && len(records) > 0 {
			end := -1
			for i, r := range records {
				if r.UUID == run.StartRecordUUID && run.StartRecordUUID != "" {
					end = i
					break
				}
			}
			if end < 0 {
				// The active transcript may have been rotated or republished after the
				// restart, so the run's start record can be gone. The baseline record is
				// the next best anchor: keep everything up to and including it.
				end = indexAfter(records, run.BeforeRecordUUID)
			}
			if end < 0 {
				end = 0
			}
			records = records[:end]
		}
	}
	if err != nil {
		return err
	}
	revision, err := chatstate.PrepareTranscript(ctx, store, ref, records)
	// A crash can leave duplicate UUIDs or a split tool exchange in the recovered
	// tail. Drop trailing records until the prefix prepares, so acknowledging a
	// lost run never dead-ends on an ambiguous tail.
	for attempt := 0; err != nil && errors.Is(err, chatstate.ErrAmbiguousHistory) && len(records) > 0 && attempt < 500; attempt++ {
		records = records[:len(records)-1]
		revision, err = chatstate.PrepareTranscript(ctx, store, ref, records)
	}
	if err != nil {
		return err
	}
	defer s.cleanupPreparedChatTranscript(revision)
	_, err = s.m.pg.CommitChatContextChange(ctx, ref, token, reserved, revision, nil, inputID)
	return err
}

// indexAfter returns the position after uuid, or -1 when absent.
func indexAfter(records []transcript.Record, uuid string) int {
	if uuid == "" {
		return -1
	}
	for i, r := range records {
		if r.UUID == uuid {
			return i + 1
		}
	}
	return -1
}
