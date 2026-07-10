package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroup/baseplate/internal/provider"
	"github.com/outpostgroup/baseplate/internal/provider/roblox/opencloud"
)

type universeSecret struct{ sc opencloud.SecretClient }

// UniverseSecret returns the roblox_universe_secret resource. Its state id is
// "<universeID>/<name>". The plaintext `value` is sealed client-side, never stored
// in state, and never rendered in plan output; value-change detection flows through
// the value_hash trigger. Destroy performs a real hard delete.
func UniverseSecret(sc opencloud.SecretClient) provider.Resource { return &universeSecret{sc: sc} }

func (u *universeSecret) Type() string { return "roblox_universe_secret" }

func (u *universeSecret) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id": &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"name":        &hcldec.AttrSpec{Name: "name", Type: cty.String, Required: true},
		"value":       &hcldec.AttrSpec{Name: "value", Type: cty.String, Required: true},
		"domain":      &hcldec.AttrSpec{Name: "domain", Type: cty.String, Required: false},
	}
}

// Computed excludes from diff: the state id; universe_id and name (create-only
// identity); value (write-only — change detection via the value_hash trigger) and
// key_id/timestamps (provider outputs). domain is intentionally absent — it is the
// one remotely-readable, mutable attribute and diffs normally.
func (u *universeSecret) Computed() []string {
	return []string{"id", "universe_id", "name", "value", "key_id", "create_time", "update_time"}
}

// Sensitive redacts the plaintext value from all rendered plan output.
func (u *universeSecret) Sensitive() []string { return []string{"value"} }

func (u *universeSecret) Dependencies(provider.Attributes) []provider.Ref { return nil }

// Triggers forces an Update when the plaintext value changes (the value is never
// readable back, so a hash is the only comparable fingerprint).
func (u *universeSecret) Triggers(desired provider.Attributes) (map[string]cty.Value, error) {
	sum := sha256.Sum256([]byte(desired["value"].AsString()))
	return map[string]cty.Value{"value_hash": cty.StringVal(hex.EncodeToString(sum[:]))}, nil
}

func (u *universeSecret) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := desired["universe_id"].AsString()
	name := desired["name"].AsString()
	got, err := u.sc.Create(ctx, universeID, u.write(desired))
	if err != nil {
		return "", nil, err
	}
	return universeID + "/" + name, u.stateAttrs(universeID, got), nil
}

func (u *universeSecret) Read(ctx context.Context, id string) (provider.Attributes, error) {
	universeID, name, ok := splitSecretID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_universe_secret: invalid id %q", id)
	}
	list, err := u.sc.List(ctx, universeID)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	for _, s := range list {
		if s.Name == name {
			return u.stateAttrs(universeID, s), nil
		}
	}
	return nil, provider.ErrNotFound
}

func (u *universeSecret) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	universeID, name, ok := splitSecretID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_universe_secret: invalid id %q", id)
	}
	w := u.write(desired)
	w.Name = name
	got, err := u.sc.Update(ctx, universeID, w)
	if err != nil {
		return nil, err
	}
	return u.stateAttrs(universeID, got), nil
}

func (u *universeSecret) Delete(ctx context.Context, id string) error {
	universeID, name, ok := splitSecretID(id)
	if !ok {
		return fmt.Errorf("roblox_universe_secret: invalid id %q", id)
	}
	return u.sc.Delete(ctx, universeID, name)
}

// write maps config to the client input. Value carries the plaintext (sealed by
// the client); domain defaults to empty (private key) when unset.
func (u *universeSecret) write(desired provider.Attributes) opencloud.SecretWrite {
	return opencloud.SecretWrite{
		Name:   desired["name"].AsString(),
		Value:  []byte(desired["value"].AsString()),
		Domain: stringOr(desired, "domain"),
	}
}

// stateAttrs is what gets persisted — never the plaintext value.
func (u *universeSecret) stateAttrs(universeID string, s opencloud.Secret) provider.Attributes {
	return provider.Attributes{
		"universe_id": cty.StringVal(universeID),
		"name":        cty.StringVal(s.Name),
		"domain":      cty.StringVal(s.Domain),
		"key_id":      cty.StringVal(s.KeyID),
	}
}

// splitSecretID splits "<universeID>/<name>".
func splitSecretID(id string) (string, string, bool) {
	i := strings.IndexByte(id, '/')
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}
