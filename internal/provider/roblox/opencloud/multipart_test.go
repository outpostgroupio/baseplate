package opencloud

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDoMultipartSendsJSONAndFileParts(t *testing.T) {
	var gotRequestName, gotFileName, gotFileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mt != "multipart/form-data" {
			http.Error(w, "not multipart", 400)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			body, _ := io.ReadAll(p)
			switch p.FormName() {
			case "request":
				var m map[string]string
				json.Unmarshal(body, &m)
				gotRequestName = m["name"]
			case "fileContent":
				gotFileName = p.FileName()
				gotFileBody = string(body)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"path": "/ops/1", "done": true,
			"response": map[string]string{"assetId": "9"}})
	}))
	defer srv.Close()

	c := New("k", srv.URL, WithSleeper(func(time.Duration) {}))
	var op operation
	if _, err := c.DoMultipart(context.Background(), http.MethodPost, "/v1/assets", []Part{
		{Name: "request", JSON: map[string]string{"name": "gem"}},
		{Name: "fileContent", Filename: "i.png", ContentType: "image/png", Data: []byte("PNGDATA")},
	}, &op); err != nil {
		t.Fatal(err)
	}
	if gotRequestName != "gem" {
		t.Fatalf("request part name = %q", gotRequestName)
	}
	if gotFileName != "i.png" || gotFileBody != "PNGDATA" {
		t.Fatalf("file part = %q / %q", gotFileName, gotFileBody)
	}
	if !op.Done {
		t.Fatalf("response not decoded: %+v", op)
	}
}
