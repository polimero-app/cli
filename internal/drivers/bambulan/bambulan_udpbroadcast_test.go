package bambulan

import (
	"testing"
)

func TestParseUDPBroadcast_ValidJSON(t *testing.T) {
	data := []byte(`{"dev_name":"My P1S","sn":"01P09A310200XXX","ip":"192.0.2.10","dev_product_name":"P1S"}`)
	e := parseUDPBroadcast(data, "10.0.0.1")
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if e.Host != "192.0.2.10" {
		t.Errorf("Host = %q, want %q (should prefer ip field)", e.Host, "192.0.2.10")
	}
	if e.Port != 8883 {
		t.Errorf("Port = %d, want 8883", e.Port)
	}
	if got := txtValue(e.Text, "sn"); got != "01P09A310200XXX" {
		t.Errorf("sn = %q, want %q", got, "01P09A310200XXX")
	}
	if got := txtValue(e.Text, "dev_name"); got != "My P1S" {
		t.Errorf("dev_name = %q, want %q", got, "My P1S")
	}
	if got := txtValue(e.Text, "dev_model_name"); got != "P1S" {
		t.Errorf("dev_model_name = %q, want %q", got, "P1S")
	}
}

func TestParseUDPBroadcast_FallbackToSourceIP(t *testing.T) {
	data := []byte(`{"dev_name":"X1C","sn":"01S09C450100XXX"}`) // no "ip" field
	e := parseUDPBroadcast(data, "10.0.0.2")
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if e.Host != "10.0.0.2" {
		t.Errorf("Host = %q, want %q (should fall back to source IP)", e.Host, "10.0.0.2")
	}
}

func TestParseUDPBroadcast_InvalidJSON_ReturnsNil(t *testing.T) {
	if e := parseUDPBroadcast([]byte("not json"), "10.0.0.1"); e != nil {
		t.Errorf("expected nil for invalid JSON, got %+v", e)
	}
}

func TestParseUDPBroadcast_EmptyPayload_ReturnsNil(t *testing.T) {
	if e := parseUDPBroadcast([]byte(""), "10.0.0.1"); e != nil {
		t.Errorf("expected nil for empty payload, got %+v", e)
	}
}

func TestParseUDPBroadcast_MissingOptionalFields(t *testing.T) {
	data := []byte(`{"sn":"01S09C450100XXX"}`)
	e := parseUDPBroadcast(data, "10.0.0.3")
	if e == nil {
		t.Fatal("expected non-nil entry when only sn is present")
	}
	if txtValue(e.Text, "dev_name") != "" {
		t.Error("expected empty dev_name when field absent")
	}
	if txtValue(e.Text, "dev_model_name") != "" {
		t.Error("expected empty dev_model_name when field absent")
	}
}
