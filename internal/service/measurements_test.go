package service

import (
	"math"
	"testing"
	"time"

	"github.com/wyw14/solo-037/internal/model"
)

func TestOperatorMeasurementsPersistAndAnalyzeSuppliedValues(t *testing.T) {
	directory := t.TempDir()
	a, err := OpenMeasurementArchive(directory)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.Create("test A", "hull X", "section-a", "carriage-1")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC()
	for _, input := range []MeasurementInput{
		{Kind: model.EvidenceWaveTime, SourceID: "left", Value: 0.1, RecordedAt: at},
		{Kind: model.EvidenceWaveTime, SourceID: "right", Value: 0.3, RecordedAt: at.Add(time.Second)},
		{Kind: model.EvidenceForce, SourceID: "load-cell", Value: 12, RecordedAt: at},
	} {
		if _, err := a.Record(run.Operation.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := OpenMeasurementArchive(directory)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := loaded.Analyze(run.Operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Wave.Count != 2 || math.Abs(analysis.Wave.Mean-0.2) > 1e-9 || analysis.ForceCount != 1 || analysis.ForceMean != 12 {
		t.Fatalf("analysis did not reflect supplied values after reload: %+v", analysis)
	}
	if len(loaded.List()) != 1 || len(loaded.List()[0].Evidence) != 3 || !loaded.List()[0].Evidence[0].Durable {
		t.Fatalf("persisted evidence missing: %+v", loaded.List())
	}
}

func TestOperatorMeasurementRejectsInvalidAndUnknownRun(t *testing.T) {
	a, err := OpenMeasurementArchive(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.Create("test B", "hull X", "section-a", "carriage-1")
	if err != nil {
		t.Fatal(err)
	}
	input := MeasurementInput{Kind: model.EvidenceWaveTime, SourceID: "left", Value: 1, RecordedAt: time.Now().UTC()}
	if _, err := a.Record("missing", input); err == nil {
		t.Fatal("unknown run accepted")
	}
	input.Value = math.NaN()
	if _, err := a.Record(run.Operation.ID, input); err == nil {
		t.Fatal("NaN accepted")
	}
	if len(a.List()[0].Evidence) != 0 {
		t.Fatal("rejected values changed archive")
	}
}

func TestResistanceAnalysisPairsPersistedSpeedAndForceFrames(t *testing.T) {
	directory := t.TempDir()
	a, err := OpenMeasurementArchive(directory)
	if err != nil {
		t.Fatal(err)
	}
	run, err := a.Create("tow C", "hull Y", "section-a", "carriage-1")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Minute).UTC()
	inputs := []MeasurementInput{
		{Kind: model.EvidenceVelocity, SourceID: "encoder", Value: 1.0, RecordedAt: at},
		{Kind: model.EvidenceVelocity, SourceID: "encoder", Value: 1.02, RecordedAt: at.Add(400 * time.Millisecond)},
		{Kind: model.EvidenceForce, SourceID: "load-cell", Value: 12.0, RecordedAt: at.Add(100 * time.Millisecond)},
		{Kind: model.EvidenceForce, SourceID: "load-cell", Value: 12.4, RecordedAt: at.Add(500 * time.Millisecond)},
		{Kind: model.EvidenceVelocity, SourceID: "encoder", Value: 2.0, RecordedAt: at.Add(2 * time.Second)},
		{Kind: model.EvidenceForce, SourceID: "load-cell", Value: 48.0, RecordedAt: at.Add(2 * time.Second)},
	}
	for _, input := range inputs {
		if _, err := a.Record(run.Operation.ID, input); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := OpenMeasurementArchive(directory)
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := loaded.AnalyzeResistance(run.Operation.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	points := analysis.Curve.Points
	if len(points) != 2 {
		t.Fatalf("expected 2 paired windows after reload, got %+v", analysis.Curve)
	}
	if math.Abs(points[0].Speed-1.01) > 1e-9 || math.Abs(points[0].Force-12.2) > 1e-9 || points[0].Uncertainty <= 0 {
		t.Fatalf("first point wrong: %+v", points[0])
	}
	if points[1].Speed != 2.0 || points[1].Force != 48.0 || points[1].ForceFrames != 1 {
		t.Fatalf("second point wrong: %+v", points[1])
	}
	if len(points[0].Evidence) != 4 {
		t.Fatalf("point not traceable to archived frames: %+v", points[0])
	}
	if _, err := loaded.AnalyzeResistance("missing", 0); err == nil {
		t.Fatal("unknown run analyzed")
	}
}
