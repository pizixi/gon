package fontset

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"golang.org/x/image/font/gofont/gomono"
)

func TestInstallPresetRetriesAfterAnIncompleteDownload(t *testing.T) {
	var licenseReady atomic.Bool
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/font" {
			_, _ = w.Write(gomono.TTF)
			return
		}
		if !licenseReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("Fixture license"))
	}))
	defer server.Close()
	p := preset{id: "fixture", label: "Fixture", assets: []fontAsset{
		{"Regular.ttf", server.URL + "/font"}, {"LICENSE.txt", server.URL + "/license"},
	}}
	dir := t.TempDir()
	if err := installPreset(t.Context(), server.Client(), dir, p); err == nil || p.installed(dir) {
		t.Fatal("an incomplete download became selectable")
	}
	licenseReady.Store(true)
	if err := installPreset(t.Context(), server.Client(), dir, p); err != nil {
		t.Fatal(err)
	}
	path, err := ResolveChoice(dir, p.value())
	if err != nil {
		t.Fatal(err)
	}
	face, err := LoadFace(path)
	if err != nil {
		t.Fatal(err)
	}
	face.close()
	prior := requests.Load()
	if err := installPreset(t.Context(), server.Client(), dir, p); err != nil || requests.Load() != prior {
		t.Fatalf("reselecting an installed font attempted to download it: %v", err)
	}
}
