package bambulan

import (
	"testing"
)

func TestParseSSDPResponse_ValidMSearch(t *testing.T) {
	msg := "HTTP/1.1 200 OK\r\n" +
		"LOCATION: http://192.0.2.10/\r\n" +
		"ST: urn:bambulab-com:device:3dprinter:1\r\n" +
		"USN: uuid:01P09A310200XXX::urn:bambulab-com:device:3dprinter:1\r\n" +
		"DevModel.bambu.com: P1S\r\n" +
		"DevName.bambu.com: My P1S\r\n" +
		"\r\n"

	e := parseSSDPResponse(msg, "192.0.2.10")
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if e.Host != "192.0.2.10" {
		t.Errorf("Host = %q, want %q", e.Host, "192.0.2.10")
	}
	if e.Port != 8883 {
		t.Errorf("Port = %d, want 8883", e.Port)
	}
	if got := txtValue(e.Text, "sn"); got != "01P09A310200XXX" {
		t.Errorf("sn = %q, want %q", got, "01P09A310200XXX")
	}
	if got := txtValue(e.Text, "dev_model_name"); got != "P1S" {
		t.Errorf("dev_model_name = %q, want %q", got, "P1S")
	}
	if got := txtValue(e.Text, "dev_name"); got != "My P1S" {
		t.Errorf("dev_name = %q, want %q", got, "My P1S")
	}
}

func TestParseSSDPResponse_Notify(t *testing.T) {
	msg := "NOTIFY * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"NT: urn:bambulab-com:device:3dprinter:1\r\n" +
		"NTS: ssdp:alive\r\n" +
		"LOCATION: http://192.0.2.11/\r\n" +
		"USN: uuid:01S09C450100XXX::urn:bambulab-com:device:3dprinter:1\r\n" +
		"DevModel.bambu.com: X1C\r\n" +
		"DevName.bambu.com: My X1C\r\n" +
		"\r\n"

	e := parseSSDPResponse(msg, "192.0.2.11")
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if got := txtValue(e.Text, "sn"); got != "01S09C450100XXX" {
		t.Errorf("sn = %q, want %q", got, "01S09C450100XXX")
	}
	if got := txtValue(e.Text, "dev_model_name"); got != "X1C" {
		t.Errorf("dev_model_name = %q, want %q", got, "X1C")
	}
}

func TestParseSSDPResponse_WrongST_ReturnsNil(t *testing.T) {
	msg := "HTTP/1.1 200 OK\r\n" +
		"ST: urn:schemas-upnp-org:device:Basic:1\r\n" +
		"\r\n"
	if e := parseSSDPResponse(msg, "192.0.2.1"); e != nil {
		t.Errorf("expected nil for non-Bambu ST, got %+v", e)
	}
}

func TestParseSSDPResponse_FallbackToSourceIP(t *testing.T) {
	msg := "HTTP/1.1 200 OK\r\n" +
		"ST: urn:bambulab-com:device:3dprinter:1\r\n" +
		"USN: uuid:SNX::urn:bambulab-com:device:3dprinter:1\r\n" +
		"\r\n" // no LOCATION header
	e := parseSSDPResponse(msg, "10.0.0.5")
	if e == nil {
		t.Fatal("expected non-nil entry")
	}
	if e.Host != "10.0.0.5" {
		t.Errorf("Host = %q, want %q", e.Host, "10.0.0.5")
	}
}

func TestExtractIPFromURL(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"http://192.0.2.10/", "192.0.2.10"},
		{"http://192.0.2.10:80/path", "192.0.2.10"},
		{"http://192.0.2.10", "192.0.2.10"},
		{"not-a-url", "not-a-url"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractIPFromURL(tt.url)
		if got != tt.want {
			t.Errorf("extractIPFromURL(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

func TestExtractSerialFromUSN(t *testing.T) {
	tests := []struct {
		usn  string
		want string
	}{
		{"uuid:01P09A310200XXX::urn:bambulab-com:device:3dprinter:1", "01P09A310200XXX"},
		{"uuid:SERIAL", "SERIAL"},
		{"not-uuid", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractSerialFromUSN(tt.usn)
		if got != tt.want {
			t.Errorf("extractSerialFromUSN(%q) = %q, want %q", tt.usn, got, tt.want)
		}
	}
}
