package server

import (
	"encoding/json"
	"testing"
)

// TestProxyAddrTracksBudgetWithoutCapture pins the §7 contract: the proxy is usable
// for throttling alone. Capture off + budget on must still hand agents the MITM
// proxy and its CA (HostTools stay capture-only); neither must yield "" so direct
// egress is unaffected.
func TestProxyAddrTracksBudgetWithoutCapture(t *testing.T) {
	s, _, _ := trafficEvidenceServer(t)
	m := s.m
	m.SetTrafficEnabled(false) //nolint:errcheck
	t.Cleanup(func() { m.SetEgressBudget(0, defaultEgressBurst); m.SetTrafficEnabled(false) })

	wantProxy := m.traffic.ProxyAddr()
	wantCA := m.traffic.CACertPath()

	// Neither: direct egress, no CA, no traffic tools.
	if got := m.ProxyAddr(); got != "" {
		t.Fatalf("ProxyAddr with nothing on = %q, want empty", got)
	}
	if got := m.ProxyCACert(); got != "" {
		t.Fatalf("ProxyCACert with nothing on = %q, want empty", got)
	}

	// Budget on, capture off: the proxy and CA are in play, tools are not.
	if err := m.SetEgressBudget(1, 5); err != nil {
		t.Fatal(err)
	}
	if got := m.ProxyAddr(); got != wantProxy {
		t.Fatalf("ProxyAddr with budget on = %q, want %q", got, wantProxy)
	}
	if got := m.ProxyCACert(); got != wantCA {
		t.Fatalf("ProxyCACert with budget on = %q, want %q", got, wantCA)
	}
	if tools := m.HostTools(); len(tools) != 0 {
		t.Fatalf("HostTools with capture off returned %d tools", len(tools))
	}

	// A global proxy must not win over the budget (the budget is enforced inside
	// the MITM, so traffic has to keep flowing through it).
	t.Cleanup(func() { m.SetGlobalProxy("") }) //nolint:errcheck
	if err := m.SetGlobalProxy("http://127.0.0.1:9"); err != nil {
		t.Fatal(err)
	}
	if got := m.ProxyAddr(); got != wantProxy {
		t.Fatalf("ProxyAddr with budget and global proxy = %q, want %q", got, wantProxy)
	}

	// Budget off again: back to the bare global proxy, no CA.
	if err := m.SetEgressBudget(0, 5); err != nil {
		t.Fatal(err)
	}
	if got := m.ProxyAddr(); got != "http://127.0.0.1:9" {
		t.Fatalf("ProxyAddr after budget off = %q, want the global proxy", got)
	}
	if got := m.ProxyCACert(); got != "" {
		t.Fatalf("ProxyCACert after budget off = %q, want empty", got)
	}

	// Capture alone still routes through the proxy, as before.
	if err := m.SetTrafficEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := m.ProxyAddr(); got != wantProxy {
		t.Fatalf("ProxyAddr with capture on = %q, want %q", got, wantProxy)
	}
}

func TestEgressBudgetSettingsRoundTrip(t *testing.T) {
	s, _, request := trafficEvidenceServer(t)
	prevRPS, prevBurst := s.m.EgressBudget()
	t.Cleanup(func() { s.m.SetEgressBudget(prevRPS, prevBurst) }) //nolint:errcheck
	if err := s.m.SetEgressBudget(0, defaultEgressBurst); err != nil {
		t.Fatal(err)
	}

	if payload := s.settingsPayload(); payload["egress_rps"] != float64(0) || payload["egress_burst"] != defaultEgressBurst {
		t.Fatalf("defaults in payload = %v/%v", payload["egress_rps"], payload["egress_burst"])
	}

	r := request("PUT", "/api/settings", `{"egress_rps":2.5,"egress_burst":7}`)
	if r.Code != 200 {
		t.Fatalf("PUT egress = %d %s", r.Code, r.Body)
	}
	var got struct {
		RPS   float64 `json:"egress_rps"`
		Burst int     `json:"egress_burst"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RPS != 2.5 || got.Burst != 7 {
		t.Fatalf("payload after PUT = %v/%v, want 2.5/7", got.RPS, got.Burst)
	}
	if rps, burst := s.m.EgressBudget(); rps != 2.5 || burst != 7 {
		t.Fatalf("stored budget = %v/%v, want 2.5/7", rps, burst)
	}
	if v, _, _ := s.m.pg.GetSetting(settingEgressRPS); v != "2.5" {
		t.Fatalf("setting %s = %q", settingEgressRPS, v)
	}
	if !s.m.traffic.BudgetOn() {
		t.Fatal("budget not applied to the proxy")
	}

	// A burst-only PUT keeps the stored rate.
	if r := request("PUT", "/api/settings", `{"egress_burst":9}`); r.Code != 200 {
		t.Fatalf("partial PUT = %d %s", r.Code, r.Body)
	}
	if rps, burst := s.m.EgressBudget(); rps != 2.5 || burst != 9 {
		t.Fatalf("partial PUT changed the rate: %v/%v", rps, burst)
	}

	// 0 disables and clears the proxy state; negatives are rejected.
	if r := request("PUT", "/api/settings", `{"egress_rps":0}`); r.Code != 200 {
		t.Fatalf("PUT rps=0 = %d %s", r.Code, r.Body)
	}
	if s.m.traffic.BudgetOn() || s.m.traffic.BudgetSnapshot() != nil {
		t.Fatal("rps=0 did not disable the budget")
	}
	if r := request("PUT", "/api/settings", `{"egress_rps":-1}`); r.Code != 400 {
		t.Fatalf("PUT negative rps = %d, want 400", r.Code)
	}
}
