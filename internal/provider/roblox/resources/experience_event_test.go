package resources

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/state"
)

// fakeEvents mimics the API: it stores what was written and, on read, reports
// times in a DIFFERENT but equivalent representation (fractional seconds) to
// prove Normalize/Read converge.
type fakeEvents struct {
	store       map[string]opencloud.GameEvent
	lastCreate  opencloud.GameEventWrite
	lastUpdate  opencloud.GameEventWrite
	createdIn   string
	getErr      error
	deleteErr   error
	deleted     []string
	updateCalls int
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func (f *fakeEvents) apply(ev *opencloud.GameEvent, w opencloud.GameEventWrite) {
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = *v
		}
	}
	set(&ev.Title, w.Title)
	set(&ev.Subtitle, w.Subtitle)
	set(&ev.Description, w.Description)
	set(&ev.Tagline, w.Tagline)
	set(&ev.Visibility, w.Visibility)
	set(&ev.FeaturingStatus, w.FeaturingStatus)
	set(&ev.PlaceID, w.PlaceID)
	if w.StartTime != nil {
		ev.StartTime = strings.TrimSuffix(*w.StartTime, "Z") + ".000Z"
	}
	if w.EndTime != nil {
		ev.EndTime = strings.TrimSuffix(*w.EndTime, "Z") + ".000Z"
	}
	if w.Categories != nil {
		ev.Categories = w.Categories
	}
	if w.ThumbnailMediaIDs != nil {
		ev.ThumbnailMediaIDs = w.ThumbnailMediaIDs
	}
}

func (f *fakeEvents) Create(_ context.Context, universeID string, w opencloud.GameEventWrite) (opencloud.GameEvent, error) {
	f.lastCreate, f.createdIn = w, universeID
	ev := opencloud.GameEvent{ID: "9007199254740993123", UniverseID: universeID, PlaceID: "999", FeaturingStatus: "disabled"}
	f.apply(&ev, w)
	if f.store == nil {
		f.store = map[string]opencloud.GameEvent{}
	}
	f.store[ev.ID] = ev
	return ev, nil
}
func (f *fakeEvents) Get(_ context.Context, id string) (opencloud.GameEvent, error) {
	if f.getErr != nil {
		return opencloud.GameEvent{}, f.getErr
	}
	ev, ok := f.store[id]
	if !ok {
		return opencloud.GameEvent{}, &opencloud.APIError{Status: 404}
	}
	return ev, nil
}
func (f *fakeEvents) Update(_ context.Context, id string, w opencloud.GameEventWrite) (opencloud.GameEvent, error) {
	f.lastUpdate = w
	f.updateCalls++
	ev := f.store[id]
	f.apply(&ev, w)
	f.store[id] = ev
	return ev, nil
}
func (f *fakeEvents) Delete(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleted = append(f.deleted, id)
	delete(f.store, id)
	return nil
}

func eventDesired() provider.Attributes {
	return provider.Attributes{
		"universe_id": cty.StringVal("42"),
		"title":       cty.StringVal("Double XP"),
		"subtitle":    cty.StringVal("All weekend"),
		"description": cty.StringVal("2x XP everywhere"),
		// An offset + fractional seconds: canonicalised to 2026-12-01T18:00:00Z.
		"start_time":          cty.StringVal("2026-12-01T10:00:00.250-08:00"),
		"end_time":            cty.StringVal("2026-12-03T18:00:00Z"),
		"visibility":          cty.StringVal("Private"),
		"categories":          cty.ListVal([]cty.Value{cty.StringVal("newcontent"), cty.StringVal("festival")}),
		"thumbnail_media_ids": cty.ListVal([]cty.Value{cty.StringVal("111"), cty.StringVal("222")}),
		"featuring_enabled":   cty.False,
	}
}

func TestExperienceEventCreateSendsCanonicalValues(t *testing.T) {
	f := &fakeEvents{}
	r := ExperienceEvent(f)
	id, attrs, err := r.Create(context.Background(), eventDesired())
	if err != nil {
		t.Fatal(err)
	}
	if id != "9007199254740993123" || f.createdIn != "42" {
		t.Fatalf("id=%q universe=%q", id, f.createdIn)
	}
	w := f.lastCreate
	if deref(w.StartTime) != "2026-12-01T18:00:00Z" || deref(w.EndTime) != "2026-12-03T18:00:00Z" {
		t.Fatalf("times sent = %q / %q", deref(w.StartTime), deref(w.EndTime))
	}
	if deref(w.Visibility) != "private" || deref(w.FeaturingStatus) != "disabled" {
		t.Fatalf("visibility/featuring sent = %q / %q", deref(w.Visibility), deref(w.FeaturingStatus))
	}
	if strings.Join(w.Categories, ",") != "newContent,festival" {
		t.Fatalf("categories sent = %v", w.Categories)
	}
	if w.Tagline != nil || w.PlaceID != nil || w.GroupID != nil {
		t.Fatal("undeclared attributes must not be sent")
	}
	if attrs["start_time"].AsString() != "2026-12-01T18:00:00Z" || attrs["universe_id"].AsString() != "42" {
		t.Fatalf("state attrs = %#v", attrs)
	}
}

func TestExperienceEventGroupIDCreateOnly(t *testing.T) {
	f := &fakeEvents{}
	r := ExperienceEvent(f)
	d := eventDesired()
	d["group_id"] = cty.StringVal("77")
	id, _, err := r.Create(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if deref(f.lastCreate.GroupID) != "77" {
		t.Fatalf("group id not sent on create: %+v", f.lastCreate)
	}
	if _, err := r.Update(context.Background(), id, d); err != nil {
		t.Fatal(err)
	}
	if f.lastUpdate.GroupID != nil {
		t.Fatal("group_id must not be sent on update")
	}
}

// TestExperienceEventConvergesThroughEngine drives plan.Build: after a create,
// a re-plan of the same config (with an offset-form start_time the API reports
// back as UTC with milliseconds) must be a no-op; then a title change is an
// update that converges too.
func TestExperienceEventConvergesThroughEngine(t *testing.T) {
	f := &fakeEvents{}
	p := singleProvider{"roblox_experience_event": ExperienceEvent(f)}
	ctx := context.Background()
	s := state.New()

	id, _, err := p["roblox_experience_event"].Create(ctx, eventDesired())
	if err != nil {
		t.Fatal(err)
	}
	s.Upsert(state.Resource{Type: "roblox_experience_event", Name: "xp", ID: id})

	desired := func(title string) []plan.DesiredResource {
		d := eventDesired()
		d["title"] = cty.StringVal(title)
		return []plan.DesiredResource{{Type: "roblox_experience_event", Name: "xp", Attributes: d}}
	}
	pl, err := plan.Build(ctx, p, s, desired("Double XP"))
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("expected convergence, got %+v", pl.Changes[0].AttrDiffs)
	}

	pl, err = plan.Build(ctx, p, s, desired("Triple XP"))
	if err != nil {
		t.Fatal(err)
	}
	if !pl.HasChanges() || len(pl.Changes[0].AttrDiffs) != 1 || pl.Changes[0].AttrDiffs[0].Name != "title" {
		t.Fatalf("expected a title-only update, got %+v", pl.Changes[0].AttrDiffs)
	}
	if _, err := p["roblox_experience_event"].Update(ctx, id, desired("Triple XP")[0].Attributes); err != nil {
		t.Fatal(err)
	}
	pl, err = plan.Build(ctx, p, s, desired("Triple XP"))
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("expected convergence after update, got %+v", pl.Changes[0].AttrDiffs)
	}
}

type singleProvider map[string]provider.Resource

func (s singleProvider) Resources() map[string]provider.Resource { return s }

func TestExperienceEventNormalizeRejectsBadValues(t *testing.T) {
	r := ExperienceEvent(nil).(provider.Normalizer)
	cases := map[string]func(provider.Attributes){
		"RFC 3339":         func(d provider.Attributes) { d["start_time"] = cty.StringVal("2026-12-01 18:00") },
		"must be after":    func(d provider.Attributes) { d["end_time"] = cty.StringVal("2026-12-01T18:00:00Z") },
		"visibility":       func(d provider.Attributes) { d["visibility"] = cty.StringVal("moderated") },
		"unknown category": func(d provider.Attributes) { d["categories"] = cty.ListVal([]cty.Value{cty.StringVal("party")}) },
	}
	for want, mutate := range cases {
		d := eventDesired()
		mutate(d)
		if _, err := r.Normalize(d); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
	}
}

func TestExperienceEventNormalizeLeavesUnknownAndInputAlone(t *testing.T) {
	r := ExperienceEvent(nil).(provider.Normalizer)
	d := eventDesired()
	d["start_time"] = cty.UnknownVal(cty.String)
	out, err := r.Normalize(d)
	if err != nil {
		t.Fatal(err)
	}
	if out["start_time"].IsKnown() {
		t.Fatal("unknown must pass through")
	}
	if d["visibility"].AsString() != "Private" {
		t.Fatal("Normalize must not mutate its input")
	}
}

func TestExperienceEventReadAndNotFound(t *testing.T) {
	f := &fakeEvents{store: map[string]opencloud.GameEvent{"5": {
		ID: "5", UniverseID: "42", PlaceID: "999", Title: "T", Subtitle: "S",
		StartTime: "2026-12-01T18:00:00.123+00:00", EndTime: "2026-12-01T19:00:00Z",
		Visibility: "public", FeaturingStatus: "enabled",
	}}}
	r := ExperienceEvent(f)
	got, err := r.Read(context.Background(), "5")
	if err != nil {
		t.Fatal(err)
	}
	if got["start_time"].AsString() != "2026-12-01T18:00:00Z" || got["place_id"].AsString() != "999" ||
		!got["featuring_enabled"].True() || got["universe_id"].AsString() != "42" {
		t.Fatalf("read = %#v", got)
	}
	if !got["categories"].RawEquals(cty.ListValEmpty(cty.String)) {
		t.Fatalf("empty categories must read as an empty list, got %#v", got["categories"])
	}
	if _, err := r.Read(context.Background(), "nope"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestExperienceEventDelete(t *testing.T) {
	f := &fakeEvents{store: map[string]opencloud.GameEvent{"5": {ID: "5"}}}
	r := ExperienceEvent(f)
	if err := r.Delete(context.Background(), "5"); err != nil || len(f.deleted) != 1 {
		t.Fatalf("delete err=%v deleted=%v", err, f.deleted)
	}
	// Already gone is success (idempotent destroy).
	f.deleteErr = &opencloud.APIError{Status: 404}
	if err := r.Delete(context.Background(), "5"); err != nil {
		t.Fatalf("404 on delete must be success, got %v", err)
	}
	f.deleteErr = &opencloud.APIError{Status: 403, Message: "Scope not authorized."}
	if err := r.Delete(context.Background(), "5"); err == nil {
		t.Fatal("non-404 delete errors must propagate")
	}
}
