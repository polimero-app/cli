package bambulan

import (
	"context"
	"testing"

	"github.com/polimero-app/cli/internal/driver"
)

func stubBrowse(entries ...*mdnsEntry) func(ctx context.Context, service string) (<-chan *mdnsEntry, error) {
	return func(_ context.Context, _ string) (<-chan *mdnsEntry, error) {
		ch := make(chan *mdnsEntry, len(entries))
		for _, e := range entries {
			ch <- e
		}
		close(ch)
		return ch, nil
	}
}

func TestDiscover_ReturnsParsedPrinters(t *testing.T) {
	d := &Driver{
		browse: stubBrowse(
			&mdnsEntry{
				Host: "192.0.2.10",
				Port: 8883,
				Text: []string{"sn=01S09C450100XXX", "dev_model_name=X1C", "dev_name=My X1C"},
			},
			&mdnsEntry{
				Host: "192.0.2.11",
				Port: 8883,
				Text: []string{"sn=01P09A310200XXX", "dev_model_name=P1S", "dev_name=P1S"},
			},
		),
	}
	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(printers) != 2 {
		t.Fatalf("expected 2 printers, got %d", len(printers))
	}

	want := []driver.DiscoveredPrinter{
		{Host: "192.0.2.10", Port: 8883, Serial: "01S09C450100XXX", Model: "X1C", Name: "My X1C", Driver: "bambu-lan"},
		{Host: "192.0.2.11", Port: 8883, Serial: "01P09A310200XXX", Model: "P1S", Name: "P1S", Driver: "bambu-lan"},
	}
	for i, got := range printers {
		if got != want[i] {
			t.Errorf("printers[%d] = %+v, want %+v", i, got, want[i])
		}
	}
}

func TestDiscover_EmptyResult_NilFree(t *testing.T) {
	d := &Driver{browse: stubBrowse()}
	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if printers == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(printers) != 0 {
		t.Errorf("expected 0 printers, got %d", len(printers))
	}
}

func TestDiscover_MissingTxtFields_EmptyStrings(t *testing.T) {
	d := &Driver{
		browse: stubBrowse(&mdnsEntry{Host: "10.0.0.1", Port: 8883, Text: nil}),
	}
	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(printers) != 1 {
		t.Fatalf("expected 1 printer, got %d", len(printers))
	}
	p := printers[0]
	if p.Serial != "" || p.Model != "" || p.Name != "" {
		t.Errorf("expected empty strings for missing TXT fields, got serial=%q model=%q name=%q", p.Serial, p.Model, p.Name)
	}
	if p.Driver != "bambu-lan" {
		t.Errorf("Driver = %q, want %q", p.Driver, "bambu-lan")
	}
}

func TestTxtValue_Found(t *testing.T) {
	records := []string{"sn=ABC123", "dev_model_name=X1C", "dev_name=My Printer"}
	if got := txtValue(records, "sn"); got != "ABC123" {
		t.Errorf("txtValue sn = %q, want %q", got, "ABC123")
	}
	if got := txtValue(records, "dev_model_name"); got != "X1C" {
		t.Errorf("txtValue dev_model_name = %q, want %q", got, "X1C")
	}
}

func TestTxtValue_Missing(t *testing.T) {
	if got := txtValue(nil, "sn"); got != "" {
		t.Errorf("txtValue on nil = %q, want empty", got)
	}
	if got := txtValue([]string{"other=val"}, "sn"); got != "" {
		t.Errorf("txtValue missing key = %q, want empty", got)
	}
}
