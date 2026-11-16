// Package egress reads and rotates the machine's outbound identity. On a box
// running Mullvad the tunnel *is* the identity: there is no per-request proxy to
// pick, only the relay the whole machine exits through. See docs/opsec-blend.md §8.
package egress

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Status is the current outbound identity and its safety state.
type Status struct {
	Driver     string `json:"driver"`
	Connected  bool   `json:"connected"`
	Lockdown   bool   `json:"lockdown"`
	ExitIP     string `json:"exit_ip,omitempty"`
	Hostname   string `json:"hostname,omitempty"`
	City       string `json:"city,omitempty"`
	Country    string `json:"country,omitempty"`
	Interface  string `json:"interface,omitempty"`
	ObservedAt int64  `json:"observed_at"`
}

// Safe reports whether exiting through this identity cannot leak the host IP:
// either the tunnel is up, or lockdown blocks traffic while it is down.
func (s Status) Safe() bool { return s.Connected || s.Lockdown }

// Key identifies one identity for bookkeeping and burn tracking.
func (s Status) Key() string {
	if s.ExitIP != "" {
		return s.ExitIP
	}
	return s.Hostname
}

// Provider is what the engine needs from any egress implementation.
type Provider interface {
	Status(ctx context.Context) (Status, error)
	// Rotate changes the exit to a different relay. location may be empty to let
	// the driver pick a different one; a non-empty value is passed to the driver's
	// location selector.
	Rotate(ctx context.Context, location string) (Status, error)
}

// Runner runs a command and returns stdout. Injected by tests.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

// Mullvad drives the Mullvad CLI.
type Mullvad struct {
	Bin  string
	Run  Runner
	Wait time.Duration // how long Rotate waits for a new exit
}

// NewMullvad returns a provider using the `mullvad` binary on PATH.
func NewMullvad() *Mullvad {
	return &Mullvad{Bin: "mullvad", Run: execRunner, Wait: 30 * time.Second}
}

func (m *Mullvad) bin() string {
	if m.Bin == "" {
		return "mullvad"
	}
	return m.Bin
}

func (m *Mullvad) run(ctx context.Context, args ...string) ([]byte, error) {
	if m.Run == nil {
		m.Run = execRunner
	}
	return m.Run(ctx, m.bin(), args...)
}

// mullvadStatus mirrors the subset of `mullvad status -j` we use.
type mullvadStatus struct {
	State   string `json:"state"`
	Details struct {
		Endpoint struct {
			Interface string `json:"tunnel_interface"`
		} `json:"endpoint"`
		Location struct {
			IPv4     string `json:"ipv4"`
			Hostname string `json:"hostname"`
			City     string `json:"city"`
			Country  string `json:"country"`
		} `json:"location"`
	} `json:"details"`
}

// Status reads the tunnel state and the lockdown flag. Lockdown is read
// separately: status -j does not report it.
func (m *Mullvad) Status(ctx context.Context) (Status, error) {
	raw, err := m.run(ctx, "status", "-j")
	if err != nil {
		return Status{Driver: "mullvad", ObservedAt: time.Now().Unix()}, err
	}
	var parsed mullvadStatus
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return Status{Driver: "mullvad"}, fmt.Errorf("parse mullvad status: %w", err)
	}
	lockRaw, lockErr := m.run(ctx, "lockdown-mode", "get")
	st := Status{
		Driver:     "mullvad",
		Connected:  parsed.State == "connected",
		Lockdown:   lockErr == nil && strings.Contains(strings.ToLower(string(lockRaw)), "on"),
		ExitIP:     parsed.Details.Location.IPv4,
		Hostname:   parsed.Details.Location.Hostname,
		City:       parsed.Details.Location.City,
		Country:    parsed.Details.Location.Country,
		Interface:  parsed.Details.Endpoint.Interface,
		ObservedAt: time.Now().Unix(),
	}
	return st, nil
}

// Rotate selects a new location (when given) and reconnects, then waits until a
// different exit IP is reported. It never reports success on a stale identity.
func (m *Mullvad) Rotate(ctx context.Context, location string) (Status, error) {
	before, _ := m.Status(ctx)
	if loc := strings.TrimSpace(location); loc != "" {
		if _, err := m.run(ctx, "relay", "set", "location", loc); err != nil {
			return before, err
		}
	}
	if _, err := m.run(ctx, "reconnect", "-w"); err != nil {
		return before, err
	}
	wait := m.Wait
	if wait <= 0 {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		st, err := m.Status(ctx)
		if err == nil && st.Connected && st.Key() != "" && st.Key() != before.Key() {
			return st, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return st, fmt.Errorf("rotate: %w", err)
			}
			return st, fmt.Errorf("rotate: exit unchanged after %s", wait)
		}
		select {
		case <-ctx.Done():
			return st, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
