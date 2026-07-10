package bambulan

import (
	"sync"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// fakeCommandClient is an mqttConn for testing command-style operations.
// On each Publish call, it captures the payload and delivers responses[i] to
// the subscribe handler synchronously (if non-nil).
type fakeCommandClient struct {
	connectErr   error
	subscribeErr error
	publishErrs  []error  // publishErrs[i] returned for Publish call i
	responses    [][]byte // responses[i] delivered to subscribe handler after Publish i (nil = no delivery)

	mu           sync.Mutex
	connectCalls int
	published    []string // captured payloads as strings
	handler      mqtt.MessageHandler
	handlerTopic string
}

func (f *fakeCommandClient) Connect() mqtt.Token {
	f.mu.Lock()
	f.connectCalls++
	f.mu.Unlock()
	return newFakeToken(f.connectErr)
}

func (f *fakeCommandClient) Subscribe(topic string, _ byte, cb mqtt.MessageHandler) mqtt.Token {
	if f.subscribeErr != nil {
		return newFakeToken(f.subscribeErr)
	}
	f.mu.Lock()
	f.handler = cb
	f.handlerTopic = topic
	f.mu.Unlock()
	return newFakeToken(nil)
}

func (f *fakeCommandClient) Publish(_ string, _ byte, _ bool, payload any) mqtt.Token {
	f.mu.Lock()
	idx := len(f.published)
	var data string
	switch v := payload.(type) {
	case string:
		data = v
	case []byte:
		data = string(v)
	}
	f.published = append(f.published, data)

	var publishErr error
	if idx < len(f.publishErrs) {
		publishErr = f.publishErrs[idx]
	}
	var response []byte
	if idx < len(f.responses) {
		response = f.responses[idx]
	}
	handler := f.handler
	topic := f.handlerTopic
	f.mu.Unlock()

	if publishErr != nil {
		return newFakeToken(publishErr)
	}
	// Deliver response synchronously so the predicate loop finds it in the channel.
	if response != nil && handler != nil {
		handler(nil, &fakeMessage{topic: topic, payload: response})
	}
	return newFakeToken(nil)
}

func (f *fakeCommandClient) Disconnect(_ uint) {}

func (f *fakeCommandClient) getPublished() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.published))
	copy(out, f.published)
	return out
}

func (f *fakeCommandClient) getConnectCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connectCalls
}

// newCommandDriver returns a Driver that uses fc for its MQTT client.
func newCommandDriver(fc *fakeCommandClient) *Driver {
	return &Driver{newClient: func(_ *mqtt.ClientOptions) mqttConn { return fc }}
}

// pushallResponse returns a valid pushall-style report in the given gcode_state.
func pushallResponse(gcodeState string) []byte {
	return []byte(`{"print":{"gcode_state":"` + gcodeState + `","nozzle_temper":200.0,"nozzle_target_temper":200.0,"bed_temper":60.0,"bed_target_temper":60.0,"chamber_temper":0,"mc_percent":50,"hms":[]}}`)
}
