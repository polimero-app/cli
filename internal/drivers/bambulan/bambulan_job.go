package bambulan

import (
	"context"
	"encoding/json"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/devicepath"
	"github.com/polimero-app/cli/internal/driver"
)

// JobPause sends the Bambu pause command and waits for gcode_state=PAUSED.
func (d *Driver) JobPause(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	payload, err := buildJobControlPayload("pause")
	if err != nil {
		return driver.JobActionResult{}, err
	}
	data, err := d.mqttCommand(ctx, p, s, payload, isJobState("PAUSED"))
	if err != nil {
		return driver.JobActionResult{}, err
	}
	return jobActionResultFromReport(data, d)
}

// JobResume sends the Bambu resume command and waits for gcode_state in {PRINTING, PREPARE, RUNNING, SLICING}.
func (d *Driver) JobResume(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	payload, err := buildJobControlPayload("resume")
	if err != nil {
		return driver.JobActionResult{}, err
	}
	data, err := d.mqttCommand(ctx, p, s, payload, isJobState("PRINTING", "PREPARE", "RUNNING", "SLICING"))
	if err != nil {
		return driver.JobActionResult{}, err
	}
	return jobActionResultFromReport(data, d)
}

// JobCancel sends the Bambu stop command and waits for gcode_state in {IDLE, FINISH}.
func (d *Driver) JobCancel(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger) (driver.JobActionResult, error) {
	payload, err := buildJobControlPayload("stop")
	if err != nil {
		return driver.JobActionResult{}, err
	}
	data, err := d.mqttCommand(ctx, p, s, payload, isJobState("IDLE", "FINISH"))
	if err != nil {
		return driver.JobActionResult{}, err
	}
	return jobActionResultFromReport(data, d)
}

// JobStart sends a Bambu project_file or gcode_file command and waits for
// gcode_state in {PRINTING, PREPARE, RUNNING, SLICING}.
func (d *Driver) JobStart(ctx context.Context, p driver.ProfileInput, s driver.SecretsBundle, _ *slog.Logger, devicePath string, opts driver.JobStartOptions) (driver.JobActionResult, error) {
	path, filename, err := parseJobDevicePath(devicePath)
	if err != nil {
		return driver.JobActionResult{}, err
	}

	payload, err := buildJobStartPayload(path, filename, opts)
	if err != nil {
		return driver.JobActionResult{}, err
	}

	data, err := d.mqttCommand(ctx, p, s, payload, isJobState("PRINTING", "PREPARE", "RUNNING", "SLICING"))
	if err != nil {
		return driver.JobActionResult{}, err
	}
	return jobActionResultFromReport(data, d)
}

// buildJobControlPayload constructs a simple Bambu job control command payload.
func buildJobControlPayload(command string) (string, error) {
	type cmd struct {
		SequenceID string `json:"sequence_id"`
		Command    string `json:"command"`
	}
	type payload struct {
		Print cmd `json:"print"`
	}
	b, err := json.Marshal(payload{Print: cmd{SequenceID: nextSequenceID(), Command: command}})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// buildJobStartPayload constructs a Bambu project_file or gcode_file command payload.
func buildJobStartPayload(path, filename string, opts driver.JobStartOptions) (string, error) {
	command := jobCommandForPath(path)
	if command == "project_file" {
		return buildProjectFilePayload(path, filename, opts)
	}
	return buildGcodeFilePayload(path, filename, opts)
}

func buildProjectFilePayload(path, filename string, opts driver.JobStartOptions) (string, error) {
	plateIdx := 0
	if opts.Plate != nil {
		plateIdx = *opts.Plate
	}
	plateNumber := plateIdx
	if plateNumber <= 0 {
		plateNumber = 1
	}

	type projectFileCmd struct {
		SequenceID    string `json:"sequence_id"`
		Command       string `json:"command"`
		Param         string `json:"param"`
		ProjectID     string `json:"project_id"`
		ProfileID     string `json:"profile_id"`
		TaskID        string `json:"task_id"`
		SubtaskID     string `json:"subtask_id"`
		SubtaskName   string `json:"subtask_name"`
		File          string `json:"file"`
		URL           string `json:"url"`
		MD5           string `json:"md5"`
		BedType       string `json:"bed_type"`
		BedLeveling   bool   `json:"bed_leveling"`
		FlowCali      bool   `json:"flow_cali"`
		VibrationCali bool   `json:"vibration_cali"`
		LayerInspect  bool   `json:"layer_inspect"`
		Timelapse     bool   `json:"timelapse"`
		UseAMS        bool   `json:"use_ams"`
		AMSMapping    []int  `json:"ams_mapping"`
		AMSMapping2   []any  `json:"ams_mapping2"`

		AutoBedLeveling  int    `json:"auto_bed_leveling"`
		NozzleOffsetCali int    `json:"nozzle_offset_cali"`
		Cfg              string `json:"cfg"`
		ExtrudeCaliFlag  int    `json:"extrude_cali_flag"`
	}
	type payload struct {
		Print projectFileCmd `json:"print"`
	}

	p := payload{Print: projectFileCmd{
		SequenceID:    nextSequenceID(),
		Command:       "project_file",
		Param:         plateParam(plateNumber),
		ProjectID:     "0",
		ProfileID:     "0",
		TaskID:        "0",
		SubtaskID:     "0",
		SubtaskName:   filename,
		File:          strings.TrimPrefix(path, "/"),
		URL:           "file://" + path,
		MD5:           "",
		BedType:       "auto",
		BedLeveling:   !opts.SkipLeveling,
		FlowCali:      false,
		VibrationCali: false,
		LayerInspect:  false,
		Timelapse:     false,
		UseAMS:        false,
		AMSMapping:    []int{},
		AMSMapping2:   []any{},

		AutoBedLeveling:  0,
		NozzleOffsetCali: 0,
		Cfg:              "0",
		ExtrudeCaliFlag:  0,
	}}

	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func buildGcodeFilePayload(path, filename string, opts driver.JobStartOptions) (string, error) {
	plateIdx := 0
	if opts.Plate != nil {
		plateIdx = *opts.Plate
	}

	type gcodeFileCmd struct {
		SequenceID    string `json:"sequence_id"`
		Command       string `json:"command"`
		Param         string `json:"param"`
		SubtaskName   string `json:"subtask_name,omitempty"`
		Timelapse     bool   `json:"timelapse"`
		BedType       string `json:"bed_type"`
		BedLeveling   bool   `json:"bed_leveling"`
		FlowCali      bool   `json:"flow_cali"`
		VibrationCali bool   `json:"vibration_cali"`
		LayerInspect  bool   `json:"layer_inspect"`
		UseAMS        bool   `json:"use_ams"`
		PlateIdx      int    `json:"plate_idx"`
	}
	type payload struct {
		Print gcodeFileCmd `json:"print"`
	}

	p := payload{Print: gcodeFileCmd{
		SequenceID:    nextSequenceID(),
		Command:       "gcode_file",
		Param:         path,
		SubtaskName:   filename,
		Timelapse:     false,
		BedType:       "auto",
		BedLeveling:   !opts.SkipLeveling,
		FlowCali:      false,
		VibrationCali: false,
		LayerInspect:  false,
		UseAMS:        false,
		PlateIdx:      plateIdx,
	}}

	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func plateParam(plate int) string {
	return "Metadata/plate_" + strconv.Itoa(plate) + ".gcode"
}

// parseJobDevicePath splits "sdcard:/models/cube.3mf" into path="/models/cube.3mf"
// and filename="cube.3mf". The root prefix is stripped.
func parseJobDevicePath(raw string) (remotePath string, filename string, err error) {
	dp, err := devicepath.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if dp.Root != ftpRootName {
		return "", "", apperr.Newf(2, "unknown root %q", dp.Root)
	}
	if dp.BaseName() == "" {
		return "", "", apperr.New(2, "device path must name a file")
	}
	ext := strings.ToLower(path.Ext(dp.Path))
	if ext != ".3mf" && ext != ".gcode" {
		return "", "", apperr.Newf(2, "unsupported print file extension %q", ext)
	}
	return dp.Path, path.Base(dp.Path), nil
}

// jobCommandForPath returns "project_file" for .3mf files and "gcode_file" otherwise.
func jobCommandForPath(remotePath string) string {
	if strings.EqualFold(path.Ext(remotePath), ".3mf") {
		return "project_file"
	}
	return "gcode_file"
}

// isJobState returns a predicate that is true when the report is a full pushall
// with gcode_state in the given set.
func isJobState(states ...string) func([]byte) bool {
	stateSet := make(map[string]bool, len(states))
	for _, s := range states {
		stateSet[s] = true
	}
	return func(data []byte) bool {
		var rep struct {
			Print *struct {
				GcodeState *string `json:"gcode_state"`
			} `json:"print"`
		}
		if err := json.Unmarshal(data, &rep); err != nil || rep.Print == nil || rep.Print.GcodeState == nil {
			return false
		}
		return stateSet[*rep.Print.GcodeState]
	}
}

// jobActionResultFromReport parses a Bambu pushall report into a JobActionResult.
func jobActionResultFromReport(data []byte, d *Driver) (driver.JobActionResult, error) {
	status, err := parseReport(data)
	if err != nil {
		return driver.JobActionResult{}, err
	}
	warnings := status.Warnings
	if warnings == nil {
		warnings = []driver.StatusWarning{}
	}
	return driver.JobActionResult{
		State:        status.State,
		Warnings:     warnings,
		Capabilities: d.Capabilities(),
	}, nil
}
