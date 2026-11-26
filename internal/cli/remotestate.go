package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/auth"
	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// remoteStateReader reads the states baseplate_remote_state data sources
// name, each once per run however often the config is loaded.
func remoteStateReader() config.RemoteStateReader {
	cache := map[string]map[string]cty.Value{}
	return func(src config.RemoteStateSource) (map[string]cty.Value, error) {
		key, err := sourceKey(src)
		if err != nil {
			return nil, err
		}
		if v, ok := cache[key]; ok {
			return v, nil
		}
		outputs, err := readRemoteOutputs(src)
		if err != nil {
			return nil, err
		}
		values, err := state.OutputValues(outputs)
		if err != nil {
			return nil, err
		}
		for name, o := range outputs {
			if o.Sensitive {
				values[name] = values[name].Mark(plan.SensitiveMark)
			}
		}
		cache[key] = values
		return values, nil
	}
}

func sourceKey(src config.RemoteStateSource) (string, error) {
	keys := make([]string, 0, len(src.Config))
	for k := range src.Config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := []any{src.Backend}
	for _, k := range keys {
		v := src.Config[k]
		b, err := ctyjson.Marshal(v, v.Type())
		if err != nil {
			return "", fmt.Errorf("config.%s: %w", k, err)
		}
		parts = append(parts, k, json.RawMessage(b))
	}
	b, err := json.Marshal(parts)
	return string(b), err
}

// readRemoteOutputs reads the output values of the state src names, as
// Terraform's terraform_remote_state does: through that backend, without
// taking its lock. The http backend uses the same credentials as a backend
// block for that address. From a server that advertises outputs.v1, only the
// outputs are read; from any other, the whole state.
func readRemoteOutputs(src config.RemoteStateSource) (map[string]state.Output, error) {
	switch src.Backend {
	case "local":
		path, ok := src.Config["path"]
		if len(src.Config) != 1 || !ok || path.IsNull() || path.Type() != cty.String {
			return nil, fmt.Errorf(`the local backend takes config = { path = "<state file>" }`)
		}
		st, err := backend.NewLocal(path.AsString()).Get()
		if err != nil {
			return nil, err
		}
		return st.Outputs, nil
	case "http":
		h, err := httpBackend(src.Config)
		if err != nil {
			return nil, err
		}
		address := src.Config["address"].AsString()
		host, err := auth.HostOf(address)
		if err != nil {
			return nil, err
		}
		// A server that can't be asked falls back to the whole state, which
		// every http backend serves.
		if d, err := auth.Discover(context.Background(), http.DefaultClient, host); err == nil && d.Outputs != nil {
			return h.Outputs()
		}
		st, err := h.Get()
		if err != nil {
			return nil, err
		}
		return st.Outputs, nil
	default:
		return nil, fmt.Errorf("unsupported backend %q; use \"http\" or \"local\"", src.Backend)
	}
}
