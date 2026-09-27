package resistance

import (
	"math"
	"testing"
	"time"

	"github.com/wyw14/solo-037/internal/model"
)

func frame(run model.RunID, revision model.RevisionID, kind model.EvidenceKind, value float64, at time.Time) model.Evidence {
	return model.NewEvidence(run, revision, "bench", kind, value, at)
}

func TestAnalyzePairsSpeedAndForcePerWindow(t *testing.T) {
	run, revision := model.NewRunID(), model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	evidence := []model.Evidence{
		// window 0: ~1 m/s plateau (first frame defines the grid anchor)
		frame(run, revision, model.EvidenceSpeed, 1.0, anchor),
		frame(run, revision, model.EvidenceSpeed, 1.02, anchor.Add(100*time.Millisecond)),
		frame(run, revision, model.EvidenceSpeed, 0.98, anchor.Add(200*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 100, anchor.Add(50*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 102, anchor.Add(150*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 98, anchor.Add(250*time.Millisecond)),
		// window 1 intentionally left empty
		// window 2: ~2 m/s plateau
		frame(run, revision, model.EvidenceSpeed, 2.0, anchor.Add(2*time.Second)),
		frame(run, revision, model.EvidenceSpeed, 2.02, anchor.Add(2100*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 400, anchor.Add(2050*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 404, anchor.Add(2150*time.Millisecond)),
		// window 4: force only, cannot be paired
		frame(run, revision, model.EvidenceForce, 150, anchor.Add(4*time.Second)),
	}
	result := Analyze(evidence, Params{Window: time.Second})
	if result.Parameters.WindowMS != 1000 || result.Parameters.OutlierSigma != DefaultOutlierSigma {
		t.Fatalf("parameters not echoed: %+v", result.Parameters)
	}
	if len(result.Points) != 2 {
		t.Fatalf("expected 2 curve points, got %+v", result.Points)
	}
	// points are ordered by speed to form the resistance-speed curve
	slow, fast := result.Points[0], result.Points[1]
	if math.Abs(slow.SpeedMean-1.0) > 1e-9 || math.Abs(slow.ForceMean-100) > 1e-9 {
		t.Fatalf("slow window mismatch: %+v", slow)
	}
	if math.Abs(fast.SpeedMean-2.01) > 1e-9 || math.Abs(fast.ForceMean-402) > 1e-9 {
		t.Fatalf("fast window mismatch: %+v", fast)
	}
	if slow.SpeedCount != 3 || slow.ForceCount != 3 || len(slow.SpeedEvidence) != 3 || len(slow.ForceEvidence) != 3 {
		t.Fatalf("traceability counts wrong: %+v", slow)
	}
	// 95% interval around force mean 100: stddev sqrt(8/3), SE /sqrt(3)
	expectedHalf := 1.96 * math.Sqrt(8.0/3.0) / math.Sqrt(3)
	if math.Abs(slow.ForceIntervalHalfWidth-expectedHalf) > 1e-9 ||
		math.Abs(slow.ForceLower-(100-expectedHalf)) > 1e-9 ||
		math.Abs(slow.ForceUpper-(100+expectedHalf)) > 1e-9 {
		t.Fatalf("uncertainty interval wrong: %+v", slow)
	}
	if result.EmptyWindows != 2 { // windows 1 and 3 hold no frames at all
		t.Fatalf("empty windows not counted: %+v", result.EmptyWindows)
	}
	if len(result.DroppedWindows) != 1 || result.DroppedWindows[0].Reason != DropMissingSpeed {
		t.Fatalf("force-only window not dropped traceably: %+v", result.DroppedWindows)
	}
	if len(result.RejectedFrames) != 0 {
		t.Fatalf("clean evidence rejected: %+v", result.RejectedFrames)
	}
}

func TestAnalyzeRejectsAbnormalFrames(t *testing.T) {
	run, revision := model.NewRunID(), model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)
	evidence := []model.Evidence{
		frame(run, revision, model.EvidenceSpeed, 1.0, anchor),
		frame(run, revision, model.EvidenceSpeed, -0.5, anchor.Add(100*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 100, anchor),
		frame(run, revision, model.EvidenceForce, 101, anchor.Add(50*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 99, anchor.Add(100*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 100, anchor.Add(150*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 5000, anchor.Add(200*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, math.NaN(), anchor.Add(250*time.Millisecond)),
	}
	result := Analyze(evidence, Params{Window: time.Second})
	reasons := map[string]int{}
	for _, rejected := range result.RejectedFrames {
		reasons[rejected.Reason]++
	}
	if reasons[ReasonNonPositiveSpeed] != 1 || reasons[ReasonOutlier] != 1 || reasons[ReasonNonFinite] != 1 {
		t.Fatalf("rejections not classified: %+v", result.RejectedFrames)
	}
	if len(result.Points) != 1 {
		t.Fatalf("expected one paired window, got %+v", result.Points)
	}
	point := result.Points[0]
	if math.Abs(point.SpeedMean-1.0) > 1e-9 || math.Abs(point.ForceMean-100) > 1e-9 {
		t.Fatalf("abnormal frames leaked into statistics: %+v", point)
	}
	if point.ForceCount != 4 || len(point.ForceEvidence) != 4 {
		t.Fatalf("kept force frames miscounted: %+v", point)
	}
}

func TestAnalyzeUncertaintyNarrowsWithMoreFrames(t *testing.T) {
	run, revision := model.NewRunID(), model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	evidence := []model.Evidence{
		frame(run, revision, model.EvidenceSpeed, 1.0, anchor),
		frame(run, revision, model.EvidenceSpeed, 2.0, anchor.Add(2*time.Second)),
	}
	// window 0: 3 force frames, window 2: same values duplicated to 6 frames
	for index, value := range []float64{100, 102, 98} {
		evidence = append(evidence, frame(run, revision, model.EvidenceForce, value, anchor.Add(time.Duration(index)*100*time.Millisecond)))
	}
	for index := 0; index < 2; index++ {
		for slot, value := range []float64{100, 102, 98} {
			evidence = append(evidence, frame(run, revision, model.EvidenceForce, value, anchor.Add(2*time.Second+time.Duration(index*3+slot)*100*time.Millisecond)))
		}
	}
	result := Analyze(evidence, Params{Window: time.Second})
	if len(result.Points) != 2 {
		t.Fatalf("expected 2 points, got %+v", result.Points)
	}
	sparse, dense := result.Points[0], result.Points[1]
	if sparse.ForceCount != 3 || dense.ForceCount != 6 {
		t.Fatalf("frame counts wrong: %+v", result.Points)
	}
	if math.Abs(sparse.ForceStddev-dense.ForceStddev) > 1e-9 {
		t.Fatalf("stddev should match: %+v vs %+v", sparse, dense)
	}
	if !(dense.ForceIntervalHalfWidth < sparse.ForceIntervalHalfWidth) {
		t.Fatalf("interval did not narrow with more frames: %+v vs %+v", sparse, dense)
	}
}

func TestAnalyzeKeepsQuantizedJitterButRejectsSpike(t *testing.T) {
	run, revision := model.NewRunID(), model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 12, 30, 0, 0, time.UTC)
	evidence := []model.Evidence{
		// quantized sensor alternating 1.00/1.02: MAD is zero, none are outliers
		frame(run, revision, model.EvidenceSpeed, 1.0, anchor),
		frame(run, revision, model.EvidenceSpeed, 1.02, anchor.Add(100*time.Millisecond)),
		frame(run, revision, model.EvidenceSpeed, 1.0, anchor.Add(200*time.Millisecond)),
		frame(run, revision, model.EvidenceSpeed, 1.02, anchor.Add(300*time.Millisecond)),
		frame(run, revision, model.EvidenceSpeed, 1.0, anchor.Add(400*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 50, anchor),
		frame(run, revision, model.EvidenceForce, 50, anchor.Add(100*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 50, anchor.Add(200*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 50, anchor.Add(300*time.Millisecond)),
		frame(run, revision, model.EvidenceForce, 500, anchor.Add(400*time.Millisecond)),
	}
	result := Analyze(evidence, Params{Window: time.Second})
	if len(result.Points) != 1 {
		t.Fatalf("expected one point, got %+v", result.Points)
	}
	point := result.Points[0]
	if point.SpeedCount != 5 {
		t.Fatalf("quantized speed jitter rejected: %+v", point)
	}
	if point.ForceCount != 4 || point.ForceMean != 50 {
		t.Fatalf("force spike not rejected: %+v", point)
	}
	if len(result.RejectedFrames) != 1 || result.RejectedFrames[0].Reason != ReasonOutlier {
		t.Fatalf("expected exactly one outlier rejection: %+v", result.RejectedFrames)
	}
}

func TestAnalyzeHandlesEmptyAndUnpairableEvidence(t *testing.T) {
	empty := Analyze(nil, Params{})
	if len(empty.Points) != 0 || len(empty.DroppedWindows) != 0 || empty.EmptyWindows != 0 {
		t.Fatalf("empty evidence produced output: %+v", empty)
	}
	run, revision := model.NewRunID(), model.NewRevisionID()
	anchor := time.Date(2026, 9, 27, 13, 0, 0, 0, time.UTC)
	result := Analyze([]model.Evidence{
		frame(run, revision, model.EvidenceSpeed, 1.5, anchor),
		frame(run, revision, model.EvidenceWaveTime, 0.2, anchor),
	}, Params{})
	if len(result.Points) != 0 || len(result.DroppedWindows) != 1 || result.DroppedWindows[0].Reason != DropMissingForce {
		t.Fatalf("speed-only window not dropped: %+v", result)
	}
	if result.Parameters.WindowMS != DefaultWindow.Milliseconds() {
		t.Fatalf("default window not applied: %+v", result.Parameters)
	}
}
