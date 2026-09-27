// Package resistance pairs actual carriage speed frames with force frames
// recorded during the same tow and derives a resistance-versus-speed curve
// with an uncertainty interval per point. Pairing is done over fixed time
// windows anchored at the first frame of the tow; windows without both
// kinds of frame are dropped and abnormal frames are rejected, and every
// decision stays traceable through the returned structures.
package resistance

import (
	"math"
	"sort"
	"time"

	"github.com/wyw14/solo-037/internal/model"
)

// DefaultWindow is the pairing window used when the caller does not pick one.
const DefaultWindow = time.Second

// MaxWindow bounds the pairing window so one window cannot span a whole run.
const MaxWindow = 10 * time.Minute

// robustZLimit is the rejection limit for the robust z-score 0.6745*(x-med)/MAD.
const robustZLimit = 3.5

// Point is one paired window: mean speed, mean resistance force and the 95%
// uncertainty half-width of the force mean. Evidence lists the frame IDs that
// produced the point so it can be traced back to the archived records.
type Point struct {
	WindowStart time.Time       `json:"window_start"`
	WindowEnd   time.Time       `json:"window_end"`
	Speed       float64         `json:"speed"`
	SpeedSpread float64         `json:"speed_spread"`
	Force       float64         `json:"force"`
	ForceSpread float64         `json:"force_spread"`
	Uncertainty float64         `json:"uncertainty"`
	SpeedFrames int             `json:"speed_frames"`
	ForceFrames int             `json:"force_frames"`
	Evidence    []model.EventID `json:"evidence"`
}

// DroppedWindow records a window that held frames but produced no point.
type DroppedWindow struct {
	WindowStart time.Time `json:"window_start"`
	Reason      string    `json:"reason"`
}

// RejectedFrame records an abnormal frame excluded from pairing.
type RejectedFrame struct {
	Evidence model.EventID      `json:"evidence"`
	Kind     model.EvidenceKind `json:"kind"`
	Source   string             `json:"source"`
	Value    float64            `json:"value"`
	At       time.Time          `json:"at"`
	Reason   string             `json:"reason"`
}

// Curve is the resistance-versus-speed result of one tow. Points are sorted
// by speed. EmptyWindows counts windows with no frames at all; Dropped lists
// windows that held frames of only one kind; Rejected lists abnormal frames.
type Curve struct {
	Run          model.RunID      `json:"run"`
	Revision     model.RevisionID `json:"revision"`
	Window       time.Duration    `json:"window"`
	Points       []Point          `json:"points"`
	EmptyWindows int              `json:"empty_windows"`
	Dropped      []DroppedWindow  `json:"dropped"`
	Rejected     []RejectedFrame  `json:"rejected"`
}

type bucket struct {
	speeds []model.Evidence
	forces []model.Evidence
}

// Analyze builds the curve for one tow. Speed frames must be positive and
// finite, force frames finite; within a window, frames whose robust z-score
// exceeds the limit are rejected as abnormal. A window pairs only when it
// still holds at least one frame of each kind after rejection.
func Analyze(run model.RunID, revision model.RevisionID, speeds, forces []model.Evidence, window time.Duration) Curve {
	if window <= 0 {
		window = DefaultWindow
	}
	if window > MaxWindow {
		window = MaxWindow
	}
	curve := Curve{Run: run, Revision: revision, Window: window, Points: []Point{}, Dropped: []DroppedWindow{}, Rejected: []RejectedFrame{}}
	keptSpeeds := make([]model.Evidence, 0, len(speeds))
	for _, frame := range speeds {
		if math.IsNaN(frame.Value) || math.IsInf(frame.Value, 0) || frame.Value <= 0 {
			curve.Rejected = append(curve.Rejected, reject(frame, "speed must be positive and finite"))
			continue
		}
		keptSpeeds = append(keptSpeeds, frame)
	}
	keptForces := make([]model.Evidence, 0, len(forces))
	for _, frame := range forces {
		if math.IsNaN(frame.Value) || math.IsInf(frame.Value, 0) {
			curve.Rejected = append(curve.Rejected, reject(frame, "force must be finite"))
			continue
		}
		keptForces = append(keptForces, frame)
	}
	if len(keptSpeeds) == 0 || len(keptForces) == 0 {
		return curve
	}
	orderedSpeeds := byTime(keptSpeeds)
	orderedForces := byTime(keptForces)
	anchor := orderedSpeeds[0].RecordedAt
	if orderedForces[0].RecordedAt.Before(anchor) {
		anchor = orderedForces[0].RecordedAt
	}
	buckets := make(map[int]*bucket)
	last := 0
	assign := func(frames []model.Evidence, put func(*bucket, model.Evidence)) {
		for _, frame := range frames {
			index := int(frame.RecordedAt.Sub(anchor) / window)
			slot, ok := buckets[index]
			if !ok {
				slot = &bucket{}
				buckets[index] = slot
			}
			put(slot, frame)
			if index > last {
				last = index
			}
		}
	}
	assign(orderedSpeeds, func(slot *bucket, frame model.Evidence) { slot.speeds = append(slot.speeds, frame) })
	assign(orderedForces, func(slot *bucket, frame model.Evidence) { slot.forces = append(slot.forces, frame) })
	for index := 0; index <= last; index++ {
		slot, ok := buckets[index]
		if !ok {
			curve.EmptyWindows++
			continue
		}
		start := anchor.Add(time.Duration(index) * window)
		slot.speeds = rejectOutliers(slot.speeds, &curve)
		slot.forces = rejectOutliers(slot.forces, &curve)
		if len(slot.speeds) == 0 || len(slot.forces) == 0 {
			reason := "no_speed_frames"
			if len(slot.forces) == 0 {
				reason = "no_force_frames"
			}
			curve.Dropped = append(curve.Dropped, DroppedWindow{WindowStart: start, Reason: reason})
			continue
		}
		curve.Points = append(curve.Points, pair(start, window, slot))
	}
	sort.SliceStable(curve.Points, func(i, j int) bool { return curve.Points[i].Speed < curve.Points[j].Speed })
	return curve
}

func byTime(frames []model.Evidence) []model.Evidence {
	ordered := append([]model.Evidence{}, frames...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].RecordedAt.Before(ordered[j].RecordedAt) })
	return ordered
}

func reject(frame model.Evidence, reason string) RejectedFrame {
	return RejectedFrame{Evidence: frame.ID, Kind: frame.Kind, Source: frame.SourceID, Value: frame.Value, At: frame.RecordedAt, Reason: reason}
}

// rejectOutliers drops frames whose robust z-score 0.6745*|x-med|/MAD exceeds
// the limit. With fewer than four frames, or a zero MAD, there is not enough
// structure to tell an abnormal frame from a genuine step, so all are kept.
func rejectOutliers(frames []model.Evidence, curve *Curve) []model.Evidence {
	if len(frames) < 4 {
		return frames
	}
	values := make([]float64, 0, len(frames))
	for _, frame := range frames {
		values = append(values, frame.Value)
	}
	med := median(values)
	deviations := make([]float64, 0, len(frames))
	for _, frame := range frames {
		deviations = append(deviations, math.Abs(frame.Value-med))
	}
	mad := median(deviations)
	if mad == 0 {
		return frames
	}
	kept := make([]model.Evidence, 0, len(frames))
	for _, frame := range frames {
		if 0.6745*math.Abs(frame.Value-med)/mad > robustZLimit {
			curve.Rejected = append(curve.Rejected, reject(frame, "abnormal frame within window"))
			continue
		}
		kept = append(kept, frame)
	}
	return kept
}

func median(values []float64) float64 {
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

func pair(start time.Time, window time.Duration, slot *bucket) Point {
	point := Point{
		WindowStart: start,
		WindowEnd:   start.Add(window),
		SpeedFrames: len(slot.speeds),
		ForceFrames: len(slot.forces),
		Evidence:    make([]model.EventID, 0, len(slot.speeds)+len(slot.forces)),
	}
	speedValues := make([]float64, 0, len(slot.speeds))
	for _, frame := range slot.speeds {
		speedValues = append(speedValues, frame.Value)
		point.Evidence = append(point.Evidence, frame.ID)
	}
	forceValues := make([]float64, 0, len(slot.forces))
	for _, frame := range slot.forces {
		forceValues = append(forceValues, frame.Value)
		point.Evidence = append(point.Evidence, frame.ID)
	}
	point.Speed, point.SpeedSpread = meanSpread(speedValues)
	point.Force, point.ForceSpread = meanSpread(forceValues)
	// 95% interval of the force mean (normal approximation). A single frame
	// carries no spread information, so the uncertainty stays zero and the
	// frame count marks the point as unverified.
	if point.ForceFrames > 1 {
		point.Uncertainty = 1.96 * point.ForceSpread / math.Sqrt(float64(point.ForceFrames))
	}
	return point
}

// meanSpread returns the mean and the sample (n-1) standard deviation.
func meanSpread(values []float64) (float64, float64) {
	sum := 0.0
	for _, value := range values {
		sum += value
	}
	mean := sum / float64(len(values))
	if len(values) == 1 {
		return mean, 0
	}
	squares := 0.0
	for _, value := range values {
		squares += (value - mean) * (value - mean)
	}
	return mean, math.Sqrt(squares / float64(len(values)-1))
}
