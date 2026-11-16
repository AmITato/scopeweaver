package agent

import (
	"context"
	"encoding/json"

	"github.com/Autumn-27/artex/egress"
	"github.com/Autumn-27/artex/locale"
	actool "github.com/Autumn-27/norma/tool"
)

// SetEgress wires the machine's outbound identity provider. nil means unmanaged.
func (t *ToolSet) SetEgress(p egress.Provider) { t.egress = p }

func (t *ToolSet) egressStatusTool() actool.CoreTool {
	return readTool(
		"egress_status",
		locale.Text(t.language, "Report the machine's outbound identity: the VPN driver, whether the tunnel is connected, whether lockdown blocks traffic when it is down, and the exit IP/relay/city. Use it before active work: exiting while the tunnel is down without lockdown leaks the host IP."),
		obj(map[string]any{}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.egress == nil {
				return jsonResult(map[string]any{"driver": "none", "managed": false})
			}
			st, err := t.egress.Status(ctx)
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return jsonResult(map[string]any{
				"driver":    st.Driver,
				"managed":   true,
				"connected": st.Connected,
				"lockdown":  st.Lockdown,
				"exit_ip":   st.ExitIP,
				"hostname":  st.Hostname,
				"city":      st.City,
				"country":   st.Country,
				"safe":      st.Safe(),
			})
		},
	)
}

// egressRotate changes the exit. It is operator-driven and must run between
// waves: every worker and the model stream share this tunnel.
func (t *ToolSet) egressRotateTool() actool.CoreTool {
	return writeTool(
		"egress_rotate",
		locale.Text(t.language, "Rotate the machine's VPN exit, optionally to a location selector such as \"de fra\" or \"se sto\". Rotation moves ALL traffic including the model stream, so only call it when no worker is mid-chain and the operator asked for it. Verifies the exit IP actually changed."),
		obj(map[string]any{
			"location": str(locale.Text(t.language, "Optional relay selector: country [city] [host], e.g. \"de fra\"")),
		}),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.egress == nil {
				return actool.Errorf(locale.Text(locale.FromContext(ctx), "no egress driver is configured")), nil
			}
			var a struct {
				Location string `json:"location"`
			}
			_ = json.Unmarshal(in, &a)
			st, err := t.egress.Rotate(ctx, a.Location)
			if err != nil {
				return actool.Errorf(locale.ErrorMessage(locale.FromContext(ctx), err)), nil
			}
			return jsonResult(map[string]any{"exit_ip": st.ExitIP, "hostname": st.Hostname, "city": st.City, "country": st.Country})
		},
	)
}
