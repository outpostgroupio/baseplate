package opencloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// Alert is the domain view of an analytics alert configuration
// (analytics-alert-control-plane/v1, EXPERIMENTAL). The nested wire shapes
// (condition, filter[], breakdown[], webhookReceiverConfig.receivers[]) are
// flattened here so the resource layer deals only in flat fields.
type Alert struct {
	ID                     string // string-encoded int64
	UniverseID             string
	Name                   string
	Metric                 string
	Description            string // "" when the API returns null
	Severity               string // SEV_0 | SEV_1 | SEV_2
	Interval               string // OneMinute | HalfHour | OneHour | OneDay
	ConsecutiveOccurrences int64
	Operator               string // Gt | Gte | Lt | Lte
	Threshold              float64
	EvaluationMode         string // Absolute | PeriodOverPeriod
	PeriodOffsetMultiplier *int64 // nil for Absolute conditions
	// Filters maps a dimension name to its allowed values. Never nil after decode.
	Filters map[string][]string
	// BreakdownDimensions is every dimension across every breakdown entry, in
	// order. The API currently allows at most one entry with one dimension.
	BreakdownDimensions     []string
	WebhookConfigurationIDs []string // never nil after decode
	ConfigState             string   // Enabled | Disabled | PausedByRoblox | Syncing | Error
	FiringStatus            string   // OK | Firing
}

// AlertWrite is the input to Create/Update. Every field is optional so the same
// type serves the PATCH endpoint's partial-update semantics: a nil pointer / nil
// map / nil slice is OMITTED from the request (server keeps its current value),
// while a non-nil empty map / slice / "" breakdown is sent explicitly to CLEAR it.
type AlertWrite struct {
	Name                    *string
	Metric                  *string
	Description             *string
	Severity                *string
	Interval                *string
	ConsecutiveOccurrences  *int64
	Operator                *string
	Threshold               *float64
	EvaluationMode          *string
	PeriodOffsetMultiplier  *int64
	Filters                 map[string][]string
	BreakdownDimension      *string  // "" clears the breakdown
	WebhookConfigurationIDs []string // empty (non-nil) removes all webhooks
	// Enabled maps to configState Enabled/Disabled. PATCH-only: the create
	// request has no configState field, so Create ignores it.
	Enabled *bool
}

// AnalyticsAlertClient manages a universe's analytics alerts. There is no
// single-alert GET, so callers read via List(ids...) and find by id.
type AnalyticsAlertClient interface {
	List(ctx context.Context, universeID string, ids ...string) ([]Alert, error)
	Create(ctx context.Context, universeID string, w AlertWrite) (Alert, error)
	Update(ctx context.Context, universeID, alertID string, w AlertWrite) (Alert, error)
	Delete(ctx context.Context, universeID, alertID string) error
}

func NewAnalyticsAlertClient(c *Client) AnalyticsAlertClient { return &alertClient{c: c} }

type alertClient struct{ c *Client }

func alertsBase(universeID string) string {
	return "/analytics-alert-control-plane/v1/resource/Universe/id/" + url.PathEscape(universeID) + "/alerts"
}

// --- wire types ---

// idString decodes a JSON string OR number into a string. The spec and docs say
// alert ids are string-encoded int64s; accepting a bare number too is a cheap
// guard in case the wire disagrees (cf. assetId/gamePassId surprises).
type idString string

func (s *idString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*s = idString(v)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*s = idString(n.String())
	return nil
}

type alertConditionWire struct {
	Operator               *string  `json:"operator,omitempty"`
	Threshold              *float64 `json:"threshold,omitempty"`
	EvaluationMode         *string  `json:"evaluationMode,omitempty"`
	PeriodOffsetMultiplier *int64   `json:"periodOffsetMultiplier,omitempty"`
}

type alertFilterWire struct {
	Dimension string   `json:"dimension"`
	Values    []string `json:"values"`
}

type alertBreakdownWire struct {
	Dimensions []string `json:"dimensions"`
}

type webhookReceiverWire struct {
	WebhookConfigurationID string `json:"webhookConfigurationId"`
}

type webhookReceiverConfigWire struct {
	Receivers []webhookReceiverWire `json:"receivers"`
}

// alertRequestWire is the create/PATCH body. Pointer fields + omitempty mean a
// nil field is omitted, while a non-nil pointer to an empty slice is sent as [].
type alertRequestWire struct {
	Name                   *string                    `json:"name,omitempty"`
	Metric                 *string                    `json:"metric,omitempty"`
	Description            *string                    `json:"description,omitempty"`
	Severity               *string                    `json:"severity,omitempty"`
	Interval               *string                    `json:"interval,omitempty"`
	ConsecutiveOccurrences *int64                     `json:"consecutiveOccurrences,omitempty"`
	Condition              *alertConditionWire        `json:"condition,omitempty"`
	Filter                 *[]alertFilterWire         `json:"filter,omitempty"`
	Breakdown              *[]alertBreakdownWire      `json:"breakdown,omitempty"`
	WebhookReceiverConfig  *webhookReceiverConfigWire `json:"webhookReceiverConfig,omitempty"`
	ConfigState            *string                    `json:"configState,omitempty"`
}

type alertResponseWire struct {
	AlertID                idString             `json:"alertId"`
	ResourceID             idString             `json:"resourceId"`
	Name                   string               `json:"name"`
	Metric                 string               `json:"metric"`
	Description            *string              `json:"description"`
	Severity               string               `json:"severity"`
	Interval               string               `json:"interval"`
	ConsecutiveOccurrences int64                `json:"consecutiveOccurrences"`
	Filter                 []alertFilterWire    `json:"filter"`
	Breakdown              []alertBreakdownWire `json:"breakdown"`
	Condition              struct {
		Operator               string  `json:"operator"`
		Threshold              float64 `json:"threshold"`
		EvaluationMode         string  `json:"evaluationMode"`
		PeriodOffsetMultiplier *int64  `json:"periodOffsetMultiplier"`
	} `json:"condition"`
	ConfigState           string                     `json:"configState"`
	WebhookReceiverConfig *webhookReceiverConfigWire `json:"webhookReceiverConfig"`
	FiringStatus          string                     `json:"firingStatus"`
}

func (w alertResponseWire) toDomain() Alert {
	a := Alert{
		ID:                      string(w.AlertID),
		UniverseID:              string(w.ResourceID),
		Name:                    w.Name,
		Metric:                  w.Metric,
		Severity:                w.Severity,
		Interval:                w.Interval,
		ConsecutiveOccurrences:  w.ConsecutiveOccurrences,
		Operator:                w.Condition.Operator,
		Threshold:               w.Condition.Threshold,
		EvaluationMode:          w.Condition.EvaluationMode,
		PeriodOffsetMultiplier:  w.Condition.PeriodOffsetMultiplier,
		Filters:                 map[string][]string{},
		BreakdownDimensions:     []string{},
		WebhookConfigurationIDs: []string{},
		ConfigState:             w.ConfigState,
		FiringStatus:            w.FiringStatus,
	}
	if w.Description != nil {
		a.Description = *w.Description
	}
	for _, f := range w.Filter {
		a.Filters[f.Dimension] = append(a.Filters[f.Dimension], f.Values...)
	}
	for _, b := range w.Breakdown {
		a.BreakdownDimensions = append(a.BreakdownDimensions, b.Dimensions...)
	}
	if w.WebhookReceiverConfig != nil {
		for _, r := range w.WebhookReceiverConfig.Receivers {
			a.WebhookConfigurationIDs = append(a.WebhookConfigurationIDs, r.WebhookConfigurationID)
		}
	}
	return a
}

func (w AlertWrite) toWire(includeConfigState bool) alertRequestWire {
	r := alertRequestWire{
		Name:                   w.Name,
		Metric:                 w.Metric,
		Description:            w.Description,
		Severity:               w.Severity,
		Interval:               w.Interval,
		ConsecutiveOccurrences: w.ConsecutiveOccurrences,
	}
	if w.Operator != nil || w.Threshold != nil || w.EvaluationMode != nil || w.PeriodOffsetMultiplier != nil {
		r.Condition = &alertConditionWire{
			Operator:               w.Operator,
			Threshold:              w.Threshold,
			EvaluationMode:         w.EvaluationMode,
			PeriodOffsetMultiplier: w.PeriodOffsetMultiplier,
		}
	}
	if w.Filters != nil {
		dims := make([]string, 0, len(w.Filters))
		for d := range w.Filters {
			dims = append(dims, d)
		}
		sort.Strings(dims) // deterministic request bodies
		fs := make([]alertFilterWire, 0, len(dims))
		for _, d := range dims {
			vals := w.Filters[d]
			if vals == nil {
				vals = []string{}
			}
			fs = append(fs, alertFilterWire{Dimension: d, Values: vals})
		}
		r.Filter = &fs
	}
	if w.BreakdownDimension != nil {
		bs := []alertBreakdownWire{}
		if *w.BreakdownDimension != "" {
			bs = append(bs, alertBreakdownWire{Dimensions: []string{*w.BreakdownDimension}})
		}
		r.Breakdown = &bs
	}
	if w.WebhookConfigurationIDs != nil {
		rs := make([]webhookReceiverWire, 0, len(w.WebhookConfigurationIDs))
		for _, id := range w.WebhookConfigurationIDs {
			rs = append(rs, webhookReceiverWire{WebhookConfigurationID: id})
		}
		r.WebhookReceiverConfig = &webhookReceiverConfigWire{Receivers: rs}
	}
	if includeConfigState && w.Enabled != nil {
		s := "Disabled"
		if *w.Enabled {
			s = "Enabled"
		}
		r.ConfigState = &s
	}
	return r
}

// --- operations ---

// List returns the universe's alerts. The endpoint returns a bare JSON array (no
// pagination per the spec; a universe is capped at 20 alerts). ids, when given,
// are sent as repeated ?ids= query params to narrow the result server-side.
func (ac *alertClient) List(ctx context.Context, universeID string, ids ...string) ([]Alert, error) {
	path := alertsBase(universeID)
	if len(ids) > 0 {
		q := url.Values{}
		for _, id := range ids {
			q.Add("ids", id)
		}
		path += "?" + q.Encode()
	}
	var ws []alertResponseWire
	if _, err := ac.c.Do(ctx, http.MethodGet, path, nil, &ws); err != nil {
		err = translateAlertErr(universeID, err)
		return nil, err
	}
	out := make([]Alert, 0, len(ws))
	for _, w := range ws {
		out = append(out, w.toDomain())
	}
	return out, nil
}

func (ac *alertClient) Create(ctx context.Context, universeID string, w AlertWrite) (Alert, error) {
	var got alertResponseWire
	if _, err := ac.c.Do(ctx, http.MethodPost, alertsBase(universeID), w.toWire(false), &got); err != nil {
		err = translateAlertErr(universeID, err)
		return Alert{}, err
	}
	return got.toDomain(), nil
}

func (ac *alertClient) Update(ctx context.Context, universeID, alertID string, w AlertWrite) (Alert, error) {
	var got alertResponseWire
	path := alertsBase(universeID) + "/" + url.PathEscape(alertID)
	if _, err := ac.c.Do(ctx, http.MethodPatch, path, w.toWire(true), &got); err != nil {
		err = translateAlertErr(universeID, err)
		return Alert{}, err
	}
	return got.toDomain(), nil
}

func (ac *alertClient) Delete(ctx context.Context, universeID, alertID string) error {
	_, err := ac.c.Do(ctx, http.MethodDelete, alertsBase(universeID)+"/"+url.PathEscape(alertID), nil, nil)
	return translateAlertErr(universeID, err)
}

// AlertsNotEligibleError reports that a universe can't use analytics alerts yet.
// Roblox answers every alerts call for such a universe with
// 403 {"errorCode":"PERMISSION_DENIED","message":"Universe N is not enrolled in
// PerformanceMonitoringV2."} (live-observed 2026-10-23). "PerformanceMonitoringV2"
// isn't documented anywhere; the documented rule is that alerts are available to
// any experience meeting the Performance dashboard's eligibility requirement of
// 100+ daily active users, with no opt-in setting. Unwrap returns the APIError.
type AlertsNotEligibleError struct {
	UniverseID string
	Err        *APIError
}

func (e *AlertsNotEligibleError) Error() string {
	return fmt.Sprintf("universe %s is not eligible for analytics alerts yet (Roblox: %q). "+
		"Roblox makes alerts available to experiences that meet the Performance dashboard "+
		"eligibility requirement of 100+ daily active users; there is no documented opt-in "+
		"setting. Once the experience qualifies, re-run. "+
		"See https://create.roblox.com/docs/production/analytics/alerts",
		e.UniverseID, e.Err.Message)
}

func (e *AlertsNotEligibleError) Unwrap() error { return e.Err }

// IsAlertsNotEligible reports whether err is, or wraps, an AlertsNotEligibleError.
func IsAlertsNotEligible(err error) bool {
	var e *AlertsNotEligibleError
	return errors.As(err, &e)
}

// translateAlertErr turns the "not enrolled" 403 into an AlertsNotEligibleError
// and passes every other error (including other 403s, such as missing scopes or
// missing per-user permissions) through unchanged.
func translateAlertErr(universeID string, err error) error {
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusForbidden || ae.Code != "PERMISSION_DENIED" {
		return err
	}
	msg := strings.ToLower(ae.Message)
	if strings.Contains(msg, "enrolled") || strings.Contains(msg, "performancemonitoring") {
		return &AlertsNotEligibleError{UniverseID: universeID, Err: ae}
	}
	return err
}
