package bambulan

import (
	"context"
	"encoding/json"
	"net"
	"time"
)

const bambuBroadcastPort = 2021

type bambuBroadcastMsg struct {
	DevName        string `json:"dev_name"`
	SN             string `json:"sn"`
	IP             string `json:"ip"`
	DevProductName string `json:"dev_product_name"`
}

// realBrowseUDP listens on UDP port 2021 for Bambu printer broadcast announcements.
// Printers broadcast periodically (~20–30s); results depend on timing within ctx duration.
func realBrowseUDP(ctx context.Context) (<-chan *mdnsEntry, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: bambuBroadcastPort})
	if err != nil {
		return nil, err
	}

	out := make(chan *mdnsEntry)
	go func() {
		defer close(out)
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 4096)
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
				if e := parseUDPBroadcast(buf[:n], addr.IP.String()); e != nil {
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

// parseUDPBroadcast decodes a Bambu LAN broadcast JSON payload into an mdnsEntry.
// Returns nil when data is not valid JSON from a Bambu printer.
// sourceIP is used as Host when the payload lacks an "ip" field.
func parseUDPBroadcast(data []byte, sourceIP string) *mdnsEntry {
	var msg bambuBroadcastMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil
	}

	host := sourceIP
	if msg.IP != "" {
		host = msg.IP
	}

	var txt []string
	if msg.SN != "" {
		txt = append(txt, "sn="+msg.SN)
	}
	if msg.DevName != "" {
		txt = append(txt, "dev_name="+msg.DevName)
	}
	if msg.DevProductName != "" {
		txt = append(txt, "dev_model_name="+msg.DevProductName)
	}

	return &mdnsEntry{Host: host, Port: 8883, Text: txt}
}
