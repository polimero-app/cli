package driver

import (
	"io"
	"time"
)

const tlsFingerprintPrefix = "sha256:"

// ValidTLSFingerprint reports whether fingerprint matches the pinned TLS
// fingerprint format used by Polimero: "sha256:" plus 64 lowercase hex chars.
func ValidTLSFingerprint(fingerprint string) bool {
	if len(fingerprint) != len(tlsFingerprintPrefix)+64 {
		return false
	}
	if fingerprint[:len(tlsFingerprintPrefix)] != tlsFingerprintPrefix {
		return false
	}
	for _, c := range fingerprint[len(tlsFingerprintPrefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Capabilities describes which optional operations a driver supports.
type Capabilities struct {
	Status           bool `json:"status"`
	Discovery        bool `json:"discovery"`
	CameraStream     bool `json:"cameraStream"`
	CameraSnapshot   bool `json:"cameraSnapshot"`
	FileList         bool `json:"fileList"`
	FileDownload     bool `json:"fileDownload"`
	FileUpload       bool `json:"fileUpload"`
	JobUpload        bool `json:"jobUpload"`
	JobStart         bool `json:"jobStart"`
	JobPause         bool `json:"jobPause"`
	JobResume        bool `json:"jobResume"`
	JobCancel        bool `json:"jobCancel"`
	TemperatureRead  bool `json:"temperatureRead"`
	TemperatureWrite bool `json:"temperatureWrite"`
	MotionControl    bool `json:"motionControl"`
	TLSRefresh       bool `json:"tlsRefresh"`
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
	Name string  `json:"name"`
	ID   *string `json:"id,omitempty"` // printer-assigned or synthetic ID
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

// DiscoveredPrinter holds the information about a printer found via network discovery.
type DiscoveredPrinter struct {
	Host   string // IP address
	Port   int    // service port from mDNS SRV record (e.g. 8883)
	Serial string // serial number from service metadata; empty if unavailable
	Model  string // model identifier from service metadata; empty if unavailable
	Name   string // friendly name from service metadata; empty if unavailable
	Driver string // driver name that discovered this printer (e.g. "bambu-lan")
}

// Fans holds named fan speed readings as integer percentages (0-100).
type Fans map[string]int

// TimeEstimates holds timing information for the current print job.
type TimeEstimates struct {
	ElapsedSeconds   int  `json:"elapsedSeconds"`
	RemainingSeconds *int `json:"remainingSeconds,omitempty"`
	TotalSeconds     *int `json:"totalSeconds,omitempty"`
}

// Wifi holds network signal information.
type Wifi struct {
	SignalDbm int `json:"signalDbm"`
}

// Lights holds lighting states keyed by light name.
// Values are "on", "off", or a brightness level string.
type Lights map[string]string

// PrintMeta holds metadata about the current print file.
type PrintMeta struct {
	FileName       string   `json:"fileName"`
	FileSize       *int     `json:"fileSize,omitempty"`
	NozzleDiameter *float64 `json:"nozzleDiameter,omitempty"`
	BedType        *string  `json:"bedType,omitempty"`
}

// Timelapse holds timelapse recording status.
type Timelapse struct {
	Recording bool  `json:"recording"`
	Progress  *int  `json:"progress,omitempty"`
	Ready     *bool `json:"ready,omitempty"`
}

// GcodePosition holds g-code execution position.
type GcodePosition struct {
	ZMm         float64 `json:"zMm"`
	CurrentLine int     `json:"currentLine"`
	TotalLines  int     `json:"totalLines"`
}

// AMSTray holds data for a single AMS tray slot.
type AMSTray struct {
	Slot             int     `json:"slot"`
	FilamentType     *string `json:"filamentType,omitempty"`
	Color            *string `json:"color,omitempty"`
	RemainingPercent *int    `json:"remainingPercent,omitempty"`
	NozzleTempMin    *int    `json:"nozzleTempMin,omitempty"`
	NozzleTempMax    *int    `json:"nozzleTempMax,omitempty"`
}

// AMSUnit holds data for a single AMS unit.
type AMSUnit struct {
	ID            int       `json:"id"`
	HumidityRange *string   `json:"humidityRange,omitempty"`
	HumidityLevel *string   `json:"humidityLevel,omitempty"`
	Temperature   *float64  `json:"temperature,omitempty"`
	Trays         []AMSTray `json:"trays"`
}

// AMSData holds the full AMS status.
type AMSData struct {
	Units []AMSUnit `json:"units"`
}

// BambuExtension holds Bambu-specific extension data.
type BambuExtension struct {
	AMS         *AMSData `json:"ams,omitempty"`
	SDCardState *string  `json:"sdCardState,omitempty"` // "none", "normal", "abnormal", "readonly"
	EMMCStorage *bool    `json:"emmcStorage,omitempty"` // true if internal eMMC is supported
	ReportedIP  *string  `json:"reportedIP,omitempty"`  // printer-reported IP (may differ from connection IP)
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

	// Extended fields (populated when detailed status is requested).
	Fans            Fans           `json:"fans,omitempty"`
	TimeEstimates   *TimeEstimates `json:"timeEstimates,omitempty"`
	SpeedLevel      *string        `json:"speedLevel,omitempty"`
	Wifi            *Wifi          `json:"wifi,omitempty"`
	Lights          Lights         `json:"lights,omitempty"`
	PrintMeta       *PrintMeta     `json:"printMeta,omitempty"`
	Stage           *string        `json:"stage,omitempty"`
	Timelapse       *Timelapse     `json:"timelapse,omitempty"`
	GcodePosition   *GcodePosition `json:"gcodePosition,omitempty"`
	FirmwareVersion *string        `json:"firmwareVersion,omitempty"`
	Extensions      map[string]any `json:"extensions,omitempty"`
}

// CameraFormat identifies the video encoding of a camera stream.
type CameraFormat string

const (
	CameraFormatMJPEG CameraFormat = "mjpeg"
	CameraFormatH264  CameraFormat = "h264"
)

// CameraStreamResult is returned by Driver.CameraStream with an open stream.
type CameraStreamResult struct {
	Format       CameraFormat
	Stream       io.ReadCloser
	Capabilities Capabilities
}

// CameraSnapshotResult is returned by Driver.CameraSnapshot with JPEG image data.
type CameraSnapshotResult struct {
	Data         []byte
	Protocol     string
	Capabilities Capabilities
}

// JobStartOptions carries optional parameters for JobDriver.JobStart.
type JobStartOptions struct {
	Plate        *int // nil means driver/printer default (e.g. first or only plate)
	SkipLeveling bool
}

// JobActionResult is returned by job control operations.
// State is the confirmed resulting portable state.
type JobActionResult struct {
	State        string
	Warnings     []StatusWarning
	Capabilities Capabilities
}

// TemperatureTargets describes the heater targets to set.
// A nil pointer means "leave that heater unchanged".
// A value of 0 turns the heater off.
type TemperatureTargets struct {
	NozzleCelsius  *float64
	BedCelsius     *float64
	ChamberCelsius *float64
}

// TemperatureResult is returned by TemperatureDriver.TemperatureSet.
// Targets holds the acknowledged values, as confirmed by the printer.
type TemperatureResult struct {
	Targets      TemperatureTargets
	Warnings     []StatusWarning
	Capabilities Capabilities
}

// Axis identifies a printer motion axis.
type Axis string

const (
	AxisX Axis = "x"
	AxisY Axis = "y"
	AxisZ Axis = "z"
)

// JogDelta describes a relative motion command.
// Nil axis fields mean "do not move that axis".
type JogDelta struct {
	XMillimeters     *float64
	YMillimeters     *float64
	ZMillimeters     *float64
	FeedrateMmPerMin int
}

// MotionState describes how far a driver can truthfully confirm a motion command.
type MotionState string

const (
	MotionStateAccepted MotionState = "accepted"
	MotionStateComplete MotionState = "complete"
)

// MotionResult is returned by motion control operations.
type MotionResult struct {
	State        MotionState
	Warnings     []StatusWarning
	Capabilities Capabilities
}
