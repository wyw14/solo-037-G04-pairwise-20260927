package resistance

import (
	"testing"
	"time"

	"github.com/wyw14/solo-037/internal/model"
)

func frame(run model.RunID, revision model.RevisionID, kind model.EvidenceKind, value float64, at time.Time) model.Evidence {
	return model.NewEvidence(run, revision, "sensor", kind, value, at)
}

func TestAnalyzePairsWindowsRejectsAbnormalAndStaysTraceable(t *testing.T) {
	run := model.NewRunID()
	revision := model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	at := func(seconds float64) time.Time {
		return anchor.Add(time.Duration(seconds * float64(time.Second)))
	}
	speeds := []model.Evidence{
		frame(run, revision, model.EvidenceVelocity, 1.0, at(0.1)),
		frame(run, revision, model.EvidenceVelocity, 1.01, at(0.5)),
		frame(run, revision, model.EvidenceVelocity, 0.99, at(0.9)),
		frame(run, revision, model.EvidenceVelocity, 0, at(0.3)), // carriage not moving: abnormal
		frame(run, revision, model.EvidenceVelocity, 1.5, at(1.2)),
		frame(run, revision, model.EvidenceVelocity, 2.0, at(3.1)),
		frame(run, revision, model.EvidenceVelocity, 2.0, at(3.6)),
	}
	forces := []model.Evidence{
		frame(run, revision, model.EvidenceForce, 10.0, at(0.15)),
		frame(run, revision, model.EvidenceForce, 10.1, at(0.45)),
		frame(run, revision, model.EvidenceForce, 9.9, at(0.75)),
		frame(run, revision, model.EvidenceForce, 10.05, at(0.95)),
		frame(run, revision, model.EvidenceForce, 500.0, at(0.55)), // spike: abnormal
		frame(run, revision, model.EvidenceForce, 40.0, at(3.2)),
		frame(run, revision, model.EvidenceForce, 40.4, at(3.7)),
	}
	curve := Analyze(run, revision, speeds, forces, time.Second)
	if curve.Run != run || curve.Revision != revision {
		t.Fatalf("curve identity lost: %+v", curve)
	}
	if len(curve.Points) != 2 {
		t.Fatalf("expected 2 paired points, got %+v", curve.Points)
	}
	first, second := curve.Points[0], curve.Points[1]
	if first.Speed > second.Speed {
		t.Fatalf("points not sorted by speed: %+v", curve.Points)
	}
	if first.SpeedFrames != 3 || first.ForceFrames != 4 {
		t.Fatalf("window 0 frame counts wrong: %+v", first)
	}
	if diff := first.Force - 10.0125; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("window 0 force mean wrong: %+v", first)
	}
	if first.Uncertainty <= 0 || first.ForceSpread <= 0 {
		t.Fatalf("window 0 uncertainty missing: %+v", first)
	}
	if len(first.Evidence) != first.SpeedFrames+first.ForceFrames {
		t.Fatalf("point not traceable to source frames: %+v", first)
	}
	if diff := second.Force - 40.2; second.Speed != 2.0 || diff < -1e-9 || diff > 1e-9 || second.ForceFrames != 2 {
		t.Fatalf("window 3 point wrong: %+v", second)
	}
	if len(curve.Rejected) != 2 {
		t.Fatalf("expected 2 rejected frames, got %+v", curve.Rejected)
	}
	if curve.Rejected[0].Reason == "" || curve.Rejected[0].Evidence == "" {
		t.Fatalf("rejected frame not traceable: %+v", curve.Rejected)
	}
	if curve.EmptyWindows != 1 {
		t.Fatalf("expected 1 empty window, got %d", curve.EmptyWindows)
	}
	if len(curve.Dropped) != 1 || curve.Dropped[0].Reason != "no_force_frames" {
		t.Fatalf("expected speed-only window dropped, got %+v", curve.Dropped)
	}
}

func TestAnalyzeWithoutPairableFramesReturnsEmptyCurve(t *testing.T) {
	run := model.NewRunID()
	revision := model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	speeds := []model.Evidence{frame(run, revision, model.EvidenceVelocity, 1.0, anchor)}
	curve := Analyze(run, revision, speeds, nil, 0)
	if curve.Window != DefaultWindow {
		t.Fatalf("zero window did not fall back to default: %+v", curve)
	}
	if len(curve.Points) != 0 || len(curve.Dropped) != 0 || len(curve.Rejected) != 0 {
		t.Fatalf("unpairable input produced output: %+v", curve)
	}
}
