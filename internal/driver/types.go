package driver

import "time"

// Capabilities describes which optional operations a driver supports.
type Capabilities struct {
	Status           bool `json:"status"`
	Discovery        bool `json:"discovery"`
	JobUpload        bool `json:"jobUpload"`
	JobStart         bool `json:"jobStart"`
	JobPause         bool `json:"jobPause"`
	JobCancel        bool `json:"jobCancel"`
	TemperatureRead  bool `json:"temperatureRead"`
	TemperatureWrite bool `json:"temperatureWrite"`
	MotionControl    bool `json:"motionControl"`
}

// SecretsBundle carries runtime secrets for a printer connection.
type SecretsBundle struct {
	AccessCode     string // LAN access code
	TLSFingerprint string // "sha256:<hex>"; empty when insecure
}

// ProfileInput carries non-secret profile fields needed for a driver call.
type ProfileInput struct {
	Name     string
	Driver   string
	Host     string
	Serial   string
	Timeout  time.Duration
	Insecure bool
}

// Temperature holds a sensor reading and optional target.
type Temperature struct {
	CurrentCelsius float64  `json:"currentCelsius"`
	TargetCelsius  *float64 `json:"targetCelsius,omitempty"`
}

// Temperatures groups per-sensor temperature readings.
type Temperatures struct {
	Nozzle  *Temperature `json:"nozzle,omitempty"`
	Bed     *Temperature `json:"bed,omitempty"`
	Chamber *Temperature `json:"chamber,omitempty"`
}

// Job describes the currently active print job.
type Job struct {
	Name string `json:"name"`
}

// Progress describes how far through a print job the printer is.
type Progress struct {
	Percent      int  `json:"percent"`
	CurrentLayer *int `json:"currentLayer,omitempty"`
	TotalLayers  *int `json:"totalLayers,omitempty"`
}

// StatusError describes a hardware or firmware error reported by the printer.
type StatusError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusWarning describes a non-fatal condition in the status result.
type StatusWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StatusResult is the portable representation of printer state returned by Driver.Status.
// Errors and Warnings are always non-nil slices (serialize as [] not null).
type StatusResult struct {
	State        string          `json:"state"`
	Temperatures *Temperatures   `json:"temperatures"`
	Job          *Job            `json:"job"`
	Progress     *Progress       `json:"progress"`
	Errors       []StatusError   `json:"errors"`
	Warnings     []StatusWarning `json:"warnings"`
	Capabilities Capabilities    `json:"capabilities"`
}
