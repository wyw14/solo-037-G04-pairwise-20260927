package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/wyw14/solo-037/internal/service"
)

func TestMeasuredRunHTTPUsesOperatorEvidence(t *testing.T) {
	runtime, err := service.OpenRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewRouter(runtime).Handler()
	created := httptest.NewRecorder()
	server.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/measured-runs", strings.NewReader(`{"name":"tow A","model_name":"hull A","section":"section-a","carriage":"carriage-1"}`)))
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var response struct {
		Data service.MeasuredRun `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	id := response.Data.Operation.ID
	if id == "" {
		t.Fatal("missing ID")
	}
	stamp := time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
	input := `{"kind":"wave_time","source_id":"probe-a","value":0.24,"recorded_at":"` + stamp + `"}`
	recorded := httptest.NewRecorder()
	server.ServeHTTP(recorded, httptest.NewRequest(http.MethodPost, "/api/measured-runs/"+string(id)+"/evidence", strings.NewReader(input)))
	if recorded.Code != http.StatusOK {
		t.Fatalf("record: %d %s", recorded.Code, recorded.Body)
	}
	analysis := httptest.NewRecorder()
	server.ServeHTTP(analysis, httptest.NewRequest(http.MethodGet, "/api/measured-runs/"+string(id)+"/analysis", nil))
	if analysis.Code != http.StatusOK || !strings.Contains(analysis.Body.String(), `"mean":0.24`) {
		t.Fatalf("analysis: %d %s", analysis.Code, analysis.Body)
	}
	reloaded, err := service.OpenRuntime(t.TempDir())
	if err != nil || len(reloaded.MeasuredRuns()) != 0 {
		t.Fatal("archive separation failed", err)
	}
}

func TestResistanceHTTPPairsSpeedAndForceFrames(t *testing.T) {
	runtime, err := service.OpenRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewRouter(runtime).Handler()
	created := httptest.NewRecorder()
	server.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/measured-runs", strings.NewReader(`{"name":"tow B","model_name":"hull B","section":"section-a","carriage":"carriage-1"}`)))
	if created.Code != http.StatusOK {
		t.Fatalf("create: %d %s", created.Code, created.Body)
	}
	var response struct {
		Data service.MeasuredRun `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	id := string(response.Data.Operation.ID)
	stamp := func(offset time.Duration) string {
		return time.Now().Add(-time.Minute).Add(offset).UTC().Format(time.RFC3339Nano)
	}
	frames := []string{
		`{"kind":"velocity","source_id":"encoder","value":1.5,"recorded_at":"` + stamp(0) + `"}`,
		`{"kind":"velocity","source_id":"encoder","value":1.5,"recorded_at":"` + stamp(300*time.Millisecond) + `"}`,
		`{"kind":"force","source_id":"load-cell","value":22.0,"recorded_at":"` + stamp(100*time.Millisecond) + `"}`,
		`{"kind":"force","source_id":"load-cell","value":22.8,"recorded_at":"` + stamp(400*time.Millisecond) + `"}`,
	}
	for _, frame := range frames {
		recorded := httptest.NewRecorder()
		server.ServeHTTP(recorded, httptest.NewRequest(http.MethodPost, "/api/measured-runs/"+id+"/evidence", strings.NewReader(frame)))
		if recorded.Code != http.StatusOK {
			t.Fatalf("record: %d %s", recorded.Code, recorded.Body)
		}
	}
	curve := httptest.NewRecorder()
	server.ServeHTTP(curve, httptest.NewRequest(http.MethodGet, "/api/measured-runs/"+id+"/resistance?window=1s", nil))
	if curve.Code != http.StatusOK || !strings.Contains(curve.Body.String(), `"speed":1.5`) || !strings.Contains(curve.Body.String(), `"force":22.4`) {
		t.Fatalf("resistance: %d %s", curve.Code, curve.Body)
	}
	if !strings.Contains(curve.Body.String(), `"uncertainty"`) || !strings.Contains(curve.Body.String(), `"evidence"`) {
		t.Fatalf("curve missing uncertainty or traceability: %s", curve.Body)
	}
	bad := httptest.NewRecorder()
	server.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/measured-runs/"+id+"/resistance?window=abc", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad window accepted: %d", bad.Code)
	}
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/measured-runs/unknown/resistance", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown run analyzed: %d", missing.Code)
	}
}
