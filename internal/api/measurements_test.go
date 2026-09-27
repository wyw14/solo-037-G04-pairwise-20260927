package api

import (
	"encoding/json"
	"fmt"
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

func TestMeasuredRunResistanceHTTP(t *testing.T) {
	runtime, err := service.OpenRuntime(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := NewRouter(runtime).Handler()
	created := httptest.NewRecorder()
	server.ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/measured-runs", strings.NewReader(`{"name":"tow R","model_name":"hull R","section":"section-a","carriage":"carriage-1"}`)))
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
	stamp := time.Now().Add(-time.Minute).UTC()
	for index, input := range []string{
		`{"kind":"speed","source_id":"gps","value":1.5,"recorded_at":"%s"}`,
		`{"kind":"speed","source_id":"gps","value":1.5,"recorded_at":"%s"}`,
		`{"kind":"force","source_id":"load-cell","value":220,"recorded_at":"%s"}`,
		`{"kind":"force","source_id":"load-cell","value":224,"recorded_at":"%s"}`,
	} {
		at := stamp.Add(time.Duration(index) * 100 * time.Millisecond).Format(time.RFC3339Nano)
		recorded := httptest.NewRecorder()
		server.ServeHTTP(recorded, httptest.NewRequest(http.MethodPost, "/api/measured-runs/"+id+"/evidence", strings.NewReader(fmt.Sprintf(input, at))))
		if recorded.Code != http.StatusOK {
			t.Fatalf("record %d: %d %s", index, recorded.Code, recorded.Body)
		}
	}
	resistance := httptest.NewRecorder()
	server.ServeHTTP(resistance, httptest.NewRequest(http.MethodGet, "/api/measured-runs/"+id+"/resistance?window_ms=1000", nil))
	if resistance.Code != http.StatusOK {
		t.Fatalf("resistance: %d %s", resistance.Code, resistance.Body)
	}
	body := resistance.Body.String()
	for _, fragment := range []string{`"speed_mean":1.5`, `"force_mean":222`, `"force_count":2`, `"force_interval_half_width"`, `"speed_evidence"`} {
		if !strings.Contains(body, fragment) {
			t.Fatalf("resistance response missing %s: %s", fragment, body)
		}
	}
	bad := httptest.NewRecorder()
	server.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/measured-runs/"+id+"/resistance?window_ms=abc", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid window_ms accepted: %d", bad.Code)
	}
	missing := httptest.NewRecorder()
	server.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/measured-runs/unknown/resistance", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("unknown run analyzed: %d", missing.Code)
	}
}
