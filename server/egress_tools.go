package server

import (
	"context"

	"github.com/Autumn-27/artex/egress"
)

// rotationProvider routes the agent's egress_rotate tool through the server's
// drain barrier, so a manual rotation waits for in-flight work and is recorded
// (LastRotation, burn baseline) exactly like an automatic one.
type rotationProvider struct {
	s    *Server
	base egress.Provider
}

func (p rotationProvider) Status(ctx context.Context) (egress.Status, error) {
	return p.base.Status(ctx)
}

func (p rotationProvider) Rotate(ctx context.Context, location string) (egress.Status, error) {
	return p.s.RotateEgress(ctx, location)
}

// egressForTools returns the provider agents should see, or nil when unmanaged.
func (s *Server) egressForTools() egress.Provider {
	base := s.m.Egress()
	if base == nil {
		return nil
	}
	return rotationProvider{s: s, base: base}
}
