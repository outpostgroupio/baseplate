// Package output owns Baseplate's machine-readable output envelope and the
// text/JSON dispatch every CLI command uses, so the JSON schema lives in one
// place. The envelope is versioned via FormatVersion for CI consumers.
package output

import (
	"encoding/json"
	"fmt"
	"io"
)

// FormatVersion is the schema version carried by every JSON envelope.
const FormatVersion = 1

// Format selects human text or machine JSON output.
type Format int

const (
	Text Format = iota
	JSON
)

type successEnvelope struct {
	FormatVersion int    `json:"format_version"`
	Command       string `json:"command"`
	Result        any    `json:"result"`
}

type errorEnvelope struct {
	FormatVersion int       `json:"format_version"`
	Command       string    `json:"command"`
	Error         errorBody `json:"error"`
}

type errorBody struct {
	Message string `json:"message"`
}

// Emit writes a successful command result to w (stdout). In Text mode it writes
// the human body verbatim; in JSON mode it writes the versioned envelope.
func Emit(w io.Writer, f Format, command, text string, result any) error {
	if f == JSON {
		b, err := json.MarshalIndent(successEnvelope{FormatVersion, command, result}, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, string(b))
		return err
	}
	_, err := fmt.Fprint(w, text)
	return err
}

// EmitError writes a command failure to w (stderr). Text mode mirrors the
// legacy "Error: <msg>" line; JSON mode writes the versioned error envelope.
func EmitError(w io.Writer, f Format, command string, err error) {
	if f == JSON {
		b, mErr := json.MarshalIndent(errorEnvelope{FormatVersion, command, errorBody{err.Error()}}, "", "  ")
		if mErr != nil {
			fmt.Fprintln(w, "Error:", err.Error())
			return
		}
		fmt.Fprintln(w, string(b))
		return
	}
	fmt.Fprintln(w, "Error:", err.Error())
}
