package resources

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

const (
	// configMaxKeys / configMaxKeyLen are Roblox's documented repository limits.
	configMaxKeys   = 100
	configMaxKeyLen = 256
	// configPublishMessage is recorded in the repository's revision history.
	configPublishMessage = "Published by Baseplate"
)

// configSleep is the resource's sleeper (swapped out in unit tests).
var configSleep = time.Sleep

var configStrategies = map[string]bool{"Immediate": true, "GradualRollout": true}

type experienceConfig struct {
	cc    opencloud.ExperienceConfigClient
	sleep func(time.Duration)
}

// ExperienceConfig returns the roblox_experience_config resource: a universe's
// Experience Config repository (key/value settings read in-game via
// ConfigService). Its state id is "<universeID>/<repository>"; import also
// accepts a bare universe id (repository InExperienceConfig).
//
// Ownership: the resource is authoritative for the repository's BASE keys (a key
// not in `entries` is removed on apply) but never touches what it doesn't model:
// conditional rules and the conditional branches of keys it keeps. It writes with
// a PATCH draft (changed keys + explicit nulls for removed keys, no
// conditionalRules), never draft:overwrite, which deletes every rule/branch that
// isn't echoed back and so would be one stale read away from wiping them.
//
// Destroy removes every plain key and publishes (conditional rules are left
// alone, as on apply). Production config should be protected with
// `lifecycle { prevent_destroy = true }`.
func ExperienceConfig(cc opencloud.ExperienceConfigClient) provider.Resource {
	return &experienceConfig{cc: cc, sleep: configSleep}
}

func (e *experienceConfig) Type() string { return "roblox_experience_config" }

func (e *experienceConfig) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages a universe's Experience Config (a beta Roblox feature): the key/value settings, such as feature flags and tuning values, that a published game reads at runtime through `ConfigService`. Each apply stages the key changes as a draft and publishes it.",
		Attributes: map[string]string{
			"universe_id": "The universe.",
			"repository":  "Config repository. Default `InExperienceConfig` (the only one Roblox documents).",
			"entries": "Map of config key to value. Values are written as native HCL: bool, number, string, or an object/list (a JSON config), e.g. `{ enable_tutorial = true, boss_health = 100, shop = { discount = 0.2 } }`. " +
				"Keys in the repository but not listed here are removed on apply. Max 100 keys, 256-char keys; `.RBX.` is reserved in keys.",
			"deployment_strategy": "`Immediate` (default) or `GradualRollout` (~15-20 min). Used for every publish, but not readable back, so it isn't diffed (listed as create-only): changing it alone doesn't publish.",
		},
		Notes: "The resource owns the repository's base keys: an apply removes any published key that isn't in `entries`. " +
			"To protect an existing game, the first apply (create) refuses if the repository already has keys that aren't declared. `import` it first so the plan shows what would be removed. " +
			"Conditional rules and the conditional (targeted) values of keys you keep are preserved (Baseplate never sends them, and verifies the staged draft and the published result); a removed key's conditional values are removed with it, and a type change for a key with conditional values is rejected by Roblox. " +
			"Apply refuses if the repository has an unpublished draft (for example, pending edits in Creator Hub). Publish or discard it first. " +
			"`destroy` removes every plain key in the repository and publishes (the same ownership as apply; conditional rules are left alone, and an empty or never-published repository is a no-op). Protect production config with [`lifecycle { prevent_destroy = true }`](config-reference.md#lifecycle), which makes any plan that would destroy it fail. " +
			"With `GradualRollout` (observed ~20 minutes), the API keeps returning the previous values until the rollout completes and rejects new drafts meanwhile, so a re-plan in that window shows the change again and an apply fails with a retry-later error. " +
			"Numbers are stored as doubles (exact only up to 2^53).",
		Scopes:      []string{"universe:read", "universe:write"},
		ExampleFile: "examples/experience-config.bp.hcl",
	}
}

func (e *experienceConfig) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id": &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"repository":  &hcldec.AttrSpec{Name: "repository", Type: cty.String, Required: false},
		// DynamicPseudoType (not map(any)): HCL would unify a map(any)'s mixed
		// bool/number/string values to string. The object shape is validated at apply.
		"entries":             &hcldec.AttrSpec{Name: "entries", Type: cty.DynamicPseudoType, Required: true},
		"deployment_strategy": &hcldec.AttrSpec{Name: "deployment_strategy", Type: cty.String, Required: false},
	}
}

// Computed excludes from diff: identity (universe_id/repository are fixed by
// the id), deployment_strategy (write-only, not readable back), and the
// provider-reported config_version. Only `entries` diffs.
func (e *experienceConfig) Computed() []string {
	return []string{"id", "universe_id", "repository", "deployment_strategy", "config_version"}
}

func (e *experienceConfig) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (e *experienceConfig) Read(ctx context.Context, id string) (provider.Attributes, error) {
	universeID, repo, err := splitConfigID(id)
	if err != nil {
		return nil, err
	}
	pub, err := e.cc.GetPublished(ctx, universeID, repo)
	if err != nil {
		return nil, err
	}
	entries, err := entriesToCty(pub.Entries)
	if err != nil {
		return nil, fmt.Errorf("roblox_experience_config: %w", err)
	}
	return provider.Attributes{
		"universe_id":    cty.StringVal(universeID),
		"repository":     cty.StringVal(repo),
		"entries":        entries,
		"config_version": cty.NumberIntVal(pub.Version),
	}, nil
}

func (e *experienceConfig) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := stringOr(desired, "universe_id")
	if universeID == "" {
		return "", nil, errors.New("roblox_experience_config: universe_id is required")
	}
	repo := stringOr(desired, "repository")
	if repo == "" {
		repo = opencloud.DefaultConfigRepository
	}
	out, err := e.write(ctx, universeID, repo, desired, true)
	if err != nil {
		return "", nil, err
	}
	return universeID + "/" + repo, out, nil
}

func (e *experienceConfig) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	universeID, repo, err := splitConfigID(id)
	if err != nil {
		return nil, err
	}
	return e.write(ctx, universeID, repo, desired, false)
}

// Delete removes every plain key the resource owns and publishes, the same
// ownership as apply (which already removes undeclared plain keys), so it needs
// no state. Conditional rules are left alone and the same draft guard, draft
// verification and post-publish checks apply. An empty or never-published
// repository, or a publish that turns out to be a no-op, is success.
func (e *experienceConfig) Delete(ctx context.Context, id string) error {
	universeID, repo, err := splitConfigID(id)
	if err != nil {
		return err
	}
	if err := e.refusePendingDraft(ctx, universeID, repo); err != nil {
		return err
	}
	pub, err := e.freshPublished(ctx, universeID, repo)
	if err != nil {
		return err
	}
	changes := map[string]json.RawMessage{}
	for k := range pub.Entries {
		if !isBranchKey(k) {
			changes[k] = nil // delete (its conditional branches go with it)
		}
	}
	if len(changes) == 0 {
		return nil // nothing to remove
	}
	_, err = e.publishChanges(ctx, universeID, repo, changes, pub, "Immediate", func(string) bool { return false })
	return err
}

// refusePendingDraft errors when the repository has an unpublished draft, so
// Baseplate never publishes someone else's pending edits along with its own.
func (e *experienceConfig) refusePendingDraft(ctx context.Context, universeID, repo string) error {
	draft, err := e.cc.GetDraft(ctx, universeID, repo)
	if err != nil {
		return err
	}
	if draft != nil {
		return fmt.Errorf("roblox_experience_config: universe %s repository %s has unpublished draft changes (keys: %s); publish or discard them (Creator Hub, or DELETE …/draft) before applying, so Baseplate doesn't overwrite someone else's pending edits",
			universeID, repo, keyList(draft.Entries))
	}
	return nil
}

// write stages desired as the full repository (preserving conditional rules and
// the branches of kept keys) and publishes it.
func (e *experienceConfig) write(ctx context.Context, universeID, repo string, desired provider.Attributes, create bool) (provider.Attributes, error) {
	want, err := desiredEntries(desired)
	if err != nil {
		return nil, err
	}
	strategy := stringOr(desired, "deployment_strategy")
	if strategy == "" {
		strategy = "Immediate"
	}
	if !configStrategies[strategy] {
		return nil, fmt.Errorf("roblox_experience_config: deployment_strategy must be \"Immediate\" or \"GradualRollout\", got %q", strategy)
	}

	if err := e.refusePendingDraft(ctx, universeID, repo); err != nil {
		return nil, err
	}

	pub, err := e.freshPublished(ctx, universeID, repo)
	if err != nil {
		return nil, err
	}
	if create {
		var undeclared []string
		for k := range pub.Entries {
			if _, ok := want[k]; !ok && !isBranchKey(k) {
				undeclared = append(undeclared, k)
			}
		}
		if len(undeclared) > 0 {
			sort.Strings(undeclared)
			return nil, fmt.Errorf("roblox_experience_config: universe %s repository %s already has %d published key(s) not in entries (%s); this resource is authoritative and would delete them. Import it first (import { to = …, id = \"%s/%s\" }) so the plan shows the removal, or add them to entries",
				universeID, repo, len(undeclared), strings.Join(undeclared, ", "), universeID, repo)
		}
	}

	// Every desired key (re-sending an unchanged value is a no-op for that key)
	// plus an explicit null for each published base key we don't declare. Branch
	// keys and conditional rules are never sent, so they are left as they are; a
	// nulled base key takes its own branches with it (verified live).
	changes := make(map[string]json.RawMessage, len(want))
	for k, v := range want {
		changes[k] = v
	}
	for k := range pub.Entries {
		if _, ok := want[k]; !ok && !isBranchKey(k) {
			changes[k] = nil // delete
		}
	}
	version, err := e.publishChanges(ctx, universeID, repo, changes, pub, strategy, func(k string) bool { _, ok := want[k]; return ok })
	if err != nil {
		return nil, err
	}

	out := provider.Attributes{
		"universe_id":    cty.StringVal(universeID),
		"repository":     cty.StringVal(repo),
		"entries":        desired["entries"],
		"config_version": cty.NumberIntVal(version),
	}
	if v, ok := desired["deployment_strategy"]; ok && !v.IsNull() {
		out["deployment_strategy"] = v
	}
	return out, nil
}

// freshReads is how many reads freshPublished takes to pick the newest version.
const freshReads = 3

// freshPublished reads the published config a few times and keeps the newest
// version: right after a publish, reads flap between the old and new versions
// (verified live), and the keys to delete are computed from this read.
func (e *experienceConfig) freshPublished(ctx context.Context, universeID, repo string) (opencloud.ConfigValues, error) {
	var best opencloud.ConfigValues
	for i := 0; i < freshReads; i++ {
		if i > 0 {
			e.sleep(300 * time.Millisecond)
		}
		pub, err := e.cc.GetPublished(ctx, universeID, repo)
		if err != nil {
			return opencloud.ConfigValues{}, err
		}
		if i == 0 || pub.Version > best.Version {
			best = pub
		}
	}
	return best, nil
}

// reconcileRounds bounds the extra PATCH+publish rounds publishChanges runs to
// remove keys a stale initial read hid from it.
const reconcileRounds = 2

// publishChanges publishes changes and then, for an Immediate publish, checks
// the result (read at the published version, so not stale) for plain keys that
// shouldn't be there; keep reports which base keys belong. Reads flap between
// versions for a few seconds after a publish (verified live), so the read the
// deletions were computed from can miss keys; leftovers are deleted in a
// follow-up round.
func (e *experienceConfig) publishChanges(ctx context.Context, universeID, repo string, changes map[string]json.RawMessage, pub opencloud.ConfigValues, strategy string, keep func(string) bool) (int64, error) {
	for round := 0; ; round++ {
		version, after, err := e.publishOnce(ctx, universeID, repo, changes, pub, strategy)
		if err != nil || after == nil || round >= reconcileRounds {
			return version, err
		}
		leftovers := map[string]json.RawMessage{}
		for k := range after.Entries {
			if !isBranchKey(k) && !keep(k) {
				leftovers[k] = nil
			}
		}
		if len(leftovers) == 0 {
			return version, nil
		}
		changes, pub = leftovers, *after
	}
}

// publishOnce stages changes (verified by stage), publishes them and, for an
// Immediate publish, waits for the new version and checks nothing Baseplate
// preserves went missing, returning that read (nil when not awaited). A no-op
// publish (EmptyDraft) is success: the draft is discarded and the current
// version returned.
func (e *experienceConfig) publishOnce(ctx context.Context, universeID, repo string, changes map[string]json.RawMessage, pub opencloud.ConfigValues, strategy string) (int64, *opencloud.ConfigValues, error) {
	hash, err := e.stage(ctx, universeID, repo, changes, pub)
	if err != nil {
		return 0, nil, err
	}
	version, err := e.cc.Publish(ctx, universeID, repo, hash, configPublishMessage, strategy)
	// Observed live: the hash returned by the PATCH can already be stale when we
	// publish ("The provided draft hash does not match the current draft."). Re-read
	// the draft, re-verify it is still exactly what we staged, and publish with its
	// current hash.
	for attempt := 0; opencloud.IsDraftHashMismatch(err) && attempt < stageAttempts; attempt++ {
		e.sleep(time.Duration(attempt+1) * time.Second)
		draft, derr := e.cc.GetDraft(ctx, universeID, repo)
		if derr != nil {
			return 0, nil, fmt.Errorf("roblox_experience_config: re-reading the staged draft: %w", derr)
		}
		if problem := unintendedChanges(draft, changes, pub); problem != "" {
			_ = e.cc.DiscardDraft(ctx, universeID, repo, "")
			return 0, nil, fmt.Errorf("roblox_experience_config: universe %s repository %s: the staged draft changed under Baseplate and now %s; it was discarded and nothing was published; retry shortly", universeID, repo, problem)
		}
		hash = draft.Hash
		version, err = e.cc.Publish(ctx, universeID, repo, hash, configPublishMessage, strategy)
	}
	switch {
	case opencloud.IsEmptyDraft(err):
		// Nothing changed remotely (e.g. drift already reverted). Don't leave the
		// no-op draft behind, because it would block the next apply.
		if derr := e.discard(ctx, universeID, repo, hash); derr != nil {
			return 0, nil, fmt.Errorf("roblox_experience_config: discarding no-op draft: %w", derr)
		}
		version = pub.Version
	case err != nil:
		if derr := e.discard(ctx, universeID, repo, hash); derr != nil {
			return 0, nil, fmt.Errorf("roblox_experience_config: publish failed: %w (and discarding the staged draft also failed: %v)", err, derr)
		}
		return 0, nil, fmt.Errorf("roblox_experience_config: publish failed (staged draft discarded): %w", err)
	}

	if strategy == "Immediate" && version > pub.Version {
		if after, ok := e.awaitVersion(ctx, universeID, repo, version); ok {
			if lost := lostPreserved(pub, after, changes); lost != "" {
				return 0, nil, fmt.Errorf("roblox_experience_config: universe %s repository %s: Roblox published version %d WITHOUT %s, which Baseplate did not ask to remove; restore version %d from the config's version history in Creator Hub (or POST …/revisions/{id}/restore)", universeID, repo, version, lost, pub.Version)
			}
			return version, &after, nil
		}
	}

	return version, nil, nil
}

// discard resets the draft Baseplate just staged. The hash can already be stale
// (see publishChanges); the draft is ours (refusePendingDraft ran first), so on
// a hash mismatch it is discarded without the concurrency check.
func (e *experienceConfig) discard(ctx context.Context, universeID, repo, hash string) error {
	err := e.cc.DiscardDraft(ctx, universeID, repo, hash)
	if opencloud.IsDraftHashMismatch(err) {
		err = e.cc.DiscardDraft(ctx, universeID, repo, "")
	}
	return err
}

// stageAttempts bounds how often a draft that came out wrong is discarded and
// re-staged before giving up.
const stageAttempts = 3

// stage PATCHes the draft and verifies, before anything is published, that it
// stages no deletion we didn't ask for and no conditional-rule change. Observed
// live (rarely, right after a previous publish): a PATCH that sent no rules
// produced a draft that also deleted every conditional rule and branch. Such a
// draft is discarded and re-staged; if it keeps happening, apply fails without
// publishing.
func (e *experienceConfig) stage(ctx context.Context, universeID, repo string, changes map[string]json.RawMessage, pub opencloud.ConfigValues) (string, error) {
	var problem string
	for attempt := 0; attempt < stageAttempts; attempt++ {
		if attempt > 0 {
			e.sleep(time.Duration(attempt) * 3 * time.Second)
		}
		hash, err := e.cc.PatchDraft(ctx, universeID, repo, changes)
		if opencloud.IsMultipleDraft(err) {
			return "", fmt.Errorf("roblox_experience_config: universe %s repository %s: a previous publish is still in flight (a GradualRollout publish takes ~20 minutes to finish); retry once it completes: %w", universeID, repo, err)
		}
		if err != nil {
			return "", fmt.Errorf("roblox_experience_config: staging draft: %w", err)
		}
		draft, err := e.cc.GetDraft(ctx, universeID, repo)
		if err != nil {
			return "", fmt.Errorf("roblox_experience_config: reading back the staged draft: %w", err)
		}
		problem = unintendedChanges(draft, changes, pub)
		if problem == "" {
			return hash, nil
		}
		if derr := e.discard(ctx, universeID, repo, hash); derr != nil {
			return "", fmt.Errorf("roblox_experience_config: the staged draft %s, and discarding it failed: %v", problem, derr)
		}
	}
	return "", fmt.Errorf("roblox_experience_config: universe %s repository %s: Roblox staged a draft that %s (not requested by Baseplate); it was discarded and nothing was published; retry shortly", universeID, repo, problem)
}

// unintendedChanges returns a description of anything the staged draft would do
// beyond `changes`: deleting a key we didn't null (or a branch of a key we
// kept), or changing conditional rules. "" means the draft is as requested.
func unintendedChanges(draft *opencloud.ConfigDraft, changes map[string]json.RawMessage, pub opencloud.ConfigValues) string {
	if draft == nil {
		return "could not be read back"
	}
	var bad []string
	for k, v := range draft.Entries {
		if len(v) != 0 && string(v) != "null" {
			continue // a value, not a deletion
		}
		key := k
		if base, ok := branchBase(k); ok {
			key = base
		}
		if c, ok := changes[key]; ok && c == nil {
			continue // we deleted this key (and so its branches)
		}
		bad = append(bad, k)
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return "would delete " + strings.Join(bad, ", ")
	}
	if draft.ConditionalRules != nil && !sameJSONValue(draft.ConditionalRules, pub.ConditionalRules) {
		return "would change the conditional rules"
	}
	return ""
}

// sameJSONValue compares two JSON documents semantically.
func sameJSONValue(a, b json.RawMessage) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// awaitVersion waits (bounded, best-effort) until the values endpoint serves
// the version just published. Observed live: a read issued right after an
// Immediate publish can still return the previous version, which would make the
// next plan show the just-applied change as drift.
// It returns the served config once it has caught up (ok=false if it never did).
func (e *experienceConfig) awaitVersion(ctx context.Context, universeID, repo string, version int64) (opencloud.ConfigValues, bool) {
	for i := 0; i < 10; i++ {
		pub, err := e.cc.GetPublished(ctx, universeID, repo)
		if err != nil {
			return opencloud.ConfigValues{}, false
		}
		if pub.Version >= version {
			return pub, true
		}
		e.sleep(time.Second)
	}
	return opencloud.ConfigValues{}, false
}

// lostPreserved reports conditional rules or kept keys' branches that existed
// before the publish but are gone after it: a safety net behind the draft check
// in stage (Baseplate never asks for either to be removed).
func lostPreserved(before, after opencloud.ConfigValues, changes map[string]json.RawMessage) string {
	var lost []string
	for k := range before.Entries {
		base, ok := branchBase(k)
		if !ok {
			continue
		}
		if c, sent := changes[base]; sent && c == nil {
			continue // we deleted the base key
		}
		if _, still := after.Entries[k]; !still {
			lost = append(lost, k)
		}
	}
	sort.Strings(lost)
	if before.ConditionalRules != nil && !sameJSONValue(before.ConditionalRules, after.ConditionalRules) {
		lost = append(lost, "conditional rules")
	}
	return strings.Join(lost, ", ")
}

// desiredEntries validates the `entries` value and encodes each value as JSON.
func desiredEntries(desired provider.Attributes) (map[string]json.RawMessage, error) {
	v, ok := desired["entries"]
	if !ok || v.IsNull() {
		return nil, errors.New("roblox_experience_config: entries is required (use {} for an empty repository)")
	}
	if !v.IsWhollyKnown() {
		return nil, errors.New("roblox_experience_config: entries must be fully known (references inside entries aren't supported)")
	}
	ty := v.Type()
	if !ty.IsObjectType() && !ty.IsMapType() {
		return nil, fmt.Errorf("roblox_experience_config: entries must be a map of key = value, got %s", ty.FriendlyName())
	}
	out := map[string]json.RawMessage{}
	for it := v.ElementIterator(); it.Next(); {
		kv, ev := it.Element()
		k := kv.AsString()
		switch {
		case k == "" || len(k) > configMaxKeyLen:
			return nil, fmt.Errorf("roblox_experience_config: entries key %q must be 1-%d characters", k, configMaxKeyLen)
		case strings.Contains(k, ".RBX."):
			return nil, fmt.Errorf("roblox_experience_config: entries key %q: the segment \".RBX.\" is reserved by Roblox (conditional values are managed in Creator Hub and preserved by Baseplate)", k)
		case ev.IsNull():
			return nil, fmt.Errorf("roblox_experience_config: entries[%q] is null; remove the key instead", k)
		}
		raw, err := ctyjson.Marshal(ev, ev.Type())
		if err != nil {
			return nil, fmt.Errorf("roblox_experience_config: entries[%q]: %w", k, err)
		}
		out[k] = raw
	}
	if len(out) > configMaxKeys {
		return nil, fmt.Errorf("roblox_experience_config: entries has %d keys; Roblox allows at most %d per repository", len(out), configMaxKeys)
	}
	return out, nil
}

// entriesToCty converts the published base keys (conditional branches excluded)
// to a cty object whose attribute types are implied from the JSON, matching what
// the equivalent HCL literal decodes to (objects, tuples, numbers, ...).
func entriesToCty(entries map[string]json.RawMessage) (cty.Value, error) {
	attrs := map[string]cty.Value{}
	for k, raw := range entries {
		if isBranchKey(k) {
			continue
		}
		ty, err := ctyjson.ImpliedType(raw)
		if err != nil {
			return cty.NilVal, fmt.Errorf("entry %q: %w", k, err)
		}
		v, err := ctyjson.Unmarshal(raw, ty)
		if err != nil {
			return cty.NilVal, fmt.Errorf("entry %q: %w", k, err)
		}
		attrs[k] = v
	}
	if len(attrs) == 0 {
		return cty.EmptyObjectVal, nil
	}
	return cty.ObjectVal(attrs), nil
}

func isBranchKey(k string) bool { _, ok := branchBase(k); return ok }

// branchBase returns the base key of a conditional-branch key
// ("speed.RBX.conditional.c1" -> "speed").
func branchBase(k string) (string, bool) {
	i := strings.Index(k, opencloud.ConfigBranchMarker)
	if i <= 0 {
		return "", false
	}
	return k[:i], true
}

// keyList renders up to 10 sorted keys for an error message.
func keyList(m map[string]json.RawMessage) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return "none"
	}
	if len(keys) > 10 {
		return strings.Join(keys[:10], ", ") + fmt.Sprintf(", … %d more", len(keys)-10)
	}
	return strings.Join(keys, ", ")
}

// splitConfigID parses "<universeID>" or "<universeID>/<repository>".
func splitConfigID(id string) (string, string, error) {
	universeID, repo, hasRepo := strings.Cut(id, "/")
	if universeID == "" || (hasRepo && (repo == "" || strings.Contains(repo, "/"))) {
		return "", "", fmt.Errorf("roblox_experience_config: invalid id %q (want <universe_id> or <universe_id>/<repository>)", id)
	}
	if !hasRepo {
		repo = opencloud.DefaultConfigRepository
	}
	return universeID, repo, nil
}
