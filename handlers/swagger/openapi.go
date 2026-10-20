package swagger

import "encoding/json/v2"

// openApiDocument is the root of the generated OpenAPI description.
type openApiDocument struct {
	OpenApi    string                    `json:"openapi"`
	Info       openApiInfo               `json:"info"`
	Servers    []openApiServer           `json:"servers,omitempty"`
	Paths      map[string]map[string]any `json:"paths"`
	Components map[string]any            `json:"components,omitempty"`
}

type openApiInfo struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version"`
}

type openApiServer struct {
	Url         string `json:"url"`
	Description string `json:"description,omitempty"`
}

type openApiParameter struct {
	Name            string `json:"name"`
	In              string `json:"in"`
	Description     string `json:"description,omitempty"`
	Required        bool   `json:"required,omitzero"`
	Deprecated      bool   `json:"deprecated,omitzero"`
	Style           string `json:"style,omitempty"`
	Explode         *bool  `json:"explode,omitempty"`
	AllowEmptyValue bool   `json:"allowEmptyValue,omitzero"`
	AllowReserved   bool   `json:"allowReserved,omitzero"`
	Schema          any    `json:"schema,omitzero"`
	Example         any    `json:"example,omitzero"`
}

type openApiRequestBody struct {
	Description string                      `json:"description,omitempty"`
	Required    bool                        `json:"required,omitzero"`
	Content     map[string]openApiMediaType `json:"content"`
}

type openApiMediaType struct {
	Schema   any            `json:"schema,omitzero"`
	Example  any            `json:"example,omitzero"`
	Examples map[string]any `json:"examples,omitempty"`
}

type openApiResponse struct {
	Description string                      `json:"description"`
	Headers     map[string]openApiHeader    `json:"headers,omitempty"`
	Content     map[string]openApiMediaType `json:"content,omitempty"`
}

type openApiHeader struct {
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitzero"`
	Deprecated  bool   `json:"deprecated,omitzero"`
	Style       string `json:"style,omitempty"`
	Explode     *bool  `json:"explode,omitempty"`
	Schema      any    `json:"schema,omitzero"`
	Example     any    `json:"example,omitzero"`
}

func marshalJson(value any) ([]byte, error) {
	return json.Marshal(value, json.Deterministic(true))
}
