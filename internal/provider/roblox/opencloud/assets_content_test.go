package opencloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAssetUpdateContentSendsFileContentPart(t *testing.T) {
	var gotMask, gotFileCT string
	var gotFile []byte
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/assets/777":
			gotMask = r.URL.Query().Get("updateMask")
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
			}
			if err := json.Unmarshal([]byte(r.FormValue("request")), &gotReq); err != nil {
				t.Errorf("request part: %v", err)
			}
			f, h, err := r.FormFile("fileContent")
			if err != nil {
				t.Errorf("fileContent part: %v", err)
			} else {
				gotFileCT = h.Header.Get("Content-Type")
				gotFile, _ = io.ReadAll(f)
			}
			json.NewEncoder(w).Encode(map[string]any{"path": "operations/op3", "done": true,
				"response": map[string]any{"assetId": "777", "displayName": "Tree", "assetType": "Model"}})
		default:
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, 404)
		}
	}))
	defer srv.Close()

	ac := NewAssetClient(New("k", srv.URL, WithSleeper(func(time.Duration) {})))
	a, err := ac.UpdateContent(context.Background(), Asset{ID: "777", AssetType: "Model", DisplayName: "Tree"}, "tree.fbx", "model/fbx", []byte("FBX"))
	if err != nil {
		t.Fatal(err)
	}
	if gotMask != "displayName,description" || gotReq["assetType"] != "Model" || gotReq["assetId"] != "777" {
		t.Fatalf("mask=%q request=%+v", gotMask, gotReq)
	}
	if string(gotFile) != "FBX" || gotFileCT != "model/fbx" {
		t.Fatalf("fileContent = %q (%s)", gotFile, gotFileCT)
	}
	if a.ID != "777" {
		t.Fatalf("got %+v", a)
	}
}
