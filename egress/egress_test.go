package egress

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func fakeRun(responses map[string]string, calls *[]string) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		if calls != nil {
			*calls = append(*calls, key)
		}
		if out, ok := responses[key]; ok {
			return []byte(out), nil
		}
		return nil, errors.New("unexpected call: " + key)
	}
}

const connectedJSON = `{"state":"connected","details":{"endpoint":{"tunnel_interface":"wg0-mullvad"},"location":{"ipv4":"146.70.193.83","hostname":"rs-beg-wg-101","city":"Belgrade","country":"Serbia"}}}`

func TestStatusParsesIdentityAndLockdown(t *testing.T) {
	m := &Mullvad{Run: fakeRun(map[string]string{
		"status -j":         connectedJSON,
		"lockdown-mode get": "Block traffic when the VPN is disconnected: on\n",
	}, nil)}
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Connected || !st.Lockdown {
		t.Fatalf("status=%+v", st)
	}
	if st.ExitIP != "146.70.193.83" || st.Hostname != "rs-beg-wg-101" || st.City != "Belgrade" || st.Country != "Serbia" {
		t.Fatalf("identity not parsed: %+v", st)
	}
	if st.Key() != "146.70.193.83" {
		t.Fatalf("key=%q", st.Key())
	}
}

func TestSafeRequiresConnectedOrLockdown(t *testing.T) {
	cases := []struct {
		st   Status
		safe bool
	}{
		{Status{Connected: true}, true},
		{Status{Connected: false, Lockdown: true}, true},
		{Status{Connected: false, Lockdown: false}, false},
	}
	for _, tc := range cases {
		if got := tc.st.Safe(); got != tc.safe {
			t.Fatalf("%+v safe=%v want %v", tc.st, got, tc.safe)
		}
	}
}

func TestRotateRequiresNewExit(t *testing.T) {
	var calls []string
	// First status is the old identity; after reconnect, a new one.
	jsonNew := strings.Replace(connectedJSON, "146.70.193.83", "185.65.135.100", 1)
	n := 0
	run := func(ctx context.Context, name string, args ...string) ([]byte, error) {
		key := strings.Join(args, " ")
		calls = append(calls, key)
		switch key {
		case "status -j":
			n++
			if n == 1 {
				return []byte(connectedJSON), nil
			}
			return []byte(jsonNew), nil
		case "lockdown-mode get":
			return []byte("Block traffic when the VPN is disconnected: on\n"), nil
		case "relay set location de fra":
			return []byte(""), nil
		case "reconnect -w":
			return []byte(""), nil
		}
		return nil, errors.New("unexpected: " + key)
	}
	m := &Mullvad{Run: run}
	st, err := m.Rotate(context.Background(), "de fra")
	if err != nil {
		t.Fatal(err)
	}
	if st.ExitIP != "185.65.135.100" {
		t.Fatalf("exit not rotated: %+v", st)
	}
	if len(calls) < 4 || calls[2] != "relay set location de fra" {
		t.Fatalf("calls=%v", calls)
	}
}

func TestRotateFailsWhenExitUnchanged(t *testing.T) {
	run := fakeRun(map[string]string{
		"status -j":         connectedJSON,
		"lockdown-mode get": "Block traffic when the VPN is disconnected: on\n",
		"reconnect -w":      "",
	}, nil)
	m := &Mullvad{Run: run, Wait: 2 * time.Second}
	if _, err := m.Rotate(context.Background(), ""); err == nil {
		t.Fatal("rotate without a new exit must fail")
	}
}
