package moon

import (
	"encoding/xml"
	"testing"
)

func BenchmarkCodec(b *testing.B) {
	type codecBenchmarkAddress struct {
		City string `xml:"city" json:"city" msgpack:"city"`
		Zip  string `xml:"zip" json:"zip" msgpack:"zip"`
	}

	type codecBenchmarkValue struct {
		XMLName xml.Name              `xml:"user" json:"-" msgpack:"-"`
		ID      int                   `xml:"id" json:"id" msgpack:"id"`
		Name    string                `xml:"name" json:"name" msgpack:"name"`
		Email   string                `xml:"email" json:"email" msgpack:"email"`
		Active  bool                  `xml:"active" json:"active" msgpack:"active"`
		Tags    []string              `xml:"tags>tag" json:"tags" msgpack:"tags"`
		Address codecBenchmarkAddress `xml:"address" json:"address" msgpack:"address"`
	}

	var codecBenchmarkPayload = codecBenchmarkValue{
		ID:     123,
		Name:   "ann",
		Email:  "ann@example.com",
		Active: true,
		Tags:   []string{"admin", "editor"},
		Address: codecBenchmarkAddress{
			City: "Cairo",
			Zip:  "11511",
		},
	}

	cases := []struct {
		name  string
		codec Codec
	}{
		{"json", CodecJson},
		{"xml", CodecXml},
		{"message pack", CodecMessagePack},
	}

	b.Run("encode", func(b *testing.B) {
		for _, c := range cases {
			b.Run(c.name, func(b *testing.B) {
				raw, err := c.codec.Encode(codecBenchmarkPayload)
				if err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(len(raw)))

				for b.Loop() {
					_, err := c.codec.Encode(codecBenchmarkPayload)
					if err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	})

	b.Run("decode", func(b *testing.B) {
		encoded := make(map[string][]byte, len(cases))
		for _, c := range cases {
			raw, err := c.codec.Encode(codecBenchmarkPayload)
			if err != nil {
				b.Fatal(err)
			}
			encoded[c.name] = raw
		}

		for _, c := range cases {
			b.Run(c.name, func(b *testing.B) {
				raw := encoded[c.name]
				b.SetBytes(int64(len(raw)))

				for b.Loop() {
					var out codecBenchmarkValue
					if err := c.codec.Decode(raw, &out); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	})
}
