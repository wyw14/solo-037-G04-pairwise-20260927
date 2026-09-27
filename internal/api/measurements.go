package api

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/wyw14/solo-037/internal/model"
	"github.com/wyw14/solo-037/internal/resistance"
	"github.com/wyw14/solo-037/internal/service"
)

type MeasurementService interface {
	CreateMeasuredRun(name, modelName, section, carriage string) (service.MeasuredRun, error)
	RecordMeasurement(model.RunID, service.MeasurementInput) (model.Evidence, error)
	MeasuredRuns() []service.MeasuredRun
	MeasuredRun(model.RunID) (service.MeasuredRun, bool)
	AnalyzeMeasuredRun(model.RunID) (service.MeasurementAnalysis, error)
	AnalyzeResistance(model.RunID, resistance.Params) (service.ResistanceAnalysis, error)
}

type createMeasuredRunRequest struct {
	Name      string `json:"name"`
	ModelName string `json:"model_name"`
	Section   string `json:"section"`
	Carriage  string `json:"carriage"`
}

func decodeMeasurementBody(request *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return nil
}

func (r *Router) measuredRuns(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, r.dependencies.MeasuredRuns())
}

func (r *Router) createMeasuredRun(w http.ResponseWriter, request *http.Request) {
	var input createMeasuredRunRequest
	if err := decodeMeasurementBody(request, &input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, err := r.dependencies.CreateMeasuredRun(input.Name, input.ModelName, input.Section, input.Carriage)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, value)
}

func (r *Router) measuredRun(w http.ResponseWriter, request *http.Request) {
	value, ok := r.dependencies.MeasuredRun(model.RunID(chi.URLParam(request, "id")))
	if !ok {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	writeJSON(w, value)
}

func (r *Router) recordMeasurement(w http.ResponseWriter, request *http.Request) {
	var input service.MeasurementInput
	if err := decodeMeasurementBody(request, &input); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, err := r.dependencies.RecordMeasurement(model.RunID(chi.URLParam(request, "id")), input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, value)
}

func (r *Router) measurementAnalysis(w http.ResponseWriter, request *http.Request) {
	value, err := r.dependencies.AnalyzeMeasuredRun(model.RunID(chi.URLParam(request, "id")))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, value)
}

func parseResistanceParams(request *http.Request) (resistance.Params, error) {
	var params resistance.Params
	query := request.URL.Query()
	if raw := query.Get("window_ms"); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ms <= 0 {
			return params, errors.New("window_ms must be a positive integer")
		}
		params.Window = time.Duration(ms) * time.Millisecond
	}
	if raw := query.Get("outlier_sigma"); raw != "" {
		sigma, err := strconv.ParseFloat(raw, 64)
		if err != nil || sigma <= 0 || math.IsNaN(sigma) || math.IsInf(sigma, 0) {
			return params, errors.New("outlier_sigma must be a positive finite number")
		}
		params.OutlierSigma = sigma
	}
	return params, nil
}

func (r *Router) measurementResistance(w http.ResponseWriter, request *http.Request) {
	params, err := parseResistanceParams(request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	value, err := r.dependencies.AnalyzeResistance(model.RunID(chi.URLParam(request, "id")), params)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, value)
}
