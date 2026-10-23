package opencloud

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

const alertsPath = "/analytics-alert-control-plane/v1/resource/Universe/id/100/alerts"

// alertJSON is a response body shaped like the documented create/list response.
const alertJSON = `{
  "alertId": "1234567890123",
  "resourceType": "Universe",
  "resourceId": "100",
  "name": "High crash rate",
  "metric": "ClientCrashRate15m",
  "description": null,
  "severity": "SEV_0",
  "interval": "OneMinute",
  "consecutiveOccurrences": 10,
  "filter": [{"dimension": "Platform", "values": ["Phone", "Tablet"]}],
  "breakdown": [{"dimensions": ["Platform"]}],
  "condition": {"operator": "Gt", "threshold": 2.5, "evaluationMode": "Absolute"},
  "configState": "Syncing",
  "webhookReceiverConfig": null,
  "firingStatus": "OK",
  "lastFiredAt": null,
  "createdAt": "2026-10-01T18:00:00Z",
  "lastModifiedAt": "2026-10-01T18:00:00Z",
  "lastModifiedBy": "987654321"
}`

type capturedReq struct {
	method, path, rawQuery string
	body                   []byte
}

func newAlertTestServer(t *testing.T, status int, resp string) (*httptest.Server, *capturedReq) {
	t.Helper()
	var got capturedReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.rawQuery = r.Method, r.URL.Path, r.URL.RawQuery
		got.body, _ = io.ReadAll(r.Body)
		if r.Header.Get("x-api-key") != "k" {
			t.Errorf("missing api key header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func alertTestClient(url string) AnalyticsAlertClient {
	return NewAnalyticsAlertClient(New("k", url, WithSleeper(func(time.Duration) {})))
}

func ptr[T any](v T) *T { return &v }

func TestAlertClientCreateSendsFlattenedFieldsAsNestedWire(t *testing.T) {
	srv, got := newAlertTestServer(t, 201, alertJSON)
	ac := alertTestClient(srv.URL)

	a, err := ac.Create(context.Background(), "100", AlertWrite{
		Name:                    ptr("High crash rate"),
		Metric:                  ptr("ClientCrashRate15m"),
		Severity:                ptr("SEV_0"),
		Interval:                ptr("OneMinute"),
		ConsecutiveOccurrences:  ptr(int64(10)),
		Operator:                ptr("Gt"),
		Threshold:               ptr(2.5),
		EvaluationMode:          ptr("Absolute"),
		Filters:                 map[string][]string{"Platform": {"Phone", "Tablet"}},
		BreakdownDimension:      ptr("Platform"),
		WebhookConfigurationIDs: []string{"3f2b8c1e-5d4a-4f6b-9a7e-1c2d3e4f5a6b"},
		Enabled:                 ptr(false), // create has no configState; must not be sent
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPost || got.path != alertsPath {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name":                   "High crash rate",
		"metric":                 "ClientCrashRate15m",
		"severity":               "SEV_0",
		"interval":               "OneMinute",
		"consecutiveOccurrences": float64(10),
		"condition":              map[string]any{"operator": "Gt", "threshold": 2.5, "evaluationMode": "Absolute"},
		"filter":                 []any{map[string]any{"dimension": "Platform", "values": []any{"Phone", "Tablet"}}},
		"breakdown":              []any{map[string]any{"dimensions": []any{"Platform"}}},
		"webhookReceiverConfig":  map[string]any{"receivers": []any{map[string]any{"webhookConfigurationId": "3f2b8c1e-5d4a-4f6b-9a7e-1c2d3e4f5a6b"}}},
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("create body =\n%s", got.body)
	}

	// Response decode: nested wire → flat domain.
	if a.ID != "1234567890123" || a.UniverseID != "100" || a.Threshold != 2.5 || a.Operator != "Gt" ||
		a.ConsecutiveOccurrences != 10 || a.ConfigState != "Syncing" || a.Description != "" {
		t.Fatalf("decoded = %+v", a)
	}
	if !reflect.DeepEqual(a.Filters, map[string][]string{"Platform": {"Phone", "Tablet"}}) {
		t.Fatalf("filters = %v", a.Filters)
	}
	if !reflect.DeepEqual(a.BreakdownDimensions, []string{"Platform"}) {
		t.Fatalf("breakdown = %v", a.BreakdownDimensions)
	}
	if a.WebhookConfigurationIDs == nil || len(a.WebhookConfigurationIDs) != 0 {
		t.Fatalf("null webhookReceiverConfig must decode to an empty, non-nil slice; got %#v", a.WebhookConfigurationIDs)
	}
	if a.PeriodOffsetMultiplier != nil {
		t.Fatalf("absent periodOffsetMultiplier must stay nil")
	}
}

func TestAlertClientUpdateOmitsNilAndSendsExplicitClears(t *testing.T) {
	srv, got := newAlertTestServer(t, 200, alertJSON)
	ac := alertTestClient(srv.URL)

	_, err := ac.Update(context.Background(), "100", "1234567890123", AlertWrite{
		Threshold:               ptr(5.0),              // condition with only threshold
		Filters:                 map[string][]string{}, // clear filters
		BreakdownDimension:      ptr(""),               // clear breakdown
		WebhookConfigurationIDs: []string{},            // remove webhooks
		Enabled:                 ptr(false),            // → configState Disabled
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodPatch || got.path != alertsPath+"/1234567890123" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
	var body map[string]any
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"condition":             map[string]any{"threshold": float64(5)},
		"filter":                []any{},
		"breakdown":             []any{},
		"webhookReceiverConfig": map[string]any{"receivers": []any{}},
		"configState":           "Disabled",
	}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("patch body =\n%s", got.body)
	}
}

func TestAlertClientListByIDsAndNumericIDTolerance(t *testing.T) {
	// alertId served as a bare JSON number must still decode to its string form.
	srv, got := newAlertTestServer(t, 200, `[{"alertId": 42, "resourceId": "100", "name": "n", "metric": "m",
		"severity": "SEV_2", "interval": "OneDay", "consecutiveOccurrences": 1,
		"condition": {"operator": "Lt", "threshold": -30, "evaluationMode": "PeriodOverPeriod", "periodOffsetMultiplier": 7},
		"configState": "Enabled", "firingStatus": "OK",
		"webhookReceiverConfig": {"receivers": [{"webhookConfigurationId": "w1"}]}}]`)
	ac := alertTestClient(srv.URL)

	list, err := ac.List(context.Background(), "100", "42", "43")
	if err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodGet || got.path != alertsPath || got.rawQuery != "ids=42&ids=43" {
		t.Fatalf("request = %s %s?%s", got.method, got.path, got.rawQuery)
	}
	if len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}
	a := list[0]
	if a.ID != "42" || a.PeriodOffsetMultiplier == nil || *a.PeriodOffsetMultiplier != 7 || a.Threshold != -30 {
		t.Fatalf("decoded = %+v", a)
	}
	if !reflect.DeepEqual(a.WebhookConfigurationIDs, []string{"w1"}) {
		t.Fatalf("webhooks = %v", a.WebhookConfigurationIDs)
	}
	if len(a.Filters) != 0 || a.Filters == nil {
		t.Fatalf("absent filter must decode to an empty, non-nil map; got %#v", a.Filters)
	}
}

func TestAlertClientDelete(t *testing.T) {
	srv, got := newAlertTestServer(t, 204, "")
	if err := alertTestClient(srv.URL).Delete(context.Background(), "100", "7"); err != nil {
		t.Fatal(err)
	}
	if got.method != http.MethodDelete || got.path != alertsPath+"/7" {
		t.Fatalf("request = %s %s", got.method, got.path)
	}
}

// Live-observed (2026-10-23) once the key had the alert scopes: every endpoint
// returns this 403 for a universe below the alerts eligibility bar.
const notEnrolledBody = `{"errorCode":"PERMISSION_DENIED","message":"Universe 100 is not enrolled in PerformanceMonitoringV2."}`

func TestAlertClientTranslatesNotEnrolledOnEveryCall(t *testing.T) {
	srv, _ := newAlertTestServer(t, 403, notEnrolledBody)
	ac := alertTestClient(srv.URL)
	ctx := context.Background()
	calls := map[string]func() error{
		"list":   func() error { _, err := ac.List(ctx, "100"); return err },
		"create": func() error { _, err := ac.Create(ctx, "100", AlertWrite{}); return err },
		"update": func() error { _, err := ac.Update(ctx, "100", "1", AlertWrite{}); return err },
		"delete": func() error { return ac.Delete(ctx, "100", "1") },
	}
	for name, call := range calls {
		err := call()
		if !IsAlertsNotEligible(err) {
			t.Fatalf("%s: want a not-eligible error, got %v", name, err)
		}
		msg := err.Error()
		for _, want := range []string{"universe 100", "not eligible for analytics alerts", "100+ daily active users", "PerformanceMonitoringV2"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("%s: message missing %q:\n%s", name, want, msg)
			}
		}
		// The underlying API error stays reachable.
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != 403 || ae.Code != "PERMISSION_DENIED" {
			t.Fatalf("%s: wrapped APIError = %+v", name, ae)
		}
	}
}

func TestAlertClientOtherForbiddenIsNotTranslated(t *testing.T) {
	for _, body := range []string{
		`{"errors":[{"code":0,"message":"Scope not authorized."}]}`,
		`{"errorCode":"PERMISSION_DENIED","message":"Caller lacks Create and configure alerts."}`,
	} {
		srv, _ := newAlertTestServer(t, 403, body)
		_, err := alertTestClient(srv.URL).List(context.Background(), "100")
		if err == nil || IsAlertsNotEligible(err) {
			t.Fatalf("body %s: err = %v, must stay a plain APIError", body, err)
		}
	}
}

func TestAlertClientErrorEnvelopes(t *testing.T) {
	cases := []struct {
		name, body        string
		status            int
		wantCode, wantMsg string
	}{
		{"alerts service", `{"errorCode":"ALERT_NOT_FOUND","message":"Alert not found."}`, 404, "ALERT_NOT_FOUND", "Alert not found."},
		// Live-observed gateway envelope when the key lacks the alert scopes.
		{"gateway", `{"errors":[{"code":0,"message":"Scope not authorized."}]}`, 403, "", "Scope not authorized."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAlertTestServer(t, tc.status, tc.body)
			_, err := alertTestClient(srv.URL).List(context.Background(), "100")
			var ae *APIError
			if !errors.As(err, &ae) {
				t.Fatalf("want *APIError, got %v", err)
			}
			if ae.Status != tc.status || ae.Code != tc.wantCode || ae.Message != tc.wantMsg {
				t.Fatalf("APIError = %+v", ae)
			}
			if (tc.status == 404) != IsNotFound(err) {
				t.Fatalf("IsNotFound mismatch for %d", tc.status)
			}
		})
	}
}
