package resources

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"

	"github.com/outpostgroupio/baseplate/internal/provider"
	"github.com/outpostgroupio/baseplate/internal/provider/roblox/opencloud"
)

// filtersType is the HCL/cty type of the `filters` attribute: dimension name →
// set of allowed values. A set (not a list) so value order on the wire can never
// cause spurious drift; a map (not a list of objects) because the API allows each
// dimension at most once.
var filtersType = cty.Map(cty.Set(cty.String))

type analyticsAlert struct {
	ac opencloud.AnalyticsAlertClient
}

// AnalyticsAlert returns the roblox_analytics_alert resource. Its state id is
// "<universeID>/<alertID>". The API's nested condition / filter / breakdown /
// webhook objects are flattened into top-level attributes so every attribute
// read back from the API converges against config with cty.RawEquals. Destroy is
// a real hard delete.
func AnalyticsAlert(ac opencloud.AnalyticsAlertClient) provider.Resource {
	return &analyticsAlert{ac: ac}
}

func (a *analyticsAlert) Type() string { return "roblox_analytics_alert" }

func (a *analyticsAlert) Doc() provider.ResourceDoc {
	return provider.ResourceDoc{
		Summary: "Manages an analytics alert on an experience metric (production alerting, e.g. crash rate or concurrent players). EXPERIMENTAL Roblox API. Full create/update/real-delete lifecycle.",
		Attributes: map[string]string{
			"universe_id":               "The universe. Often a reference to an experience.",
			"name":                      "Alert name. Must be unique within the experience; at most 50 characters; subject to Roblox text moderation.",
			"metric":                    "Metric to evaluate, e.g. `ClientCrashRate15m`, `PeakConcurrentPlayers` (case-sensitive). See Roblox's alerts \"Supported metrics\" guide.",
			"description":               "Free-text description (at most 200 characters; moderated). Set `\"\"` to clear.",
			"severity":                  "`SEV_0` (critical), `SEV_1` (medium) or `SEV_2` (low).",
			"interval":                  "Evaluation frequency: `OneMinute`, `HalfHour`, `OneHour` or `OneDay`. Not every metric supports every interval.",
			"consecutive_occurrences":   "Consecutive breaching evaluations before the alert fires. `OneMinute` allows 6-30; the other intervals 1-10.",
			"operator":                  "Comparison operator: `Gt`, `Gte`, `Lt` or `Lte`.",
			"threshold":                 "Value the metric is compared against. For `PeriodOverPeriod` it is a percentage change (e.g. `-30` = a 30% drop).",
			"evaluation_mode":           "`Absolute` (default) compares the raw value; `PeriodOverPeriod` compares the percentage change from an earlier period.",
			"period_offset_multiplier":  "`PeriodOverPeriod` only: how many intervals back the comparison period is (Roblox defaults to 1). Must be omitted for `Absolute`.",
			"filters":                   "Dimension filters: a map of dimension name to the allowed values, e.g. `{ Platform = [\"Phone\", \"Tablet\"] }`. Set `{}` to clear.",
			"breakdown_dimension":       "Evaluate the metric separately per value of this dimension (which must also be a key of `filters`). Set `\"\"` to clear.",
			"webhook_configuration_ids": "UUIDs of webhooks to notify when the alert fires or resolves. Webhooks can only be created in the Creator Dashboard (Webhooks); Open Cloud has no API for them. Set `[]` to remove all.",
			"enabled":                   "Whether the alert is enabled (default `true`). `false` pauses it without deleting it.",
		},
		Notes: "Built on Roblox's EXPERIMENTAL analytics-alerts API (`analytics-alert-control-plane/v1`), which may change without notice. " +
			"Alerts are only available to experiences that meet the Performance dashboard eligibility requirement (100+ daily active users). Roblox documents no opt-in setting. Other experiences get `403 … not enrolled in PerformanceMonitoringV2` on every call, which Baseplate reports as \"not eligible for analytics alerts\". " +
			"A universe can have at most 20 alerts.New alerts report `config_state = \"Syncing\"` briefly before `Enabled`. " +
			"Removing an optional attribute from config stops managing it but leaves the remote value in place; to clear one, set it to its empty value (`\"\"`, `{}`, `[]`). " +
			"`universe_id` is create-only. Import with id `<universe_id>/<alert_id>`.",
		Scopes:      []string{"universe.analytics.alert:read", "universe.analytics.alert:write"},
		ExampleFile: "examples/analytics-alert.bp.hcl",
	}
}

func (a *analyticsAlert) DecodeSpec() hcldec.Spec {
	attr := func(name string, t cty.Type, required bool) *hcldec.AttrSpec {
		return &hcldec.AttrSpec{Name: name, Type: t, Required: required}
	}
	return hcldec.ObjectSpec{
		"universe_id":               attr("universe_id", cty.String, true),
		"name":                      attr("name", cty.String, true),
		"metric":                    attr("metric", cty.String, true),
		"description":               attr("description", cty.String, false),
		"severity":                  attr("severity", cty.String, true),
		"interval":                  attr("interval", cty.String, true),
		"consecutive_occurrences":   attr("consecutive_occurrences", cty.Number, true),
		"operator":                  attr("operator", cty.String, true),
		"threshold":                 attr("threshold", cty.Number, true),
		"evaluation_mode":           attr("evaluation_mode", cty.String, false),
		"period_offset_multiplier":  attr("period_offset_multiplier", cty.Number, false),
		"filters":                   attr("filters", filtersType, false),
		"breakdown_dimension":       attr("breakdown_dimension", cty.String, false),
		"webhook_configuration_ids": attr("webhook_configuration_ids", cty.Set(cty.String), false),
		"enabled":                   attr("enabled", cty.Bool, false),
	}
}

// Computed excludes from diff: the state id, universe_id (create-only identity,
// often a resolved reference) and the provider outputs alert_id / config_state.
func (a *analyticsAlert) Computed() []string {
	return []string{"id", "universe_id", "alert_id", "config_state"}
}

func (a *analyticsAlert) Dependencies(provider.Attributes) []provider.Ref { return nil }

func (a *analyticsAlert) Create(ctx context.Context, desired provider.Attributes) (string, provider.Attributes, error) {
	universeID := desired["universe_id"].AsString()
	w, err := alertWrite(desired)
	if err != nil {
		return "", nil, err
	}
	if w.EvaluationMode == nil {
		absolute := "Absolute" // required by the create endpoint
		w.EvaluationMode = &absolute
	}
	got, err := a.ac.Create(ctx, universeID, w)
	if err != nil {
		return "", nil, err
	}
	id := universeID + "/" + got.ID
	// The create body has no configState; disabling is a follow-up PATCH.
	if w.Enabled != nil && !*w.Enabled {
		got, err = a.ac.Update(ctx, universeID, got.ID, opencloud.AlertWrite{Enabled: w.Enabled})
		if err != nil {
			return "", nil, fmt.Errorf("roblox_analytics_alert: created alert %s but failed to disable it: %w", id, err)
		}
	}
	return id, alertStateAttrs(universeID, got), nil
}

func (a *analyticsAlert) Read(ctx context.Context, id string) (provider.Attributes, error) {
	universeID, alertID, ok := splitAlertID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_analytics_alert: invalid id %q (want <universe_id>/<alert_id>)", id)
	}
	// No single-alert GET: list with an ids= filter and find by id.
	list, err := a.ac.List(ctx, universeID, alertID)
	if err != nil {
		if opencloud.IsNotFound(err) {
			return nil, provider.ErrNotFound
		}
		return nil, err
	}
	for _, al := range list {
		if al.ID == alertID {
			return alertStateAttrs(universeID, al), nil
		}
	}
	return nil, provider.ErrNotFound
}

func (a *analyticsAlert) Update(ctx context.Context, id string, desired provider.Attributes) (provider.Attributes, error) {
	universeID, alertID, ok := splitAlertID(id)
	if !ok {
		return nil, fmt.Errorf("roblox_analytics_alert: invalid id %q (want <universe_id>/<alert_id>)", id)
	}
	w, err := alertWrite(desired)
	if err != nil {
		return nil, err
	}
	got, err := a.ac.Update(ctx, universeID, alertID, w)
	if err != nil {
		return nil, err
	}
	return alertStateAttrs(universeID, got), nil
}

// Delete is a real, irreversible delete. An alert that is already gone counts as
// deleted.
func (a *analyticsAlert) Delete(ctx context.Context, id string) error {
	universeID, alertID, ok := splitAlertID(id)
	if !ok {
		return fmt.Errorf("roblox_analytics_alert: invalid id %q (want <universe_id>/<alert_id>)", id)
	}
	if err := a.ac.Delete(ctx, universeID, alertID); err != nil && !opencloud.IsNotFound(err) {
		return err
	}
	return nil
}

// alertWrite maps declared config to the client input. Only attributes present
// (and non-null) in desired are set, so PATCH leaves everything else untouched.
func alertWrite(desired provider.Attributes) (opencloud.AlertWrite, error) {
	var w opencloud.AlertWrite
	var errs []error
	str := func(k string) *string {
		if v, ok := desired[k]; ok && !v.IsNull() {
			s := v.AsString()
			return &s
		}
		return nil
	}
	integer := func(k string) *int64 {
		v, ok := desired[k]
		if !ok || v.IsNull() {
			return nil
		}
		i, acc := v.AsBigFloat().Int64()
		if !v.AsBigFloat().IsInt() || acc != 0 {
			errs = append(errs, fmt.Errorf("roblox_analytics_alert: %s must be a whole number, got %s", k, v.AsBigFloat().Text('f', -1)))
			return nil
		}
		return &i
	}
	w.Name = str("name")
	w.Metric = str("metric")
	w.Description = str("description")
	w.Severity = str("severity")
	w.Interval = str("interval")
	w.ConsecutiveOccurrences = integer("consecutive_occurrences")
	w.Operator = str("operator")
	if v, ok := desired["threshold"]; ok && !v.IsNull() {
		f, _ := v.AsBigFloat().Float64()
		w.Threshold = &f
	}
	w.EvaluationMode = str("evaluation_mode")
	w.PeriodOffsetMultiplier = integer("period_offset_multiplier")
	if v, ok := desired["filters"]; ok && !v.IsNull() {
		w.Filters = map[string][]string{}
		for dim, vals := range v.AsValueMap() {
			out := []string{}
			if !vals.IsNull() {
				for _, s := range vals.AsValueSlice() {
					out = append(out, s.AsString())
				}
			}
			w.Filters[dim] = out
		}
	}
	w.BreakdownDimension = str("breakdown_dimension")
	if v, ok := desired["webhook_configuration_ids"]; ok && !v.IsNull() {
		w.WebhookConfigurationIDs = []string{}
		for _, s := range v.AsValueSlice() {
			w.WebhookConfigurationIDs = append(w.WebhookConfigurationIDs, s.AsString())
		}
	}
	if v, ok := desired["enabled"]; ok && !v.IsNull() {
		b := v.True()
		w.Enabled = &b
	}
	return w, errors.Join(errs...)
}

// alertStateAttrs builds the read-back attributes. Optional attributes are
// always present (with their empty value when unset remotely) so a config that
// explicitly declares the empty value (`""`, `{}`, `[]`) converges, and every
// value carries exactly the type DecodeSpec gives the config side.
func alertStateAttrs(universeID string, al opencloud.Alert) provider.Attributes {
	attrs := provider.Attributes{
		"universe_id":               cty.StringVal(universeID),
		"alert_id":                  cty.StringVal(al.ID),
		"name":                      cty.StringVal(al.Name),
		"metric":                    cty.StringVal(al.Metric),
		"description":               cty.StringVal(al.Description),
		"severity":                  cty.StringVal(al.Severity),
		"interval":                  cty.StringVal(al.Interval),
		"consecutive_occurrences":   cty.NumberIntVal(al.ConsecutiveOccurrences),
		"operator":                  cty.StringVal(al.Operator),
		"threshold":                 thresholdVal(al.Threshold),
		"evaluation_mode":           cty.StringVal(al.EvaluationMode),
		"filters":                   filtersVal(al.Filters),
		"breakdown_dimension":       cty.StringVal(strings.Join(al.BreakdownDimensions, ",")),
		"webhook_configuration_ids": stringSetVal(al.WebhookConfigurationIDs),
		"enabled":                   cty.BoolVal(al.ConfigState != "Disabled"),
		"config_state":              cty.StringVal(al.ConfigState),
	}
	if al.PeriodOffsetMultiplier != nil {
		attrs["period_offset_multiplier"] = cty.NumberIntVal(*al.PeriodOffsetMultiplier)
	}
	return attrs
}

// thresholdVal converts the wire float64 via its shortest decimal text, so the
// value has the same arbitrary-precision form HCL gives the config literal.
func thresholdVal(f float64) cty.Value {
	if v, err := cty.ParseNumberVal(strconv.FormatFloat(f, 'g', -1, 64)); err == nil {
		return v
	}
	return cty.NumberFloatVal(f)
}

func filtersVal(m map[string][]string) cty.Value {
	if len(m) == 0 {
		return cty.MapValEmpty(cty.Set(cty.String))
	}
	dims := make([]string, 0, len(m))
	for d := range m {
		dims = append(dims, d)
	}
	sort.Strings(dims)
	out := make(map[string]cty.Value, len(m))
	for _, d := range dims {
		out[d] = stringSetVal(m[d])
	}
	return cty.MapVal(out)
}

func stringSetVal(ss []string) cty.Value {
	if len(ss) == 0 {
		return cty.SetValEmpty(cty.String)
	}
	vals := make([]cty.Value, 0, len(ss))
	for _, s := range ss {
		vals = append(vals, cty.StringVal(s))
	}
	return cty.SetVal(vals)
}

// splitAlertID splits "<universeID>/<alertID>".
func splitAlertID(id string) (string, string, bool) {
	i := strings.IndexByte(id, '/')
	if i <= 0 || i == len(id)-1 || strings.Contains(id[i+1:], "/") {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}
