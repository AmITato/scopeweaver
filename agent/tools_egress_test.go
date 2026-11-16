package agent

import (
	"context"
	"testing"

	"github.com/Autumn-27/artex/egress"
	"github.com/Autumn-27/artex/locale"
)

type stubEgress struct {
	status egress.Status
	rotate egress.Status
	err    error
	called string
}

func (s *stubEgress) Status(context.Context) (egress.Status, error) {
	return s.status, s.err
}
func (s *stubEgress) Rotate(_ context.Context, location string) (egress.Status, error) {
	s.called = location
	return s.rotate, s.err
}

func TestEgressStatusReportsSafeLeakState(t *testing.T) {
	tools := NewToolSet(nil, "worker", locale.En)
	tools.SetEgress(&stubEgress{status: egress.Status{Driver: "mullvad", Connected: false, Lockdown: false, ExitIP: "1.2.3.4"}})
	out := callReadJSON(t, tools.egressStatusTool(), `{}`).(map[string]any)
	if out["safe"] != false {
		t.Fatalf("down tunnel without lockdown must be unsafe: %v", out)
	}
	tools.SetEgress(&stubEgress{status: egress.Status{Driver: "mullvad", Connected: false, Lockdown: true}})
	out = callReadJSON(t, tools.egressStatusTool(), `{}`).(map[string]any)
	if out["safe"] != true {
		t.Fatalf("lockdown must make a down tunnel safe: %v", out)
	}
}

func TestEgressToolsWithoutDriver(t *testing.T) {
	tools := NewToolSet(nil, "mainagent", locale.En)
	out := callReadJSON(t, tools.egressStatusTool(), `{}`).(map[string]any)
	if out["managed"] != false || out["driver"] != "none" {
		t.Fatalf("unmanaged status: %v", out)
	}
	res, err := tools.egressRotateTool().Call(context.Background(), []byte(`{"location":"de fra"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("rotate without a driver must error: %s", res.Flatten())
	}
}

func TestEgressRotatePassesLocation(t *testing.T) {
	stub := &stubEgress{rotate: egress.Status{ExitIP: "5.6.7.8", Hostname: "de-fra-wg-001", City: "Frankfurt", Country: "Germany"}}
	tools := NewToolSet(nil, "mainagent", locale.En)
	tools.SetEgress(stub)
	out := callReadJSON(t, tools.egressRotateTool(), `{"location":"de fra"}`).(map[string]any)
	if stub.called != "de fra" || out["exit_ip"] != "5.6.7.8" {
		t.Fatalf("called=%q out=%v", stub.called, out)
	}
}
