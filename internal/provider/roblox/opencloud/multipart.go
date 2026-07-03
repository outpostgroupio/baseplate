package opencloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
)

// Part is one part of a multipart/form-data request:
//   - JSON != nil            → a JSON value part (content-type application/json)
//   - Filename != ""         → a file part with the given Filename/ContentType
//   - otherwise              → a plain text form field with value string(Data)
type Part struct {
	Name        string
	Filename    string
	ContentType string
	JSON        any
	Data        []byte
}

// DoMultipart sends a multipart/form-data request (used by asset and developer
// product creation) with the same auth, retry/backoff, and error translation as
// Do, decoding a JSON response into out.
func (c *Client) DoMultipart(ctx context.Context, method, path string, parts []Part, out any) (*http.Response, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, p := range parts {
		h := textproto.MIMEHeader{}
		if p.JSON != nil {
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, p.Name))
			h.Set("Content-Type", "application/json")
			w, err := mw.CreatePart(h)
			if err != nil {
				return nil, err
			}
			b, err := json.Marshal(p.JSON)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(b); err != nil {
				return nil, err
			}
			continue
		}
		if p.Filename == "" {
			// Plain text form field (e.g. name=..., price=...).
			w, err := mw.CreateFormField(p.Name)
			if err != nil {
				return nil, err
			}
			if _, err := w.Write(p.Data); err != nil {
				return nil, err
			}
			continue
		}
		h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, p.Name, p.Filename))
		if p.ContentType != "" {
			h.Set("Content-Type", p.ContentType)
		}
		w, err := mw.CreatePart(h)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.Data); err != nil {
			return nil, err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}
	return c.send(ctx, method, path, mw.FormDataContentType(), buf.Bytes(), out)
}
