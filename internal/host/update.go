package host

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	"github.com/k0ngk0ng/wire-tools/internal/update"
)

// UpdateHandler exposes the same installation transaction to terminal and UI
// clients. The caller cannot override the installed executable being updated.
func UpdateHandler(stop context.CancelFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Version   string `json:"version,omitempty"`
			Archive   string `json:"archive,omitempty"`
			Checksums string `json:"checksums,omitempty"`
		}
		d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
		d.DisallowUnknownFields()
		if err := d.Decode(&input); err != nil {
			JSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		var extra any
		if err := d.Decode(&extra); err != io.EOF {
			JSON(w, 400, map[string]string{"error": "expected one JSON value"})
			return
		}
		if input.Archive != "" && !filepath.IsAbs(input.Archive) || input.Checksums != "" && !filepath.IsAbs(input.Checksums) {
			JSON(w, 400, map[string]string{"error": "offline update paths must be absolute"})
			return
		}
		result, err := update.Run(r.Context(), update.Options{Version: input.Version, Archive: input.Archive, Checksums: input.Checksums})
		if err != nil {
			code := 422
			if errors.Is(err, context.Canceled) {
				code = 408
			}
			JSON(w, code, map[string]string{"error": err.Error()})
			return
		}
		JSON(w, 200, map[string]any{"version": result.Version, "package_manager": result.PackageManager, "runtime_restarting": true})
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		go stop()
	}
}
