// Package resistance derives a resistance-versus-speed curve from operator
// supplied speed and force frames of a single tow run. Frames are paired
// inside fixed time windows, abnormal frames are rejected, and every output
// point carries the evidence identifiers it was computed from so the curve
// stays traceable back to the archived measurements.
package resistance

import (
	"math"
	"sort"
	"time"

	"github.com/wyw14/solo-037/internal/model"
)

const (
	// DefaultWindow is the pairing window used when the caller does not
	// supply one.
	DefaultWindow = time.Second
	// DefaultOutlierSigma is the modified z-score rejection threshold.
	DefaultOutlierSigma = 3.5

	minimumWindow = 50 * time.Millisecond
	maximumWindow = time.Minute
	// intervalCoverage scales the standard error to a 95% interval.
	intervalCoverage = 1.96
)

// Rejection reasons reported in RejectedFrame.
const (
	ReasonNonFinite        = "non_finite"
	ReasonNonPositiveSpeed = "non_positive_speed"
	ReasonOutlier          = "outlier"
)

// Dropped window reasons reported in DroppedWindow.
const (
	DropMissingSpeed = "missing_speed"
	DropMissingForce = "missing_force"
)

// Params controls windowing and outlier rejection. Zero fields fall back to
// the defaults above.
type Params struct {
	Window       time.Duration
	OutlierSigma float64
}

// Parameters echoes the effective analysis settings in a Result.
type Parameters struct {
	WindowMS     int64   `json:"window_ms"`
	OutlierSigma float64 `json:"outlier_sigma"`
}

// Point is one paired window of the resistance curve.
type Point struct {
	WindowStart            time.Time       `json:"window_start"`
	WindowEnd              time.Time       `json:"window_end"`
	SpeedMean              float64         `json:"speed_mean"`
	SpeedStddev            float64         `json:"speed_stddev"`
	SpeedCount             int             `json:"speed_count"`
	ForceMean              float64         `json:"force_mean"`
	ForceStddev            float64         `json:"force_stddev"`
	ForceCount             int             `json:"force_count"`
	ForceStandardError     float64         `json:"force_standard_error"`
	ForceIntervalHalfWidth float64         `json:"force_interval_half_width"`
	ForceLower             float64         `json:"force_lower"`
	ForceUpper             float64         `json:"force_upper"`
	SpeedEvidence          []model.EventID `json:"speed_evidence"`
	ForceEvidence          []model.EventID `json:"force_evidence"`
}

// RejectedFrame records an abnormal frame removed before pairing.
type RejectedFrame struct {
	ID     model.EventID      `json:"id"`
	Kind   model.EvidenceKind `json:"kind"`
	Reason string             `json:"reason"`
}

// DroppedWindow records a window that held frames but could not be paired.
type DroppedWindow struct {
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`
	Reason      string    `json:"reason"`
}

// Result is the traceable resistance-versus-speed analysis of one run.
type Result struct {
	Parameters     Parameters      `json:"parameters"`
	Points         []Point         `json:"points"`
	RejectedFrames []RejectedFrame `json:"rejected_frames"`
	DroppedWindows []DroppedWindow `json:"dropped_windows"`
	// EmptyWindows counts grid slots between the first and last frame that
	// held neither speed nor force frames; they are excluded from the curve.
	EmptyWindows int `json:"empty_windows"`
}

// NormalizeParams fills defaults and clamps the window to a sane range.
func NormalizeParams(params Params) Params {
	if params.Window <= 0 {
		params.Window = DefaultWindow
	}
	if params.Window < minimumWindow {
		params.Window = minimumWindow
	}
	if params.Window > maximumWindow {
		params.Window = maximumWindow
	}
	if params.OutlierSigma <= 0 || math.IsNaN(params.OutlierSigma) || math.IsInf(params.OutlierSigma, 0) {
		params.OutlierSigma = DefaultOutlierSigma
	}
	return params
}

// Analyze pairs speed and force frames of one run into a resistance curve.
// Evidence of other kinds is ignored. The result is deterministic for a
// given evidence set.
func Analyze(evidence []model.Evidence, params Params) Result {
	params = NormalizeParams(params)
	result := Result{
		Parameters: Parameters{
			WindowMS:     params.Window.Milliseconds(),
			OutlierSigma: params.OutlierSigma,
		},
		Points:         []Point{},
		RejectedFrames: []RejectedFrame{},
		DroppedWindows: []DroppedWindow{},
	}
	speeds := framesOfKind(evidence, model.EvidenceSpeed)
	forces := framesOfKind(evidence, model.EvidenceForce)
	if len(speeds) == 0 && len(forces) == 0 {
		return result
	}
	anchor := firstTimestamp(speeds, forces)
	speedWindows := bucketize(speeds, anchor, params.Window)
	forceWindows := bucketize(forces, anchor, params.Window)

	indices := make([]int64, 0, len(speedWindows)+len(forceWindows))
	occupied := make(map[int64]struct{}, len(speedWindows)+len(forceWindows))
	for index := range speedWindows {
		occupied[index] = struct{}{}
	}
	for index := range forceWindows {
		occupied[index] = struct{}{}
	}
	var lowest, highest int64
	first := true
	for index := range occupied {
		if first || index < lowest {
			lowest = index
		}
		if first || index > highest {
			highest = index
		}
		first = false
		indices = append(indices, index)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })
	result.EmptyWindows = int(highest-lowest+1) - len(occupied)

	for _, index := range indices {
		start := anchor.Add(time.Duration(index) * params.Window)
		end := start.Add(params.Window)
		keptSpeeds := filterChannel(speedWindows[index], model.EvidenceSpeed, params.OutlierSigma, &result)
		keptForces := filterChannel(forceWindows[index], model.EvidenceForce, params.OutlierSigma, &result)
		if len(keptSpeeds) == 0 || len(keptForces) == 0 {
			reason := DropMissingSpeed
			if len(keptSpeeds) > 0 {
				reason = DropMissingForce
			}
			result.DroppedWindows = append(result.DroppedWindows, DroppedWindow{WindowStart: start, WindowEnd: end, Reason: reason})
			continue
		}
		speedMean, speedStddev := statistics(keptSpeeds)
		forceMean, forceStddev := statistics(keptForces)
		standardError := forceStddev / math.Sqrt(float64(len(keptForces)))
		halfWidth := intervalCoverage * standardError
		result.Points = append(result.Points, Point{
			WindowStart:            start,
			WindowEnd:              end,
			SpeedMean:              speedMean,
			SpeedStddev:            speedStddev,
			SpeedCount:             len(keptSpeeds),
			ForceMean:              forceMean,
			ForceStddev:            forceStddev,
			ForceCount:             len(keptForces),
			ForceStandardError:     standardError,
			ForceIntervalHalfWidth: halfWidth,
			ForceLower:             forceMean - halfWidth,
			ForceUpper:             forceMean + halfWidth,
			SpeedEvidence:          evidenceIDs(keptSpeeds),
			ForceEvidence:          evidenceIDs(keptForces),
		})
	}
	sort.SliceStable(result.Points, func(i, j int) bool {
		return result.Points[i].SpeedMean < result.Points[j].SpeedMean
	})
	return result
}

func framesOfKind(evidence []model.Evidence, kind model.EvidenceKind) []model.Evidence {
	frames := make([]model.Evidence, 0, len(evidence))
	for _, sample := range evidence {
		if sample.Kind == kind {
			frames = append(frames, sample)
		}
	}
	return frames
}

func firstTimestamp(speeds, forces []model.Evidence) time.Time {
	anchor := time.Time{}
	for _, sample := range speeds {
		if anchor.IsZero() || sample.RecordedAt.Before(anchor) {
			anchor = sample.RecordedAt
		}
	}
	for _, sample := range forces {
		if anchor.IsZero() || sample.RecordedAt.Before(anchor) {
			anchor = sample.RecordedAt
		}
	}
	return anchor
}

func bucketize(frames []model.Evidence, anchor time.Time, window time.Duration) map[int64][]model.Evidence {
	buckets := make(map[int64][]model.Evidence)
	for _, frame := range frames {
		index := int64(frame.RecordedAt.Sub(anchor) / window)
		buckets[index] = append(buckets[index], frame)
	}
	return buckets
}

// filterChannel removes abnormal frames of one window and records every
// rejection on the result for traceability.
func filterChannel(frames []model.Evidence, kind model.EvidenceKind, sigma float64, result *Result) []model.Evidence {
	kept := make([]model.Evidence, 0, len(frames))
	for _, frame := range frames {
		switch {
		case math.IsNaN(frame.Value) || math.IsInf(frame.Value, 0):
			result.RejectedFrames = append(result.RejectedFrames, RejectedFrame{ID: frame.ID, Kind: kind, Reason: ReasonNonFinite})
		case kind == model.EvidenceSpeed && frame.Value <= 0:
			result.RejectedFrames = append(result.RejectedFrames, RejectedFrame{ID: frame.ID, Kind: kind, Reason: ReasonNonPositiveSpeed})
		default:
			kept = append(kept, frame)
		}
	}
	if len(kept) < 2 {
		return kept
	}
	center := median(kept)
	deviations := make([]float64, len(kept))
	for index, frame := range kept {
		deviations[index] = math.Abs(frame.Value - center)
	}
	// Modified z-score scale: 1.4826*MAD estimates sigma for normal data.
	// Quantized sensors often yield MAD == 0; fall back to the mean absolute
	// deviation (1.2533*MeanAD estimates sigma) before giving up on rejection.
	scale := 1.4826 * medianValues(deviations)
	if scale == 0 {
		sum := 0.0
		for _, deviation := range deviations {
			sum += deviation
		}
		scale = 1.2533 * sum / float64(len(deviations))
	}
	if scale == 0 {
		return kept
	}
	survivors := make([]model.Evidence, 0, len(kept))
	for _, frame := range kept {
		if math.Abs(frame.Value-center) > sigma*scale {
			result.RejectedFrames = append(result.RejectedFrames, RejectedFrame{ID: frame.ID, Kind: kind, Reason: ReasonOutlier})
		} else {
			survivors = append(survivors, frame)
		}
	}
	return survivors
}

func median(frames []model.Evidence) float64 {
	values := make([]float64, len(frames))
	for index, frame := range frames {
		values[index] = frame.Value
	}
	return medianValues(values)
}

func medianValues(values []float64) float64 {
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}

// statistics returns the mean and population standard deviation so a single
// frame yields a defined (zero) spread.
func statistics(frames []model.Evidence) (float64, float64) {
	sum := 0.0
	for _, frame := range frames {
		sum += frame.Value
	}
	mean := sum / float64(len(frames))
	squared := 0.0
	for _, frame := range frames {
		delta := frame.Value - mean
		squared += delta * delta
	}
	return mean, math.Sqrt(squared / float64(len(frames)))
}

func evidenceIDs(frames []model.Evidence) []model.EventID {
	ids := make([]model.EventID, 0, len(frames))
	for _, frame := range frames {
		ids = append(ids, frame.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
