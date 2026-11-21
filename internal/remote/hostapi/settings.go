package hostapi

import (
	"context"
	"slices"
	"strings"

	"github.com/brettmostert/agent-command/internal/config"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/remote/wire"
)

// Clients see and change the host's settings, but not its device ones: the
// editor and speech belong to whoever sits at the host.

// hostOnly is st without the device settings.
func hostOnly(st ipc.ConfigState) ipc.ConfigState {
	out := ipc.ConfigState{Path: st.Path, Problem: st.Problem, Values: map[string]any{}}
	for _, s := range st.Settings {
		if s.Where != config.WhereHost {
			continue
		}
		out.Settings = append(out.Settings, s)
		if v, ok := st.Values[s.Key]; ok {
			out.Values[s.Key] = v
		}
	}
	return out
}

func (a *API) configGet(context.Context, *wire.Call) (any, error) {
	return hostOnly(a.b.Config()), nil
}

func (a *API) configSet(_ context.Context, c *wire.Call) (any, error) {
	var req ipc.ConfigSet
	if err := c.Decode(&req); err != nil {
		return nil, err
	}
	var device []string
	for key := range req.Values {
		if s, ok := config.Find(key); ok && s.Where != config.WhereHost {
			device = append(device, s.Key)
		}
	}
	if len(device) > 0 {
		slices.Sort(device)
		return nil, wire.Errorf(wire.CodeBadRequest, "%s belong to the host's own machine: change them there",
			strings.Join(device, ", "))
	}
	st, err := a.b.SetConfig(req.Values)
	if err != nil {
		return nil, wire.Errorf(wire.CodeBadRequest, "%s", err.Error())
	}
	return hostOnly(st), nil
}
