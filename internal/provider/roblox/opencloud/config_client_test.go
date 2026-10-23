package opencloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const cfgPath = "/creator-configs-public-api/v1/configs/universes/42/repositories/InExperienceConfig"

func newConfigTestClient(t *testing.T, h http.HandlerFunc) ExperienceConfigClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewExperienceConfigClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
}

// The values endpoint shape, captured live: numbers come back as floats
// (100 -> 100.0), conditional branches are flat ".RBX.conditional.<id>" keys,
// and conditionalRules carries explicit nulls.
const liveValuesBody = `{"metadata":{"configVersion":3},"entries":{"i":101.0,"s":"hello2","s.RBX.conditional.c1":"hi-us","o":{"a":1.0}},"conditionalRules":{"rules":{"c1":{"tokens":[{"operator":"OPERATOR_EQ","operand":null}]}},"rulesOrder":["c1"]}}`

func TestConfigGetPublishedDecodesLiveShape(t *testing.T) {
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != cfgPath {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "k" {
			t.Error("missing api key")
		}
		io.WriteString(w, liveValuesBody)
	})
	got, err := cc.GetPublished(context.Background(), "42", "InExperienceConfig")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 || len(got.Entries) != 4 {
		t.Fatalf("got %+v", got)
	}
	if string(got.Entries["i"]) != "101.0" || string(got.Entries["s.RBX.conditional.c1"]) != `"hi-us"` {
		t.Fatalf("entries = %v", got.Entries)
	}
	if !strings.Contains(string(got.ConditionalRules), `"rulesOrder":["c1"]`) {
		t.Fatalf("rules = %s", got.ConditionalRules)
	}
}

func TestConfigGetPublishedNeverPublishedIsEmpty(t *testing.T) {
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		io.WriteString(w, `{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404}`)
	})
	got, err := cc.GetPublished(context.Background(), "42", "InExperienceConfig")
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 0 || len(got.Entries) != 0 || got.Entries == nil || got.ConditionalRules != nil {
		t.Fatalf("got %+v", got)
	}
}

func TestConfigGetPublishedForbiddenIsError(t *testing.T) {
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `"User does not have View permission for universe 42"`)
	})
	_, err := cc.GetPublished(context.Background(), "42", "InExperienceConfig")
	if err == nil || !strings.Contains(err.Error(), "View permission") {
		t.Fatalf("err = %v, want the bare-string message surfaced", err)
	}
}

func TestConfigGetDraft(t *testing.T) {
	t.Run("none", func(t *testing.T) {
		cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != cfgPath+"/draft" {
				t.Errorf("path %s", r.URL.Path)
			}
			w.WriteHeader(404)
		})
		d, err := cc.GetDraft(context.Background(), "42", "InExperienceConfig")
		if err != nil || d != nil {
			t.Fatalf("d=%v err=%v", d, err)
		}
	})
	t.Run("pending", func(t *testing.T) {
		cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"draftHash":"H","entries":{"x":{"value":1.0,"description":null},"gone":{"value":null,"description":""}},"conditionalRules":null}`)
		})
		d, err := cc.GetDraft(context.Background(), "42", "InExperienceConfig")
		if err != nil {
			t.Fatal(err)
		}
		if d.Hash != "H" || string(d.Entries["x"]) != "1.0" || string(d.Entries["gone"]) != "null" || d.ConditionalRules != nil {
			t.Fatalf("got %+v", d)
		}
	})
}

func TestConfigOverwriteDraftSendsBody(t *testing.T) {
	var got map[string]json.RawMessage
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != cfgPath+"/draft:overwrite" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type %q", ct)
		}
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"draftHash":"NEW"}`)
	})
	h, err := cc.OverwriteDraft(context.Background(), "42", "InExperienceConfig", ConfigOverwrite{
		PreviousDraftHash: "OLD",
		Entries:           map[string]json.RawMessage{"a": json.RawMessage(`true`)},
		ConditionalRules:  json.RawMessage(`{"rules":{}}`),
	})
	if err != nil || h != "NEW" {
		t.Fatalf("h=%q err=%v", h, err)
	}
	if string(got["draftHash"]) != `"OLD"` || string(got["entries"]) != `{"a":true}` || string(got["conditionalRules"]) != `{"rules":{}}` {
		t.Fatalf("body = %v", got)
	}
}

func TestConfigOverwriteDraftOmitsAbsentRulesAndHash(t *testing.T) {
	var got map[string]json.RawMessage
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"draftHash":"NEW"}`)
	})
	if _, err := cc.OverwriteDraft(context.Background(), "42", "InExperienceConfig", ConfigOverwrite{}); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["conditionalRules"]; ok {
		t.Fatal("conditionalRules must be omitted when nil")
	}
	if _, ok := got["draftHash"]; ok {
		t.Fatal("draftHash must be omitted when empty")
	}
	if string(got["entries"]) != `{}` {
		t.Fatalf("entries must be an explicit empty object, got %s", got["entries"])
	}
}

func TestConfigPublish(t *testing.T) {
	var got configPublishWire
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != cfgPath+"/publish" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, `{"configVersion":7}`)
	})
	v, err := cc.Publish(context.Background(), "42", "InExperienceConfig", "H", "msg", "Immediate")
	if err != nil || v != 7 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	if got.DraftHash != "H" || got.Message != "msg" || got.DeploymentStrategy != "Immediate" {
		t.Fatalf("body = %+v", got)
	}
}

func TestConfigPublishEmptyDraftIsClassified(t *testing.T) {
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		io.WriteString(w, `{"validationErrors":[{"key":"","code":"EmptyDraft","message":"The draft contains no changes from the current published configuration and cannot be published."}]}`)
	})
	_, err := cc.Publish(context.Background(), "42", "InExperienceConfig", "H", "", "")
	if !IsEmptyDraft(err) {
		t.Fatalf("IsEmptyDraft(%v) = false", err)
	}
	if IsEmptyDraft(&APIError{Status: 400, Message: "The provided draft hash does not match the current draft."}) {
		t.Fatal("a hash mismatch is not an empty draft")
	}
}

func TestConfigOverwriteDuringRolloutIsClassified(t *testing.T) {
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(400)
		io.WriteString(w, "MultipleDraftNotSupported") // verbatim live response
	})
	_, err := cc.OverwriteDraft(context.Background(), "42", "InExperienceConfig", ConfigOverwrite{})
	if !IsMultipleDraft(err) {
		t.Fatalf("IsMultipleDraft(%v) = false", err)
	}
	if IsEmptyDraft(err) {
		t.Fatal("not an empty draft")
	}
}

func TestConfigDiscardDraft(t *testing.T) {
	var body string
	cc := newConfigTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != cfgPath+"/draft" {
			t.Errorf("got %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		if body == `{}` {
			w.WriteHeader(404) // no draft to discard
			return
		}
		io.WriteString(w, `{"draftHash":"H"}`)
	})
	if err := cc.DiscardDraft(context.Background(), "42", "InExperienceConfig", "H"); err != nil {
		t.Fatal(err)
	}
	if body != `{"draftHash":"H"}` {
		t.Fatalf("body = %s", body)
	}
	if err := cc.DiscardDraft(context.Background(), "42", "InExperienceConfig", ""); err != nil {
		t.Fatalf("discarding a missing draft must be a no-op, got %v", err)
	}
}

func TestFallbackErrorMessage(t *testing.T) {
	cases := map[string]string{
		``:               "",
		`"plain reason"`: "plain reason",
		`{"type":"https://tools.ietf.org/html/rfc9110#section-15.5.5","title":"Not Found","status":404}`: "",
		`{ "validationErrors": [ {"code":"X"} ] }`:                                                       `{"validationErrors":[{"code":"X"}]}`,
		`not json`: "not json",
	}
	for in, want := range cases {
		if got := fallbackErrorMessage([]byte(in)); got != want {
			t.Errorf("fallbackErrorMessage(%q) = %q, want %q", in, got, want)
		}
	}
}
