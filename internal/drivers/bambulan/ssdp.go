package bambulan

import (
	"context"
	"net"
	"strings"
	"time"
)

const (
	ssdpMulticast    = "239.255.255.250:1900"
	ssdpSearchTarget = "urn:bambulab-com:device:3dprinter:1"
)

// realBrowseSSDP sends a UDP M-SEARCH to the SSDP multicast address and
// returns a channel of mdnsEntry values from Bambu printer responses.
// It also accepts NOTIFY packets if the printer sends them unsolicited.
func realBrowseSSDP(ctx context.Context) (<-chan *mdnsEntry, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: 0})
	if err != nil {
		return nil, err
	}

	multicastAddr, err := net.ResolveUDPAddr("udp4", ssdpMulticast)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}

	mSearch := "M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"MAN: \"ssdp:discover\"\r\n" +
		"MX: 3\r\n" +
		"ST: " + ssdpSearchTarget + "\r\n\r\n"

	if _, err := conn.WriteToUDP([]byte(mSearch), multicastAddr); err != nil {
		_ = conn.Close()
		return nil, err
	}

	out := make(chan *mdnsEntry)
	go func() {
		defer close(out)
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 2048)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
				n, addr, err := conn.ReadFromUDP(buf)
				if err != nil {
					if ne, ok := err.(net.Error); ok && ne.Timeout() {
						continue
					}
					return
				}
				if e := parseSSDPResponse(string(buf[:n]), addr.IP.String()); e != nil {
					select {
					case out <- e:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()
	return out, nil
}

// parseSSDPResponse parses a raw SSDP message (M-SEARCH response or NOTIFY).
// Returns nil if the message is not from a Bambu printer.
// sourceIP is used as Host when the LOCATION header is absent or unparseable.
func parseSSDPResponse(msg, sourceIP string) *mdnsEntry {
	headers := parseSSDPHeaders(msg)

	isBambu := strings.Contains(headers["ST"], ssdpSearchTarget) ||
		strings.Contains(headers["NT"], ssdpSearchTarget)
	if !isBambu {
		return nil
	}

	host := sourceIP
	if loc, ok := headers["LOCATION"]; ok {
		if ip := extractIPFromURL(loc); ip != "" {
			host = ip
		}
	}

	var txt []string
	if sn := extractSerialFromUSN(headers["USN"]); sn != "" {
		txt = append(txt, "sn="+sn)
	}
	if model := headers["DEVMODEL.BAMBU.COM"]; model != "" {
		txt = append(txt, "dev_model_name="+model)
	}
	if name := headers["DEVNAME.BAMBU.COM"]; name != "" {
		txt = append(txt, "dev_name="+name)
	}

	return &mdnsEntry{Host: host, Port: 8883, Text: txt}
}

// parseSSDPHeaders parses "Key: Value\r\n" lines from an SSDP message.
// Keys are upper-cased. The first status/request line is ignored.
func parseSSDPHeaders(msg string) map[string]string {
	headers := make(map[string]string)
	for _, line := range strings.Split(msg, "\r\n") {
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.ToUpper(strings.TrimSpace(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])
		if key != "" {
			headers[key] = value
		}
	}
	return headers
}

// extractIPFromURL extracts the host IP from a URL like "http://192.0.2.10/" or "http://[::1]:80/path".
// Handles both IPv4 and IPv6 addresses correctly.
func extractIPFromURL(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	// Try net.SplitHostPort first (handles both IPv4 "host:port" and IPv6 "[::1]:port").
	if host, _, err := net.SplitHostPort(s); err == nil {
		return host
	}
	// No port present; strip brackets for bare IPv6 like "[::1]".
	return strings.TrimSuffix(strings.TrimPrefix(s, "["), "]")
}

// extractSerialFromUSN extracts the serial from a USN like "uuid:SERIAL::urn:...".
func extractSerialFromUSN(usn string) string {
	if !strings.HasPrefix(usn, "uuid:") {
		return ""
	}
	s := usn[5:] // strip "uuid:"
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}
