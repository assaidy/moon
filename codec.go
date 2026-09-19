package moon

import (
	"encoding/json/v2"
	"encoding/xml"

	"github.com/shamaton/msgpack/v2"
)

type Codec interface {
	Encode(v any) ([]byte, error)
	Decode(raw []byte, v any) error
	ContentType() string
}

var (
	CodecJson        Codec = jsonCodec{}
	CodecXml         Codec = xmlCodec{}
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
