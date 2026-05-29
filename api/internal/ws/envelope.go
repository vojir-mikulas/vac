package ws

import (
	"encoding/json"
	"time"
)

// Frame is the envelope every WS message carries. Data is the producer's
// pre-marshalled payload. ID is set for ordered, dedup-able streams (build and
// runtime logs carry their DB row id); it is zero for stateless streams (stats,
// host). Type lets the client and the log handlers branch without decoding Data
// — e.g. the build-log stream ends on a "build-end" frame.
type Frame struct {
	Type    string          `json:"type"`
	ID      int64           `json:"id,omitempty"`
	TS      time.Time       `json:"ts"`
	Service string          `json:"service,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// Marshal builds a frame with no id and returns its JSON bytes — for stateless
// streams (stats, host snapshots).
func Marshal(typ, service string, ts time.Time, data any) ([]byte, error) {
	return marshalFrame(typ, 0, service, ts, data)
}

// LogFrame builds a frame carrying a monotonic id, for log streams that replay
// a DB backlog then tail live and need to dedup the overlap by id.
func LogFrame(typ, service string, id int64, ts time.Time, data any) ([]byte, error) {
	return marshalFrame(typ, id, service, ts, data)
}

// Control builds a payload-less frame, e.g. the "build-end" terminator.
func Control(typ string, ts time.Time) ([]byte, error) {
	return marshalFrame(typ, 0, "", ts, nil)
}

func marshalFrame(typ string, id int64, service string, ts time.Time, data any) ([]byte, error) {
	var raw json.RawMessage
	if data != nil {
		b, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	return json.Marshal(Frame{Type: typ, ID: id, TS: ts, Service: service, Data: raw})
}

// Decode parses a frame's envelope. Used by the log handlers to read .ID / .Type
// for dedup and terminal detection without touching Data.
func Decode(b []byte) (Frame, error) {
	var f Frame
	err := json.Unmarshal(b, &f)
	return f, err
}
