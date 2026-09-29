package rest

import (
	"bytes"
	"encoding/json"
	"errors"
)

// Codec marshals and unmarshals JSON payloads. It is deliberately the same
// shape as encoding/json so the default adapter is a thin wrapper and a lean
// codec can be dropped in later without touching call sites.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// StrictCodec is the optional half of the seam: a codec that can reject
// unknown fields. Binding uses UnmarshalStrict when Strict is enabled (the
// default) and the codec implements this interface; otherwise it falls back
// to Unmarshal and decodes leniently.
type StrictCodec interface {
	Codec
	UnmarshalStrict(data []byte, v any) error
}

// DefaultCodec is used whenever an operation does not supply one. Swapping it
// once at startup (before serving) changes the codec for the whole worker,
// mirroring how cloudflare.MaxBodyBytes is configured.
//
// The default is stdlib encoding/json, which measured ~+239 KB gzip on top of
// a plain worker — the price of full JSON fidelity. Codecs that only
// implement Codec are supported; they simply cannot enforce strict decoding.
var DefaultCodec Codec = stdlibJSON{}

type stdlibJSON struct{}

func (stdlibJSON) Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (stdlibJSON) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// UnmarshalStrict decodes a single JSON value, rejecting unknown fields and
// any trailing data after the value.
func (stdlibJSON) UnmarshalStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("rest: unexpected trailing data after JSON value")
	}
	return nil
}
