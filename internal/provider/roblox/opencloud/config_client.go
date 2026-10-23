package opencloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultConfigRepository is the only Experience Config repository Roblox
// documents for public use (values read in-game through ConfigService).
const DefaultConfigRepository = "InExperienceConfig"

// ConfigBranchMarker separates a base key from a conditional-branch id in the
// flat entry map the Experience Config API returns and accepts: the value a
// player matching condition "c1" receives for key "speed" lives under the entry
// key "speed.RBX.conditional.c1" (verified live; undocumented in the spec).
const ConfigBranchMarker = ".RBX.conditional."

// ConfigValues is a repository's published state as returned by the values
// endpoint. Entry values are kept as raw JSON so callers decide how to type
// them; ConditionalRules is passed through verbatim (nil when there are none).
type ConfigValues struct {
	Version          int64
	Entries          map[string]json.RawMessage
	ConditionalRules json.RawMessage
}

// ConfigDraft is a repository's pending (unpublished) draft.
type ConfigDraft struct {
	Hash             string
	Entries          map[string]json.RawMessage
	ConditionalRules json.RawMessage
}

// ConfigOverwrite is the full intended post-publish state sent to
// draft:overwrite. Omitting ConditionalRules DELETES every published conditional
// rule, so callers that don't manage rules must pass the published rules back.
type ConfigOverwrite struct {
	PreviousDraftHash string
	Entries           map[string]json.RawMessage
	ConditionalRules  json.RawMessage
}

// ExperienceConfigClient talks to the creator-configs-public-api (Experience
// Config, BETA). Writes are draft-then-publish: OverwriteDraft stages the full
// state and returns a draft hash that Publish makes live.
type ExperienceConfigClient interface {
	// GetPublished returns the published values. A repository that has never
	// been published is returned as empty (the API 404s for it), not an error.
	GetPublished(ctx context.Context, universeID, repo string) (ConfigValues, error)
	// GetDraft returns the pending draft, or nil when there is none.
	GetDraft(ctx context.Context, universeID, repo string) (*ConfigDraft, error)
	OverwriteDraft(ctx context.Context, universeID, repo string, w ConfigOverwrite) (draftHash string, err error)
	// PatchDraft merges changes into the draft (created from the published
	// config if none): keys absent from changes are untouched, a JSON-null value
	// deletes the key (and, verified live, its conditional branches), and
	// conditional rules are left as they are.
	PatchDraft(ctx context.Context, universeID, repo string, changes map[string]json.RawMessage) (draftHash string, err error)
	// Publish makes the draft live. An empty draftHash publishes without the
	// concurrency check. Returns the new config version.
	Publish(ctx context.Context, universeID, repo, draftHash, message, strategy string) (int64, error)
	// DiscardDraft resets the pending draft (no-op when there is none).
	DiscardDraft(ctx context.Context, universeID, repo, draftHash string) error
}

func NewExperienceConfigClient(c *Client) ExperienceConfigClient { return &configClient{c: c} }

type configClient struct{ c *Client }

func configBase(universeID, repo string) string {
	return "/creator-configs-public-api/v1/configs/universes/" + url.PathEscape(universeID) +
		"/repositories/" + url.PathEscape(repo)
}

type configValuesWire struct {
	Metadata *struct {
		ConfigVersion int64 `json:"configVersion"`
	} `json:"metadata"`
	Entries          map[string]json.RawMessage `json:"entries"`
	ConditionalRules json.RawMessage            `json:"conditionalRules"`
}

type configDraftWire struct {
	DraftHash string `json:"draftHash"`
	Entries   map[string]struct {
		Value json.RawMessage `json:"value"`
	} `json:"entries"`
	ConditionalRules json.RawMessage `json:"conditionalRules"`
}

type configOverwriteWire struct {
	DraftHash        string                     `json:"draftHash,omitempty"`
	Entries          map[string]json.RawMessage `json:"entries"`
	ConditionalRules json.RawMessage            `json:"conditionalRules,omitempty"`
}

type configPublishWire struct {
	DraftHash          string `json:"draftHash,omitempty"`
	Message            string `json:"message,omitempty"`
	DeploymentStrategy string `json:"deploymentStrategy,omitempty"`
}

type draftHashWire struct {
	DraftHash string `json:"draftHash"`
}

func (cc *configClient) GetPublished(ctx context.Context, universeID, repo string) (ConfigValues, error) {
	var w configValuesWire
	for attempt := 0; ; attempt++ {
		_, err := cc.c.Do(ctx, http.MethodGet, configBase(universeID, repo), nil, &w)
		if err == nil {
			break
		}
		if !IsNotFound(err) {
			return ConfigValues{}, err
		}
		// A 404 means "never published" — but the values endpoint has also been
		// seen to 404 transiently right after a publish. Reading that as "empty"
		// would show every key as drift, so confirm against the revision history.
		published, rerr := cc.hasRevisions(ctx, universeID, repo)
		if rerr != nil {
			return ConfigValues{}, rerr
		}
		if !published {
			return ConfigValues{Entries: map[string]json.RawMessage{}}, nil
		}
		if attempt >= len(etagRetryDelays) {
			return ConfigValues{}, fmt.Errorf("published config of universe %s repository %s is temporarily unavailable (404 despite published revisions): %w", universeID, repo, err)
		}
		cc.c.sleep(etagRetryDelays[attempt])
	}
	out := ConfigValues{Entries: w.Entries, ConditionalRules: nullToNil(w.ConditionalRules)}
	if out.Entries == nil {
		out.Entries = map[string]json.RawMessage{}
	}
	if w.Metadata != nil {
		out.Version = w.Metadata.ConfigVersion
	}
	return out, nil
}

// hasRevisions reports whether the repository has ever been published. The
// revisions endpoint 404s (text/plain "ResourceNotFound") for a never-published
// repository (verified live).
func (cc *configClient) hasRevisions(ctx context.Context, universeID, repo string) (bool, error) {
	var w struct {
		Revisions []json.RawMessage `json:"revisions"`
	}
	_, err := cc.c.Do(ctx, http.MethodGet, configBase(universeID, repo)+"/revisions?MaxPageSize=1", nil, &w)
	if IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(w.Revisions) > 0, nil
}

func (cc *configClient) GetDraft(ctx context.Context, universeID, repo string) (*ConfigDraft, error) {
	var w configDraftWire
	if _, err := cc.c.Do(ctx, http.MethodGet, configBase(universeID, repo)+"/draft", nil, &w); err != nil {
		if IsNotFound(err) { // no pending draft
			return nil, nil
		}
		return nil, err
	}
	d := &ConfigDraft{Hash: w.DraftHash, Entries: map[string]json.RawMessage{}, ConditionalRules: nullToNil(w.ConditionalRules)}
	for k, e := range w.Entries {
		d.Entries[k] = e.Value
	}
	return d, nil
}

func (cc *configClient) OverwriteDraft(ctx context.Context, universeID, repo string, w ConfigOverwrite) (string, error) {
	entries := w.Entries
	if entries == nil {
		entries = map[string]json.RawMessage{}
	}
	body := configOverwriteWire{DraftHash: w.PreviousDraftHash, Entries: entries, ConditionalRules: nullToNil(w.ConditionalRules)}
	var got draftHashWire
	if err := cc.doETagRetry(ctx, http.MethodPut, configBase(universeID, repo)+"/draft:overwrite", body, &got); err != nil {
		return "", err
	}
	return got.DraftHash, nil
}

func (cc *configClient) PatchDraft(ctx context.Context, universeID, repo string, changes map[string]json.RawMessage) (string, error) {
	entries := make(map[string]json.RawMessage, len(changes))
	for k, v := range changes {
		if len(v) == 0 {
			v = json.RawMessage("null") // deletion
		}
		entries[k] = v
	}
	// conditionalRules omitted: PATCH leaves the existing rules unchanged.
	body := struct {
		Entries map[string]json.RawMessage `json:"entries"`
	}{entries}
	var got draftHashWire
	if err := cc.doETagRetry(ctx, http.MethodPatch, configBase(universeID, repo)+"/draft", body, &got); err != nil {
		return "", err
	}
	return got.DraftHash, nil
}

func (cc *configClient) Publish(ctx context.Context, universeID, repo, draftHash, message, strategy string) (int64, error) {
	body := configPublishWire{DraftHash: draftHash, Message: message, DeploymentStrategy: strategy}
	var got struct {
		ConfigVersion int64 `json:"configVersion"`
	}
	if err := cc.doETagRetry(ctx, http.MethodPost, configBase(universeID, repo)+"/publish", body, &got); err != nil {
		return 0, err
	}
	return got.ConfigVersion, nil
}

// etagRetryDelays is the backoff for ETagMismatch (total ~15s).
var etagRetryDelays = []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second, 5 * time.Second}

// doETagRetry retries a write rejected with a transient 400. Observed live: a
// draft write issued within a few seconds of a previous draft write/publish is
// rejected with "ETagMismatch" (the service's own concurrency token lags) or
// "MultipleDraftNotSupported" (the previous publish is still deploying — even an
// Immediate one, briefly; a GradualRollout one for ~20 minutes), and succeeds
// shortly after. The rejection means nothing was applied, so retrying is safe.
func (cc *configClient) doETagRetry(ctx context.Context, method, path string, body, out any) error {
	for attempt := 0; ; attempt++ {
		_, err := cc.c.Do(ctx, method, path, body, out)
		if !(isETagMismatch(err) || IsMultipleDraft(err)) || attempt >= len(etagRetryDelays) {
			return err
		}
		cc.c.sleep(etagRetryDelays[attempt])
	}
}

func isETagMismatch(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusBadRequest && strings.Contains(e.Message, "ETagMismatch")
}

func (cc *configClient) DiscardDraft(ctx context.Context, universeID, repo, draftHash string) error {
	body := struct {
		DraftHash string `json:"draftHash,omitempty"`
	}{draftHash}
	_, err := cc.c.Do(ctx, http.MethodDelete, configBase(universeID, repo)+"/draft", body, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// IsEmptyDraft reports whether a publish failed because the draft has no
// changes from the published configuration (400, validation code EmptyDraft).
func IsEmptyDraft(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusBadRequest && strings.Contains(e.Message, "EmptyDraft")
}

// IsMultipleDraft reports whether a draft write was rejected because another
// publish is still in flight — observed live for a few seconds after an
// Immediate publish and for the whole of a GradualRollout (400, text/plain
// "MultipleDraftNotSupported"; GET draft 404s meanwhile, so it can't be detected
// up front). Draft writes retry it briefly before surfacing it.
func IsMultipleDraft(err error) bool {
	var e *APIError
	return errors.As(err, &e) && e.Status == http.StatusBadRequest && strings.Contains(e.Message, "MultipleDraftNotSupported")
}

// nullToNil maps an absent or JSON-null raw value to nil.
func nullToNil(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}
