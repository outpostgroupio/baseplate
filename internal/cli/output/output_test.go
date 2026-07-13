package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEmitTextWritesBodyVerbatim(t *testing.T) {
	var b bytes.Buffer
	if err := Emit(&b, Text, "validate", "Configuration is valid.\n", map[string]bool{"valid": true}); err != nil {
		t.Fatal(err)
	}
	if b.String() != "Configuration is valid.\n" {
		t.Fatalf("text mode should write the body verbatim, got %q", b.String())
	}
}

func TestEmitJSONWrapsResultInEnvelope(t *testing.T) {
	var b bytes.Buffer
	if err := Emit(&b, JSON, "validate", "ignored", map[string]bool{"valid": true}); err != nil {
		t.Fatal(err)
	}
	var env struct {
		FormatVersion int             `json:"format_version"`
		Command       string          `json:"command"`
		Result        json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(b.Bytes(), &env); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, b.String())
	}
	if env.FormatVersion != 1 || env.Command != "validate" {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if !strings.Contains(string(env.Result), `"valid": true`) {
		t.Fatalf("result payload missing: %s", env.Result)
	}
}

func TestEmitErrorTextAndJSON(t *testing.T) {
	var text bytes.Buffer
	EmitError(&text, Text, "apply", errors.New("boom"))
	if text.String() != "Error: boom\n" {
		t.Fatalf("text error wrong: %q", text.String())
	}

	var js bytes.Buffer
	EmitError(&js, JSON, "apply", errors.New("boom"))
	var env struct {
		FormatVersion int    `json:"format_version"`
		Command       string `json:"command"`
		Error         struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(js.Bytes(), &env); err != nil {
		t.Fatalf("json error not valid JSON: %v\n%s", err, js.String())
	}
	if env.FormatVersion != 1 || env.Command != "apply" || env.Error.Message != "boom" {
		t.Fatalf("json error envelope wrong: %+v", env)
	}
}
