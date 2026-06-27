package bambulan

import (
	"context"
	"errors"
	"fmt"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/polimero-app/cli/internal/apperr"
	"github.com/polimero-app/cli/internal/driver"
)

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
	tlsCfg, err := buildTLSConfig(p.Serial, s.TLSFingerprint, p.Insecure)
	if err != nil {
		return nil, err
	}

	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", p.Host))
	opts.SetClientID(randomClientID())
	opts.SetUsername("bblp")
	opts.SetPassword(s.AccessCode)
	opts.SetTLSConfig(tlsCfg)
	opts.SetConnectTimeout(p.Timeout)
	opts.SetAutoReconnect(false)
	opts.SetKeepAlive(60)

	client := d.newClient(opts)

	if err := waitMQTTToken(ctx, client.Connect()); err != nil {
		if isContextDoneErr(err) {
			go client.Disconnect(0)
			return nil, mqttContextError(err)
		}
		return nil, classifyStatusError(err)
	}
	defer client.Disconnect(250)

	ch := make(chan []byte, 8)
	reportTopic := fmt.Sprintf("device/%s/report", p.Serial)
	requestTopic := fmt.Sprintf("device/%s/request", p.Serial)

	subToken := client.Subscribe(reportTopic, 0, func(_ mqtt.Client, msg mqtt.Message) {
		payload := make([]byte, len(msg.Payload()))
		copy(payload, msg.Payload())
		select {
		case ch <- payload:
		default:
		}
	})
	if err := waitMQTTToken(ctx, subToken); err != nil {
		if isContextDoneErr(err) {
			return nil, mqttContextError(err)
		}
		return nil, apperr.Wrap(4, "command subscription failed", err)
	}

	pubToken := client.Publish(requestTopic, 0, false, commandPayload)
	if err := waitMQTTToken(ctx, pubToken); err != nil {
		if isContextDoneErr(err) {
			return nil, mqttContextError(err)
		}
		return nil, apperr.Wrap(4, "command publish failed", err)
	}

	const pushall = `{"pushing":{"sequence_id":"1","command":"pushall","version":1,"push_target":1}}`
	pubToken = client.Publish(requestTopic, 0, false, pushall)
	if err := waitMQTTToken(ctx, pubToken); err != nil {
		if isContextDoneErr(err) {
			return nil, mqttContextError(err)
		}
		return nil, apperr.Wrap(4, "pushall request failed", err)
	}

	for {
		select {
		case data := <-ch:
			if predicate(data) {
				return data, nil
			}
		case <-ctx.Done():
			return nil, mqttContextError(ctx.Err())
		}
	}
}

func mqttContextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return apperr.New(4, "command cancelled")
	}
	return apperr.New(4, "command timed out")
}
