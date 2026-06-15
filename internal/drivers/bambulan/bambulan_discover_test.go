package bambulan

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/polimero-app/cli/internal/apperr"
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

// emptyBrowseSSDP returns a stub that immediately closes (no results, no error).
func emptyBrowseSSDP() func(ctx context.Context) (<-chan *mdnsEntry, error) {
	return func(_ context.Context) (<-chan *mdnsEntry, error) {
		ch := make(chan *mdnsEntry)
		close(ch)
		return ch, nil
	}
}

// emptyBrowseUDP mirrors emptyBrowseSSDP for the UDP protocol.
func emptyBrowseUDP() func(ctx context.Context) (<-chan *mdnsEntry, error) {
	return func(_ context.Context) (<-chan *mdnsEntry, error) {
		ch := make(chan *mdnsEntry)
		close(ch)
		return ch, nil
	}
}

// stubBrowseSSDP returns a stub that emits the provided entries then closes.
func stubBrowseSSDP(entries ...*mdnsEntry) func(ctx context.Context) (<-chan *mdnsEntry, error) {
	return func(_ context.Context) (<-chan *mdnsEntry, error) {
		ch := make(chan *mdnsEntry, len(entries))
		for _, e := range entries {
			ch <- e
		}
		close(ch)
		return ch, nil
	}
}

// stubBrowseUDP mirrors stubBrowseSSDP for the UDP protocol.
func stubBrowseUDP(entries ...*mdnsEntry) func(ctx context.Context) (<-chan *mdnsEntry, error) {
	return func(_ context.Context) (<-chan *mdnsEntry, error) {
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
		browseSSDP: emptyBrowseSSDP(),
		browseUDP:  emptyBrowseUDP(),
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
	d := &Driver{browse: stubBrowse(), browseSSDP: emptyBrowseSSDP(), browseUDP: emptyBrowseUDP()}
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
		browse:     stubBrowse(&mdnsEntry{Host: "10.0.0.1", Port: 8883, Text: nil}),
		browseSSDP: emptyBrowseSSDP(),
		browseUDP:  emptyBrowseUDP(),
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

func TestDiscover_CombinesAllThreeProtocols(t *testing.T) {
	d := &Driver{
		browse: stubBrowse(
			&mdnsEntry{Host: "192.0.2.10", Port: 8883, Text: []string{"sn=SN-MDNS", "dev_model_name=X1C", "dev_name=mDNS-Printer"}},
		),
		browseSSDP: stubBrowseSSDP(
			&mdnsEntry{Host: "192.0.2.11", Port: 8883, Text: []string{"sn=SN-SSDP", "dev_model_name=P1S", "dev_name=SSDP-Printer"}},
		),
		browseUDP: stubBrowseUDP(
			&mdnsEntry{Host: "192.0.2.12", Port: 8883, Text: []string{"sn=SN-UDP", "dev_name=UDP-Printer"}},
		),
	}

	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(printers) != 3 {
		t.Errorf("expected 3 printers (one per protocol), got %d: %+v", len(printers), printers)
	}

	serialsSeen := map[string]bool{}
	for _, p := range printers {
		serialsSeen[p.Serial] = true
	}
	for _, sn := range []string{"SN-MDNS", "SN-SSDP", "SN-UDP"} {
		if !serialsSeen[sn] {
			t.Errorf("expected serial %q in combined results", sn)
		}
	}
}

func TestDiscover_DeduplicatesBySerial(t *testing.T) {
	samePrinter := &mdnsEntry{
		Host: "192.0.2.10", Port: 8883,
		Text: []string{"sn=DUPLICATE-SN", "dev_model_name=X1C", "dev_name=My Printer"},
	}
	d := &Driver{
		browse:     stubBrowse(samePrinter),
		browseSSDP: stubBrowseSSDP(samePrinter),
		browseUDP:  stubBrowseUDP(samePrinter),
	}

	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(printers) != 1 {
		t.Errorf("expected 1 printer after dedup, got %d: %+v", len(printers), printers)
	}
	if printers[0].Serial != "DUPLICATE-SN" {
		t.Errorf("Serial = %q, want %q", printers[0].Serial, "DUPLICATE-SN")
	}
}

func TestDiscover_DeduplicatesByHost_WhenNoSerial(t *testing.T) {
	samePrinterNoSerial := &mdnsEntry{Host: "192.0.2.10", Port: 8883, Text: nil}
	d := &Driver{
		browse:     stubBrowse(samePrinterNoSerial),
		browseSSDP: stubBrowseSSDP(samePrinterNoSerial),
		browseUDP:  emptyBrowseUDP(),
	}

	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(printers) != 1 {
		t.Errorf("expected 1 printer after host dedup (no serial), got %d", len(printers))
	}
}

func TestDiscover_PartialProtocolFailure_ReturnsRemainingResults(t *testing.T) {
	errBrowse := func(_ context.Context) (<-chan *mdnsEntry, error) {
		return nil, fmt.Errorf("port in use")
	}
	d := &Driver{
		browse: stubBrowse(
			&mdnsEntry{Host: "192.0.2.10", Port: 8883, Text: []string{"sn=SN1", "dev_name=Printer1"}},
		),
		browseSSDP: errBrowse,
		browseUDP:  errBrowse,
	}

	printers, err := d.Discover(context.Background())
	if err != nil {
		t.Fatalf("partial failure should not return error, got: %v", err)
	}
	if len(printers) != 1 {
		t.Errorf("expected 1 printer from mDNS despite SSDP/UDP failures, got %d", len(printers))
	}
}

func TestDiscover_AllProtocolsFail_ReturnsError(t *testing.T) {
	errBrowseMDNS := func(_ context.Context, _ string) (<-chan *mdnsEntry, error) {
		return nil, fmt.Errorf("mDNS unavailable")
	}
	errBrowse := func(_ context.Context) (<-chan *mdnsEntry, error) {
		return nil, fmt.Errorf("port in use")
	}
	d := &Driver{
		browse:     errBrowseMDNS,
		browseSSDP: errBrowse,
		browseUDP:  errBrowse,
	}

	_, err := d.Discover(context.Background())
	if err == nil {
		t.Fatal("expected error when all protocols fail")
	}
	var ae *apperr.ExitError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *apperr.ExitError, got %T: %v", err, err)
	}
	if ae.Code != 4 {
		t.Errorf("exit code = %d, want 4", ae.Code)
	}
}
