package moon

import (
	"encoding/json/v2"
	"encoding/xml"

	"github.com/shamaton/msgpack/v2"
)

// Codec encodes and decodes request and response bodies.
// See [Context.ReadAs] and [Context.WriteAs].
// [Context.WriteAs] sets the Content-Type header from [Codec.ContentType].
type Codec interface {
	// Encode serializes v for the response body.
	Encode(v any) ([]byte, error)
	// Decode parses raw request body into v.
	Decode(raw []byte, v any) error
	// ContentType returns the MIME type set on responses written with this codec.
	ContentType() string
}

var (
	// CodecJson encodes and decodes JSON ("application/json").
	CodecJson Codec = jsonCodec{}
	// CodecXml encodes and decodes XML ("application/xml").
	CodecXml Codec = xmlCodec{}
	// CodecMessagePack encodes and decodes MessagePack ("application/vnd.msgpack").
	CodecMessagePack Codec = messagePackCodec{}
)

type jsonCodec struct{}

func (me jsonCodec) Encode(v any) ([]byte, error)   { return json.Marshal(v) }
func (me jsonCodec) Decode(raw []byte, v any) error { return json.Unmarshal(raw, v) }
func (me jsonCodec) ContentType() string            { return "application/json" }

type xmlCodec struct{}

func (me xmlCodec) Encode(v any) ([]byte, error)   { return xml.Marshal(v) }
func (me xmlCodec) Decode(raw []byte, v any) error { return xml.Unmarshal(raw, v) }
func (me xmlCodec) ContentType() string            { return "application/xml" }

type messagePackCodec struct{}

func (me messagePackCodec) Encode(v any) ([]byte, error)   { return msgpack.Marshal(v) }
func (me messagePackCodec) Decode(raw []byte, v any) error { return msgpack.Unmarshal(raw, v) }
func (me messagePackCodec) ContentType() string            { return "application/vnd.msgpack" }
