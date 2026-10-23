package resources_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/config"
	"github.com/outpostgroupio/baseplate/internal/config/configgen"
	"github.com/outpostgroupio/baseplate/internal/engine/apply"
	"github.com/outpostgroupio/baseplate/internal/engine/plan"
	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/resources"
	"github.com/outpostgroupio/baseplate/internal/state"
	"github.com/outpostgroupio/baseplate/internal/state/backend"
)

// fakeAlerts is a stateful in-memory stand-in for the alerts API that mimics its
// documented semantics: create assigns a string id and starts in Syncing;
// PATCH merges only the fields present; the list returns values in a DIFFERENT
// order than they were written (to prove set-typed attributes don't drift).
type fakeAlerts struct {
	next    int
	alerts  map[string]*opencloud.Alert
	creates []opencloud.AlertWrite
	updates []opencloud.AlertWrite
	deletes []string
}

func newFakeAlerts() *fakeAlerts {
	return &fakeAlerts{next: 1000, alerts: map[string]*opencloud.Alert{}}
}

var errFake404 = &opencloud.APIError{Status: 404, Code: "ALERT_NOT_FOUND"}

func reversed(ss []string) []string {
	out := slices.Clone(ss)
	slices.Reverse(out)
	return out
}

func (f *fakeAlerts) snapshot(a *opencloud.Alert) opencloud.Alert {
	c := *a
	c.Filters = map[string][]string{}
	for k, v := range a.Filters {
		c.Filters[k] = reversed(v)
	}
	c.BreakdownDimensions = slices.Clone(a.BreakdownDimensions)
	c.WebhookConfigurationIDs = reversed(a.WebhookConfigurationIDs)
	return c
}

func (f *fakeAlerts) List(_ context.Context, _ string, ids ...string) ([]opencloud.Alert, error) {
	var out []opencloud.Alert
	for id, a := range f.alerts {
		if len(ids) == 0 || slices.Contains(ids, id) {
			out = append(out, f.snapshot(a))
		}
	}
	return out, nil
}

func (f *fakeAlerts) merge(a *opencloud.Alert, w opencloud.AlertWrite) {
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&a.Name, w.Name)
	set(&a.Metric, w.Metric)
	set(&a.Description, w.Description)
	set(&a.Severity, w.Severity)
	set(&a.Interval, w.Interval)
	set(&a.Operator, w.Operator)
	set(&a.EvaluationMode, w.EvaluationMode)
	if w.ConsecutiveOccurrences != nil {
		a.ConsecutiveOccurrences = *w.ConsecutiveOccurrences
	}
	if w.Threshold != nil {
		a.Threshold = *w.Threshold
	}
	if w.PeriodOffsetMultiplier != nil {
		v := *w.PeriodOffsetMultiplier
		a.PeriodOffsetMultiplier = &v
	}
	if a.EvaluationMode == "PeriodOverPeriod" && a.PeriodOffsetMultiplier == nil {
		one := int64(1) // documented server default
		a.PeriodOffsetMultiplier = &one
	}
	if w.Filters != nil {
		a.Filters = map[string][]string{}
		for k, v := range w.Filters {
			a.Filters[k] = slices.Clone(v)
		}
	}
	if w.BreakdownDimension != nil {
		a.BreakdownDimensions = []string{}
		if *w.BreakdownDimension != "" {
			a.BreakdownDimensions = []string{*w.BreakdownDimension}
		}
	}
	if w.WebhookConfigurationIDs != nil {
		a.WebhookConfigurationIDs = slices.Clone(w.WebhookConfigurationIDs)
	}
}

func (f *fakeAlerts) Create(_ context.Context, universeID string, w opencloud.AlertWrite) (opencloud.Alert, error) {
	f.creates = append(f.creates, w)
	f.next++
	a := &opencloud.Alert{
		ID: strconv.Itoa(f.next), UniverseID: universeID, ConfigState: "Syncing", FiringStatus: "OK",
		Filters: map[string][]string{}, BreakdownDimensions: []string{}, WebhookConfigurationIDs: []string{},
	}
	f.merge(a, w)
	f.alerts[a.ID] = a
	return f.snapshot(a), nil
}

func (f *fakeAlerts) Update(_ context.Context, _, alertID string, w opencloud.AlertWrite) (opencloud.Alert, error) {
	f.updates = append(f.updates, w)
	a, ok := f.alerts[alertID]
	if !ok {
		return opencloud.Alert{}, errFake404
	}
	f.merge(a, w)
	if w.Enabled != nil {
		a.ConfigState = map[bool]string{true: "Enabled", false: "Disabled"}[*w.Enabled]
	}
	return f.snapshot(a), nil
}

func (f *fakeAlerts) Delete(_ context.Context, _, alertID string) error {
	f.deletes = append(f.deletes, alertID)
	if _, ok := f.alerts[alertID]; !ok {
		return errFake404
	}
	delete(f.alerts, alertID)
	return nil
}

type alertProvider struct{ r provider.Resource }

func (p alertProvider) Resources() map[string]provider.Resource {
	return map[string]provider.Resource{"roblox_analytics_alert": p.r}
}

const alertHCLFull = `
resource "roblox_analytics_alert" "crash" {
  universe_id             = "100"
  name                    = "Mobile crash rate"
  description             = "Crash rate on phones/tablets"
  metric                  = "ClientCrashRate15m"
  severity                = "SEV_1"
  interval                = "OneMinute"
  consecutive_occurrences = 15
  operator                = "Gt"
  threshold               = 0.1
  evaluation_mode         = "Absolute"
  filters = {
    Platform = ["Phone", "Tablet"]
  }
  breakdown_dimension       = "Platform"
  webhook_configuration_ids = ["3f2b8c1e-5d4a-4f6b-9a7e-1c2d3e4f5a6b", "aaaaaaaa-5d4a-4f6b-9a7e-1c2d3e4f5a6b"]
  enabled                   = true
}
`

// loadAlert loads hclSrc through the real config loader (hcldec type conversion
// + unset-optional dropping), exactly as the CLI does.
func loadAlert(t *testing.T, p provider.Provider, hclSrc string) []plan.DesiredResource {
	t.Helper()
	path := filepath.Join(t.TempDir(), "a.bp.hcl")
	if err := os.WriteFile(path, []byte(hclSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, desired, _, err := config.Load(path, p)
	if err != nil {
		t.Fatal(err)
	}
	return desired
}

// cycle runs plan → apply → re-plan (via a fresh state load) and asserts the
// re-plan is a no-op.
func cycle(t *testing.T, p provider.Provider, b backend.Backend, s *state.State, hclSrc string) *plan.Plan {
	t.Helper()
	ctx := context.Background()
	desired := loadAlert(t, p, hclSrc)
	pl, err := plan.Build(ctx, p, s, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := apply.Apply(ctx, p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	reloaded, err := b.Get()
	if err != nil {
		t.Fatal(err)
	}
	pl2, err := plan.Build(ctx, p, reloaded, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl2.HasChanges() {
		t.Fatalf("expected convergence, got:\n%s", pl2.RenderText(false))
	}
	return pl
}

func TestAnalyticsAlertConvergesThroughConfigStateAndReplan(t *testing.T) {
	f := newFakeAlerts()
	p := alertProvider{resources.AnalyticsAlert(f)}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	s := state.New()

	pl := cycle(t, p, b, s, alertHCLFull)
	if pl.Changes[0].Action != provider.ActionCreate {
		t.Fatalf("first plan = %v", pl.Changes[0].Action)
	}
	// Rendering of the non-primitive attrs must be readable, not a Go dump.
	txt := pl.RenderText(false)
	if !strings.Contains(txt, `filters = {"Platform":["Phone","Tablet"]}`) || strings.Contains(txt, "cty.") {
		t.Fatalf("render:\n%s", txt)
	}
	if len(f.creates) != 1 || len(f.updates) != 0 {
		t.Fatalf("creates=%d updates=%d (enabled=true must not PATCH)", len(f.creates), len(f.updates))
	}

	// Change threshold + filters + webhooks; clear description and breakdown.
	edited := strings.NewReplacer(
		`threshold               = 0.1`, `threshold               = 2.5`,
		`Platform = ["Phone", "Tablet"]`, `Platform = ["Phone"]`+"\n    Country = [\"US\", \"GB\"]",
		`description             = "Crash rate on phones/tablets"`, `description             = ""`,
		`breakdown_dimension       = "Platform"`, `breakdown_dimension       = ""`,
		`, "aaaaaaaa-5d4a-4f6b-9a7e-1c2d3e4f5a6b"]`, `]`,
	).Replace(alertHCLFull)
	pl = cycle(t, p, b, s, edited)
	if pl.Changes[0].Action != provider.ActionUpdate {
		t.Fatalf("edit plan = %v", pl.Changes[0].Action)
	}
	var changed []string
	for _, d := range pl.Changes[0].AttrDiffs {
		changed = append(changed, d.Name)
	}
	want := []string{"breakdown_dimension", "description", "filters", "threshold", "webhook_configuration_ids"}
	if !slices.Equal(changed, want) {
		t.Fatalf("diffed attrs = %v, want %v", changed, want)
	}
	for _, a := range f.alerts {
		if a.Threshold != 2.5 || len(a.BreakdownDimensions) != 0 || a.Description != "" || len(a.WebhookConfigurationIDs) != 1 {
			t.Fatalf("remote after update = %+v", a)
		}
	}

	// Disable.
	cycle(t, p, b, s, strings.Replace(edited, "enabled                   = true", "enabled                   = false", 1))
	for _, a := range f.alerts {
		if a.ConfigState != "Disabled" {
			t.Fatalf("configState = %q", a.ConfigState)
		}
	}

	// Destroy: real delete.
	pl, err := plan.Build(context.Background(), p, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := apply.Apply(context.Background(), p, b, s, pl); err != nil {
		t.Fatal(err)
	}
	if len(f.alerts) != 0 || len(f.deletes) != 1 {
		t.Fatalf("alerts=%v deletes=%v", f.alerts, f.deletes)
	}
}

const alertHCLMinimal = `
resource "roblox_analytics_alert" "pcu" {
  universe_id             = "100"
  name                    = "PCU week-over-week drop"
  metric                  = "PeakConcurrentPlayers"
  severity                = "SEV_1"
  interval                = "OneDay"
  consecutive_occurrences = 1
  operator                = "Lt"
  threshold               = -30
  evaluation_mode         = "PeriodOverPeriod"
  enabled                 = false
}
`

// Unset optional attrs (filters, webhooks, period_offset_multiplier — which the
// server defaults to 1) must not diff; create with enabled=false PATCHes after.
func TestAnalyticsAlertMinimalPeriodOverPeriodDisabled(t *testing.T) {
	f := newFakeAlerts()
	p := alertProvider{resources.AnalyticsAlert(f)}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	cycle(t, p, b, state.New(), alertHCLMinimal)

	if len(f.updates) != 1 || f.updates[0].Enabled == nil || *f.updates[0].Enabled {
		t.Fatalf("create with enabled=false must follow up with a disable PATCH; updates=%+v", f.updates)
	}
	c := f.creates[0]
	if c.Filters != nil || c.WebhookConfigurationIDs != nil || c.BreakdownDimension != nil || c.Description != nil || c.PeriodOffsetMultiplier != nil {
		t.Fatalf("unset optionals must be omitted from create: %+v", c)
	}
}

func TestAnalyticsAlertCreateDefaultsEvaluationModeAbsolute(t *testing.T) {
	f := newFakeAlerts()
	p := alertProvider{resources.AnalyticsAlert(f)}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	src := strings.Replace(alertHCLFull, `  evaluation_mode         = "Absolute"`+"\n", "", 1)
	cycle(t, p, b, state.New(), src)
	if m := f.creates[0].EvaluationMode; m == nil || *m != "Absolute" {
		t.Fatalf("evaluationMode = %v", m)
	}
}

func TestAnalyticsAlertReadNotFoundAndDeleteIdempotent(t *testing.T) {
	r := resources.AnalyticsAlert(newFakeAlerts())
	if _, err := r.Read(context.Background(), "100/999"); !errors.Is(err, provider.ErrNotFound) {
		t.Fatalf("missing alert: err = %v, want ErrNotFound", err)
	}
	if err := r.Delete(context.Background(), "100/999"); err != nil {
		t.Fatalf("delete of an already-gone alert must succeed, got %v", err)
	}
	for _, bad := range []string{"100", "/1", "100/", "100/1/2"} {
		if _, err := r.Read(context.Background(), bad); err == nil {
			t.Fatalf("id %q must be rejected", bad)
		}
	}
}

func TestAnalyticsAlertRejectsFractionalOccurrences(t *testing.T) {
	r := resources.AnalyticsAlert(newFakeAlerts())
	_, _, err := r.Create(context.Background(), provider.Attributes{
		"universe_id": cty.StringVal("100"), "name": cty.StringVal("n"), "metric": cty.StringVal("m"),
		"severity": cty.StringVal("SEV_2"), "interval": cty.StringVal("OneDay"),
		"consecutive_occurrences": cty.NumberFloatVal(1.5),
		"operator":                cty.StringVal("Gt"), "threshold": cty.NumberIntVal(1),
	})
	if err == nil || !strings.Contains(err.Error(), "whole number") {
		t.Fatalf("err = %v", err)
	}
}

// Import-by-id + config generation: the generated HCL must re-load and converge.
func TestAnalyticsAlertImportGenerateConfigRoundTrips(t *testing.T) {
	f := newFakeAlerts()
	r := resources.AnalyticsAlert(f)
	p := alertProvider{r}
	b := backend.NewLocal(filepath.Join(t.TempDir(), "s.bpstate"))
	s := state.New()
	cycle(t, p, b, s, alertHCLFull)
	id := s.Resources[0].ID

	actual, err := r.Read(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	blk, err := configgen.Generate(r, "imported", id, actual)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "gen.bp.hcl")
	if err := configgen.WriteFile(out, []*hclwrite.Block{blk}); err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(out)

	s2 := state.New()
	desired := loadAlert(t, p, string(src))
	if err := apply.Import(context.Background(), p, b, s2, "roblox_analytics_alert.imported", id, desired); err != nil {
		t.Fatal(err)
	}
	pl, err := plan.Build(context.Background(), p, s2, desired)
	if err != nil {
		t.Fatal(err)
	}
	if pl.HasChanges() {
		t.Fatalf("generated config does not converge:\n%s\n%s", src, pl.RenderText(false))
	}
}
