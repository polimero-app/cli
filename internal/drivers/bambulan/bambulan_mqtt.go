package bambulan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
	"github.com/polimero-app/cli/internal/protocoltrace"
)

var mqttSequence atomic.Int64

func nextSequenceID() string {
	now := time.Now().UnixMilli()
	for {
		last := mqttSequence.Load()
		next := now
		if next <= last {
			next = last + 1
		}
		if mqttSequence.CompareAndSwap(last, next) {
			return strconv.FormatInt(next, 10)
		}
	}
}

// mqttCommand connects, subscribes to the report topic, publishes commandPayload
// to the request topic, then immediately publishes a pushall to get a fresh full
// status update. It loops consuming report messages until predicate returns true,
// then returns the matching raw report bytes.
//
// The pushall after the command is required for P1/A1 printers which only send
// delta reports autonomously (isPushallReport would otherwise never return true).
func (d *Driver) mqttCommand(
	ctx context.Context,
	p driver.ProfileInput,
	s driver.SecretsBundle,
	commandPayload string,
	predicate func([]byte) bool,
) ([]byte, error) {
	trace := protocoltrace.FromContext(ctx)
	endpoint := fmt.Sprintf("%s:8883", p.Host)

	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s", endpoint))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(s.AccessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(p.Timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := d.newClient(opts)

	connectStart := time.Now()
	if err := waitMQTTToken(ctx, client.Connect()); err != nil {
		dur := time.Since(connectStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "Command",
			Phase:         "connect",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			Protocol:      "mqttv3.1.1",
			DurationMs:    &dur,
			ErrorCategory: classifyTraceError(err),
		})
		if isContextDoneErr(err) {
			go client.Disconnect(0)
			return nil, mqttContextError(err)
		}
		return nil, classifyStatusError(err)
	}
	connectDur := time.Since(connectStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "Command",
		Phase:      "connect",
		Transport:  "mqtt",
		Endpoint:   endpoint,
		Protocol:   "mqttv3.1.1",
		DurationMs: &connectDur,
	})
	defer client.Disconnect(250)

	ch := make(chan []byte, 8)
	reportTopic := fmt.Sprintf("device/%s/report", p.Serial)
	requestTopic := fmt.Sprintf("device/%s/request", p.Serial)

	subStart := time.Now()
	subToken := client.Subscribe(reportTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := make([]byte, len(msg.Payload()))
		copy(payload, msg.Payload())
		select {
		case ch <- payload:
		default:
		}
	})
	if err := waitMQTTToken(ctx, subToken); err != nil {
		dur := time.Since(subStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "Command",
			Phase:         "subscribe",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			ErrorCategory: classifyTraceError(err),
		})
		if isContextDoneErr(err) {
			return nil, mqttContextError(err)
		}
		return nil, apperr.Wrap(4, "command subscription failed", err)
	}
	subDur := time.Since(subStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "Command",
		Phase:      "subscribe",
		Transport:  "mqtt",
		Endpoint:   endpoint,
		DurationMs: &subDur,
	})

	pubStart := time.Now()
	pubToken := client.Publish(requestTopic, 0, false, commandPayload)
	if err := waitMQTTToken(ctx, pubToken); err != nil {
		dur := time.Since(pubStart).Milliseconds()
		trace.Emit(protocoltrace.Event{
			Timestamp:     time.Now().UTC(),
			Driver:        "bambu-lan",
			Operation:     "Command",
			Phase:         "publish",
			Transport:     "mqtt",
			Endpoint:      endpoint,
			DurationMs:    &dur,
			Payload:       json.RawMessage(commandPayload),
			ErrorCategory: classifyTraceError(err),
		})
		if isContextDoneErr(err) {
			return nil, mqttContextError(err)
		}
		return nil, apperr.Wrap(4, "command publish failed", err)
	}
	pubDur := time.Since(pubStart).Milliseconds()
	trace.Emit(protocoltrace.Event{
		Timestamp:  time.Now().UTC(),
		Driver:     "bambu-lan",
		Operation:  "Command",
		Phase:      "publish",
		Transport:  "mqtt",
		Endpoint:   endpoint,
		DurationMs: &pubDur,
		Payload:    json.RawMessage(commandPayload),
	})

	const pushall = `{"pushing":{"sequence_id":"1","command":"pushall","version":1,"push_target":1}}`
	pubToken = client.Publish(requestTopic, 0, false, pushall)
	if err := waitMQTTToken(ctx, pubToken); err != nil {
		if isContextDoneErr(err) {
			return nil, mqttContextError(err)
		}
		return nil, apperr.Wrap(4, "pushall request failed", err)
	}

	receiveStart := time.Now()
	for {
		select {
		case data := <-ch:
			if err := commandRejectionError(data); err != nil {
				return nil, err
			}
			if predicate(data) {
				dur := time.Since(receiveStart).Milliseconds()
				bc := int64(len(data))
				trace.Emit(protocoltrace.Event{
					Timestamp:  time.Now().UTC(),
					Driver:     "bambu-lan",
					Operation:  "Command",
					Phase:      "receive",
					Transport:  "mqtt",
					Endpoint:   endpoint,
					DurationMs: &dur,
					ByteCount:  &bc,
					Payload:    json.RawMessage(data),
				})
				return data, nil
			}
		case <-ctx.Done():
			dur := time.Since(receiveStart).Milliseconds()
			trace.Emit(protocoltrace.Event{
				Timestamp:     time.Now().UTC(),
				Driver:        "bambu-lan",
				Operation:     "Command",
				Phase:         "receive",
				Transport:     "mqtt",
				Endpoint:      endpoint,
				DurationMs:    &dur,
				ErrorCategory: "timeout",
			})
			return nil, mqttContextError(ctx.Err())
		}
	}
}

func commandRejectionError(data []byte) error {
	var rep struct {
		Print *struct {
			Result  *string         `json:"result"`
			Reason  *string         `json:"reason"`
			ErrCode *rawValueString `json:"err_code"`
			ErrNo   *rawValueString `json:"errno"`
		} `json:"print"`
	}
	if err := json.Unmarshal(data, &rep); err != nil || rep.Print == nil {
		return nil
	}
	reason := ""
	if rep.Print.Reason != nil {
		reason = strings.TrimSpace(*rep.Print.Reason)
	}
	errCode := rawToInt(rep.Print.ErrCode)
	if errCode == nil {
		errCode = rawToInt(rep.Print.ErrNo)
	}
	lowerReason := strings.ToLower(reason)
	if (errCode != nil && *errCode == 84033543) || strings.Contains(lowerReason, "verification failed") {
		return apperr.New(3, "printer rejected unsigned command; enable Developer Mode or use a signed command path")
	}
	if rep.Print.Result != nil && strings.EqualFold(strings.TrimSpace(*rep.Print.Result), "fail") {
		return apperr.New(1, "printer rejected command")
	}
	return nil
}

func mqttContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return apperr.New(4, "command cancelled")
	}
	return apperr.New(4, "command timed out")
}
