package resources

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// eventTimeLayout is the canonical form of start_time/end_time in state and in
// Read output: RFC 3339, UTC, whole seconds. Config may use any RFC 3339 offset
// or precision. Roblox reads back "+00:00" with milliseconds (verified live), so
// Normalize and Read both convert to this form and equivalent instants never
// diff. The canonical string is also what gets sent.
const eventTimeLayout = "2006-01-02T15:04:05Z"

// eventMinDuration is the API's minimum event length (live: "Event end time must
// be at least 15 minutes after the start time").
const eventMinDuration = 15 * time.Minute

type experienceEvent struct {
	ec opencloud.GameEventClient
}

// ExperienceEvent returns the roblox_experience_event resource: an experience
// (virtual) event on the virtual-events/v3 API. Its state id is the bare event
// id (the single-event endpoints are not universe-scoped), which is also the
// import id. universe_id is create-only and typically a reference to a
// roblox_experience's id.
func ExperienceEvent(ec opencloud.GameEventClient) provider.Resource {
	return &experienceEvent{ec: ec}
}

func (e *experienceEvent) Type() string { return "roblox_experience_event" }

func (e *experienceEvent) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages an experience event (virtual event) for live-ops: a scheduled, titled event shown on the experience page. Uses the EXPERIMENTAL `virtual-events/v3` Open Cloud API.",
		Attributes: map[string]string{
			"universe_id":         "The universe that owns the event. Often a reference to an experience. Create-only.",
			"title":               "Event title.",
			"subtitle":            "Event subtitle (required by Roblox).",
			"description":         "Event description. Set `\"\"` to clear it.",
			"tagline":             "Tagline shown to curators when featuring is enabled. Can't be cleared once set (Roblox ignores an empty tagline), so it must not be `\"\"`.",
			"start_time":          "Start time, RFC 3339 (e.g. `2026-12-01T18:00:00Z`). Any offset is accepted; it is compared and stored in UTC at whole-second precision. Must be in the future when the event is created or the start is changed.",
			"end_time":            "End time, RFC 3339. Must be at least 15 minutes after `start_time`.",
			"visibility":          "`public` or `private` (case-insensitive). Roblox may set `moderated` server-side, which then shows as drift.",
			"place_id":            "Venue place id (a place in the universe). When unset, Roblox records no venue (`placeId` 0).",
			"group_id":            "Host group id. When unset, the API key's owner hosts the event. Create-only.",
			"thumbnail_media_ids": "Ordered list of thumbnail image asset ids (the first is the primary). `[]` clears them.",
			"featuring_enabled":   "Opt the event into featuring review (`featuringStatus`). Defaults to Roblox's \"not featured\".",
		},
		Notes: "`destroy` permanently deletes the event. Import by event id (`baseplate import roblox_experience_event.x <eventId>`). " +
			"Changing `universe_id` or `group_id` does not move the event (both are create-only). " +
			"An update sends only the fields that differ from the live event. Events that have started or ended can still be edited and deleted, " +
			"but Roblox rejects any *new* `start_time` in the past. Categories and recurrence are not supported: " +
			"the API currently rejects every categories payload, and recurrence has no effect.",
		Scopes:      []string{"universe.event:read", "universe.event:write"},
		ExampleFile: "examples/experience-event.bp.hcl",
	}
}

func (e *experienceEvent) DecodeSpec() hcldec.Spec {
	return hcldec.ObjectSpec{
		"universe_id":         &hcldec.AttrSpec{Name: "universe_id", Type: cty.String, Required: true},
		"title":               &hcldec.AttrSpec{Name: "title", Type: cty.String, Required: true},
		"subtitle":            &hcldec.AttrSpec{Name: "subtitle", Type: cty.String, Required: true},
		"description":         &hcldec.AttrSpec{Name: "description", Type: cty.String},
		"tagline":             &hcldec.AttrSpec{Name: "tagline", Type: cty.String},
		"start_time":          &hcldec.AttrSpec{Name: "start_time", Type: cty.String, Required: true},
		"end_time":            &hcldec.AttrSpec{Name: "end_time", Type: cty.String, Required: true},
		"visibility":          &hcldec.AttrSpec{Name: "visibility", Type: cty.String, Required: true},
		"place_id":            &hcldec.AttrSpec{Name: "place_id", Type: cty.String},
		"group_id":            &hcldec.AttrSpec{Name: "group_id", Type: cty.String},
		"thumbnail_media_ids": &hcldec.AttrSpec{Name: "thumbnail_media_ids", Type: cty.List(cty.String)},
		"featuring_enabled":   &hcldec.AttrSpec{Name: "featuring_enabled", Type: cty.Bool},
	}
}

// Computed excludes from diff: the state id; universe_id (create-only, often a
// resolved reference); group_id (create-only; the API reports the host, not the
// requested group).
func (e *experienceEvent) Computed() []string {
	return []string{"id", "universe_id", "group_id"}
}

func (e *experienceEvent) Dependencies(provider.Attributes) []provider.Ref { return nil }

// Normalize canonicalises start_time/end_time (UTC, whole seconds) and
// lowercases visibility, rejecting values the API would reject (bad timestamps,
// an event shorter than 15 minutes, other visibilities, an empty tagline) at
// plan time. Null/unknown values pass through untouched.
func (e *experienceEvent) Normalize(desired provider.Attributes) (provider.Attributes, error) {
	out := make(provider.Attributes, len(desired))
	for k, v := range desired {
		out[k] = v
	}
	var start, end time.Time
	for _, k := range []string{"start_time", "end_time"} {
		v, ok := evKnown(desired, k)
		if !ok {
			continue
		}
		t, err := time.Parse(time.RFC3339, v.AsString())
		if err != nil {
			return nil, fmt.Errorf("%s %q is not an RFC 3339 timestamp (e.g. 2026-12-01T18:00:00Z)", k, v.AsString())
		}
		t = t.UTC().Truncate(time.Second)
		out[k] = cty.StringVal(t.Format(eventTimeLayout))
		if k == "start_time" {
			start = t
		} else {
			end = t
		}
	}
	if !start.IsZero() && !end.IsZero() && end.Sub(start) < eventMinDuration {
		return nil, fmt.Errorf("end_time (%s) must be at least 15 minutes after start_time (%s)", end.Format(eventTimeLayout), start.Format(eventTimeLayout))
	}
	if v, ok := evKnown(desired, "visibility"); ok {
		vis := strings.ToLower(v.AsString())
		if vis != "public" && vis != "private" {
			return nil, fmt.Errorf("visibility %q must be \"public\" or \"private\"", v.AsString())
		}
		out["visibility"] = cty.StringVal(vis)
	}
	if v, ok := evKnown(desired, "tagline"); ok && v.AsString() == "" {
		return nil, fmt.Errorf("tagline must not be empty (Roblox ignores an empty tagline, so it can't be cleared); remove the attribute instead")
	}
	return out, nil
}

func (e *experienceEvent) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	n, err := e.Normalize(desired)
	if err != nil {
		return "", nil, fmt.Errorf("roblox_experience_event: %w", err)
	}
	universeID := n["universe_id"].AsString()
	w := evWrite(n)
	w.GroupID = evStrPtr(n, "group_id")
	got, err := e.ec.Create(ctx, universeID, w)
	if err != nil {
		return "", nil, err
	}
	return got.ID, evStateAttrs(n), nil
}

func (e *experienceEvent) Read(ctx context.Context, id string) (provider.Attributes, error) {
	got, err := e.ec.Get(ctx, id)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	out := provider.Attributes{
		"universe_id":         cty.StringVal(got.UniverseID),
		"title":               cty.StringVal(got.Title),
		"subtitle":            cty.StringVal(got.Subtitle),
		"description":         cty.StringVal(got.Description),
		"start_time":          cty.StringVal(evCanonicalTime(got.StartTime)),
		"end_time":            cty.StringVal(evCanonicalTime(got.EndTime)),
		"visibility":          cty.StringVal(strings.ToLower(got.Visibility)),
		"thumbnail_media_ids": evStringList(evStringVals(got.ThumbnailMediaIDs)),
	}
	if got.Tagline != "" {
		out["tagline"] = cty.StringVal(got.Tagline)
	}
	if got.PlaceID != "" {
		out["place_id"] = cty.StringVal(got.PlaceID)
	}
	if got.FeaturingStatus != "" {
		out["featuring_enabled"] = cty.BoolVal(strings.EqualFold(got.FeaturingStatus, "enabled"))
	}
	return out, nil
}

// Update PATCHes only the fields that differ from the live event (one extra
// GET). Live, Roblox accepts re-sending an already-started event's unchanged
// start time but rejects any *changed* start in the past ("New event start
// time must be after the current time"); sending only real changes keeps the
// request minimal and ties any such rejection to an actual start_time edit.
func (e *experienceEvent) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	n, err := e.Normalize(desired)
	if err != nil {
		return nil, fmt.Errorf("roblox_experience_event: %w", err)
	}
	cur, err := e.ec.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	w := evWrite(n)
	same := func(p **string, actual string) {
		if *p != nil && **p == actual {
			*p = nil
		}
	}
	same(&w.Title, cur.Title)
	same(&w.Subtitle, cur.Subtitle)
	same(&w.Description, cur.Description)
	same(&w.Tagline, cur.Tagline)
	same(&w.StartTime, evCanonicalTime(cur.StartTime))
	same(&w.EndTime, evCanonicalTime(cur.EndTime))
	same(&w.Visibility, strings.ToLower(cur.Visibility))
	same(&w.FeaturingStatus, strings.ToLower(cur.FeaturingStatus))
	same(&w.PlaceID, cur.PlaceID)
	if w.ThumbnailMediaIDs != nil && strings.Join(w.ThumbnailMediaIDs, ",") == strings.Join(cur.ThumbnailMediaIDs, ",") {
		w.ThumbnailMediaIDs = nil
	}
	if _, err := e.ec.Update(ctx, id, w); err != nil {
		return nil, err
	}
	return evStateAttrs(n), nil
}

// Delete permanently deletes the event. An already-deleted event is success.
func (e *experienceEvent) Delete(ctx context.Context, id string) error {
	if err := e.ec.Delete(ctx, id); err != nil && !opencloud.IsNotFound(err) {
		return err
	}
	return nil
}

// evWrite maps (normalized) config to the client input. Only declared
// attributes are set; PATCH treats omitted fields as "no change".
func evWrite(n provider.Attributes) opencloud.GameEventWrite {
	w := opencloud.GameEventWrite{
		Title:       evStrPtr(n, "title"),
		Subtitle:    evStrPtr(n, "subtitle"),
		Description: evStrPtr(n, "description"),
		Tagline:     evStrPtr(n, "tagline"),
		StartTime:   evStrPtr(n, "start_time"),
		EndTime:     evStrPtr(n, "end_time"),
		Visibility:  evStrPtr(n, "visibility"),
		PlaceID:     evStrPtr(n, "place_id"),
	}
	if v, ok := evKnown(n, "featuring_enabled"); ok {
		s := "disabled"
		if v.True() {
			s = "enabled"
		}
		w.FeaturingStatus = &s
	}
	w.ThumbnailMediaIDs = evStrSlice(n, "thumbnail_media_ids")
	return w
}

// evStateAttrs records the declared (normalized) attributes in state.
func evStateAttrs(n provider.Attributes) provider.Attributes {
	out := provider.Attributes{}
	for k, v := range n {
		if !v.IsNull() {
			out[k] = v
		}
	}
	return out
}

func evKnown(a provider.Attributes, k string) (cty.Value, bool) {
	v, ok := a[k]
	if !ok || v.IsNull() || !v.IsKnown() {
		return cty.NilVal, false
	}
	return v, true
}

func evStrPtr(a provider.Attributes, k string) *string {
	v, ok := evKnown(a, k)
	if !ok {
		return nil
	}
	s := v.AsString()
	return &s
}

// evStrSlice returns a declared list(string) attribute as a non-nil slice (an
// explicit empty list clears it remotely), or nil when the attribute is unset.
func evStrSlice(a provider.Attributes, k string) []string {
	v, ok := evKnown(a, k)
	if !ok {
		return nil
	}
	out := []string{}
	for it := v.ElementIterator(); it.Next(); {
		_, e := it.Element()
		out = append(out, e.AsString())
	}
	return out
}

func evStringVals(ss []string) []cty.Value {
	out := make([]cty.Value, 0, len(ss))
	for _, s := range ss {
		out = append(out, cty.StringVal(s))
	}
	return out
}

func evStringList(vs []cty.Value) cty.Value {
	if len(vs) == 0 {
		return cty.ListValEmpty(cty.String)
	}
	return cty.ListVal(vs)
}

// evCanonicalTime converts an API timestamp to eventTimeLayout. An unparseable
// value is returned verbatim, so it shows as drift rather than being hidden.
func evCanonicalTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.UTC().Truncate(time.Second).Format(eventTimeLayout)
}
