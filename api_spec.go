package moon

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

// Parameter locations accepted by [ApiParameter.In].
const (
	ApiParamInPath   = "path"
	ApiParamInQuery  = "query"
	ApiParamInHeader = "header"
	ApiParamInCookie = "cookie"
)

// apiSpec is the documentation metadata of a single route. Create it with
// [NewApiSpec] and attach it to a route with [WithApiSpec]. Handlers read every
// registered route and its spec through [Context.GetApiSpecs].
//
// apiSpec is intentionally independent of any documentation format, but its
// fields line up with an OpenAPI Operation Object: Summary, Description,
// OperationId, Tags, Deprecated, Parameters, RequestBody, Responses and
// Security. Extra carries raw operation fields or extensions that the model
// does not cover.
//
// apiSpec does not carry the route method or pattern. Those live on
// [ApiSpecEntry], which is what [Context.GetApiSpecs] returns.
type apiSpec struct {
	// Summary is a short description of the operation.
	Summary string
	// Description is a verbose explanation of the operation. It may contain
	// markdown.
	Description string
	// OperationId is a unique identifier for the operation. It must be
	// unique across the whole API when it is not empty. Note that
	// [App.MapAll] attaches the same spec to every method, so an operation
	// id set that way repeats for each method.
	OperationId string
	// Tags group the operation for documentation UIs.
	Tags []string
	// Deprecated marks the operation as deprecated. Consumers should avoid it.
	Deprecated bool
	// Parameters documents operation parameters. Path parameters are also
	// inferred from the route pattern when they are missing here.
	Parameters []ApiParameter
	// RequestBody documents the request body, when the operation has one.
	RequestBody *ApiRequestBody
	// Responses documents the possible responses. A [ApiResponse.Status] of
	// zero means the default response.
	Responses []ApiResponse
	// Security declares the security requirements of the operation.
	Security []ApiSecurityRequirement
	// Extra holds raw operation fields or specification extensions. Entries
	// are merged into the generated operation object last, so they can
	// override the fields above. Values must be JSON-compatible; a
	// documentation generator returns an error when they are not.
	Extra map[string]any
}

// ApiParameter documents a single operation parameter. A parameter is unique
// by its [ApiParameter.In] and [ApiParameter.Name] pair.
type ApiParameter struct {
	// Name is the parameter name as it appears in the request. It is trimmed
	// when the spec is created. Path parameters must match a parameter name in
	// the route pattern, including the wildcard names "*", "+", "*1", ...
	Name string
	// In is the parameter location: [ApiParamInPath], [ApiParamInQuery],
	// [ApiParamInHeader] or [ApiParamInCookie]. It is trimmed when the spec is
	// created.
	In string
	// Description explains the parameter. It may contain markdown.
	Description string
	// Required marks the parameter as required. Path parameters are always
	// required, regardless of this field.
	Required bool
	// Deprecated marks the parameter as deprecated.
	Deprecated bool
	// Style is the serialization style, such as "form", "simple",
	// "spaceDelimited", "pipeDelimited" or "deepObject". It is trimmed when the
	// spec is created. An empty value leaves the generator default for In.
	Style string
	// Explode controls whether arrays and objects generate separate
	// parameters. Nil leaves the generator default for Style.
	Explode *bool
	// AllowEmptyValue allows an empty value for query parameters. Setting it
	// for another location panics when the spec is created.
	AllowEmptyValue bool
	// AllowReserved allows reserved characters in parameter values where
	// percent-encoding applies.
	AllowReserved bool
	// Schema is passed through to the documentation generator as-is. It is
	// usually a JSON Schema object such as [Object] or map[string]any. A
	// generator may assume a string schema when it is nil.
	Schema any
	// Example is passed through to the documentation generator as-is.
	Example any
}

// ApiRequestBody documents the body of a request. Content maps a media type
// to its schema and examples.
type ApiRequestBody struct {
	// Description explains the request body. It may contain markdown.
	Description string
	// Required marks the body as required.
	Required bool
	// Content maps media types, such as "application/json", to their
	// documentation. Keys are trimmed when the spec is created. The route is
	// not documented with a request body when this map is empty.
	Content map[string]ApiMediaType
}

// ApiMediaType documents one media type of a request or response body.
type ApiMediaType struct {
	// Schema is passed through to the documentation generator as-is.
	Schema any
	// Example is passed through to the documentation generator as-is. It is
	// mutually exclusive with Examples.
	Example any
	// Examples maps example names to OpenAPI Example Objects, such as
	// Object{"value": ...}, or Reference Objects. Values are passed through
	// as-is. It is mutually exclusive with Example.
	Examples map[string]any
}

// ApiResponse documents one response of an operation. A zero Status means the
// default response.
type ApiResponse struct {
	// Status is the HTTP status code, or zero for the default response.
	Status int
	// Description explains the response. It may contain markdown. When empty,
	// a generator may derive a description from the status code.
	Description string
	// Content maps media types to their documentation. Keys are trimmed when
	// the spec is created.
	Content map[string]ApiMediaType
	// Headers documents response headers by name. Names are trimmed and must
	// be unique case-insensitively.
	Headers map[string]ApiHeader
}

// ApiHeader documents a response header.
type ApiHeader struct {
	// Description explains the header. It may contain markdown.
	Description string
	// Required marks the header as required.
	Required bool
	// Deprecated marks the header as deprecated.
	Deprecated bool
	// Style is the serialization style. Response headers only support
	// "simple"; it is trimmed when the spec is created, and an empty value
	// leaves the generator default.
	Style string
	// Explode controls whether arrays and objects generate separate values.
	// Nil leaves the generator default for Style.
	Explode *bool
	// Schema is passed through to the documentation generator as-is. A
	// generator may assume a string schema when it is nil.
	Schema any
	// Example is passed through to the documentation generator as-is.
	Example any
}

// ApiSecurityRequirement is one security requirement: scheme name to scopes.
// Scheme names are trimmed when the spec is created. An empty requirement means
// authentication is optional.
type ApiSecurityRequirement map[string][]string

// ApiSpecEntry pairs a registered route with its documentation metadata.
type ApiSpecEntry struct {
	// Method is the route HTTP method, such as [MethodGet].
	Method string
	// Pattern is the registered route pattern, such as "/users/:id".
	Pattern string
	// Spec is the documentation metadata of the route.
	Spec apiSpec
}

// ApiSpecOption configures a spec created by [NewApiSpec].
type ApiSpecOption func(*apiSpec)

// NewApiSpec creates route documentation metadata from options. Pass the
// result to [WithApiSpec].
//
// Options are applied in order. It panics on a nil option or invalid values.
func NewApiSpec(options ...ApiSpecOption) apiSpec {
	var spec apiSpec
	for _, opt := range options {
		opt(&spec)
	}
	validateApiSpec(&spec)
	return spec
}

// WithApiSpec attaches documentation metadata created by [NewApiSpec] to a
// route. It is a [RouteOption], so pass it to [App.Map] or any of the
// [App.MapGet], [App.MapPost], ... shortcuts:
//
//	app.MapGet("/users/:id", handleGetUser,
//		WithApiSpec(NewApiSpec(
//			WithSummary("Get a user"),
//			WithTags("users"),
//			WithParameter(ApiParameter{
//				Name: "id",
//				In:   ApiParamInPath,
//			}),
//			WithResponse(ApiResponse{Status: 200, Description: "OK"}),
//		)),
//	)
//
// The spec's slices and maps are copied when it is attached. Schema, Example,
// Examples and Extra values are shared as-is. Routes registered without
// WithApiSpec expose the zero spec.
func WithApiSpec(spec apiSpec) RouteOption {
	return func(re *routeEntry) {
		re.spec = cloneApiSpec(spec)
	}
}

// WithSummary sets the spec summary. Surrounding whitespace is trimmed.
func WithSummary(summary string) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.Summary = strings.TrimSpace(summary)
	}
}

// WithDescription sets the spec description. The text is stored as given, so
// it may contain markdown and surrounding whitespace.
func WithDescription(description string) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.Description = description
	}
}

// WithOperationId sets the spec operation id. Surrounding whitespace is
// trimmed.
func WithOperationId(id string) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.OperationId = strings.TrimSpace(id)
	}
}

// WithTags appends tags to the spec. Surrounding whitespace is trimmed,
// empty tags panic and duplicates are ignored. Repeated calls append.
func WithTags(tags ...string) ApiSpecOption {
	return func(spec *apiSpec) {
		for _, tag := range tags {
			spec.Tags = append(spec.Tags, strings.TrimSpace(tag))
		}
	}
}

// WithDeprecated marks the spec as deprecated.
func WithDeprecated(b bool) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.Deprecated = b
	}
}

// WithParameter appends a parameter to the spec. Repeated calls append. See
// [ApiParameter] for path parameter rules.
func WithParameter(param ApiParameter) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.Parameters = append(spec.Parameters, cloneApiParameter(param))
	}
}

// WithRequestBody sets the spec request body. Passing a body with an empty
// Content map is allowed, but documentation generators may omit the body.
func WithRequestBody(body ApiRequestBody) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.RequestBody = cloneApiRequestBody(&body)
	}
}

// WithResponse appends a response to the spec. Repeated calls append. A zero
// Status means the default response.
func WithResponse(response ApiResponse) ApiSpecOption {
	return func(spec *apiSpec) {
		spec.Responses = append(spec.Responses, cloneApiResponse(response))
	}
}

// WithSecurity appends security requirements to the spec. Repeated calls
// append. An empty requirement means authentication is optional.
func WithSecurity(requirements ...ApiSecurityRequirement) ApiSpecOption {
	return func(spec *apiSpec) {
		for _, requirement := range requirements {
			spec.Security = append(spec.Security, cloneApiSecurityRequirement(requirement))
		}
	}
}

// WithExtra sets one raw operation field or specification extension on the
// spec. Repeated calls with the same key panic during spec construction. The
// key is trimmed; the value is stored as-is.
func WithExtra(key string, value any) ApiSpecOption {
	return func(spec *apiSpec) {
		key = strings.TrimSpace(key)
		Assert(key != "", "api spec extra key cannot be empty")
		if spec.Extra == nil {
			spec.Extra = make(map[string]any)
		}
		_, duplicate := spec.Extra[key]
		Assert(!duplicate, fmt.Sprintf("duplicate api spec extra key %q", key))
		spec.Extra[key] = value
	}
}

// GetApiSpecs returns a snapshot of every registered route and its spec, in
// registration order. Every route is included, even when it has no spec;
// middlewares are not routes and are not included.
//
// The snapshot mirrors the routes registered at the time of the call. It is
// meant for documentation handlers that generate and cache a page after all
// routes are registered. Route registration is not safe concurrently with
// request handling, so do not call GetApiSpecs while registering routes.
//
// The returned slices and maps are copies, but Extra values and the
// [ApiParameter.Schema], [ApiParameter.Example], [ApiHeader.Schema],
// [ApiHeader.Example], [ApiMediaType.Schema], [ApiMediaType.Example] and
// [ApiMediaType.Examples] values are returned as-is and must be treated as
// immutable.
func (me *Context) GetApiSpecs() []ApiSpecEntry {
	return me.app.collectApiSpecs()
}

// collectApiSpecs builds the route snapshot returned by
// [Context.GetApiSpecs].
func (me *App) collectApiSpecs() []ApiSpecEntry {
	var routes []*routeEntry
	for _, methodRoutes := range me.routesPerMethod {
		routes = append(routes, methodRoutes...)
	}
	slices.SortFunc(routes, func(a, b *routeEntry) int {
		return cmp.Compare(a.order, b.order)
	})

	entries := make([]ApiSpecEntry, 0, len(routes))
	for _, route := range routes {
		entries = append(entries, ApiSpecEntry{
			Method:  route.method,
			Pattern: route.pattern,
			Spec:    cloneApiSpec(route.spec),
		})
	}
	return entries
}

// validateApiSpec normalizes and validates the route-independent parts of a
// spec. It panics on invalid values, using the same Assert style as route
// registration and spec options.
func validateApiSpec(spec *apiSpec) {
	seenTags := make(map[string]struct{}, len(spec.Tags))
	var tags []string
	for _, tag := range spec.Tags {
		tag = strings.TrimSpace(tag)
		Assert(tag != "", "api spec tag cannot be empty")
		if _, exists := seenTags[tag]; exists {
			continue
		}
		seenTags[tag] = struct{}{}
		tags = append(tags, tag)
	}
	spec.Tags = tags

	seenParams := make(map[string]struct{}, len(spec.Parameters))
	for i := range spec.Parameters {
		param := &spec.Parameters[i]
		param.Name = strings.TrimSpace(param.Name)
		param.In = strings.TrimSpace(param.In)
		param.Style = strings.TrimSpace(param.Style)
		Assert(param.Name != "", "api parameter name cannot be empty")
		switch param.In {
		case ApiParamInPath, ApiParamInQuery, ApiParamInHeader, ApiParamInCookie:
		default:
			panic(fmt.Sprintf("invalid api parameter location %q", param.In))
		}

		key := apiParameterKey(param.In, param.Name)
		_, duplicate := seenParams[key]
		Assert(!duplicate, fmt.Sprintf("duplicate api parameter %q in %q", param.Name, param.In))
		seenParams[key] = struct{}{}

		if param.In == ApiParamInPath {
			param.Required = true
		}
		if param.AllowEmptyValue {
			Assert(param.In == ApiParamInQuery, "only query parameters can allow empty values")
		}
	}

	if spec.RequestBody != nil {
		spec.RequestBody.Content = normalizeApiMediaTypes(spec.RequestBody.Content)
	}

	for i, requirement := range spec.Security {
		normalized := make(ApiSecurityRequirement, len(requirement))
		for scheme, scopes := range requirement {
			scheme = strings.TrimSpace(scheme)
			Assert(scheme != "", "api security scheme name cannot be empty")
			_, duplicate := normalized[scheme]
			Assert(!duplicate, fmt.Sprintf("duplicate api security scheme %q", scheme))
			if scopes == nil {
				scopes = []string{}
			}
			normalized[scheme] = scopes
		}
		spec.Security[i] = normalized
	}

	seenStatuses := make(map[int]struct{}, len(spec.Responses))
	for i := range spec.Responses {
		response := &spec.Responses[i]
		Assert(
			response.Status == 0 || response.Status >= 100 && response.Status <= 599,
			fmt.Sprintf("invalid api response status %d", response.Status),
		)
		_, duplicate := seenStatuses[response.Status]
		Assert(!duplicate, fmt.Sprintf("duplicate api response status %d", response.Status))
		seenStatuses[response.Status] = struct{}{}

		response.Content = normalizeApiMediaTypes(response.Content)
		response.Headers = normalizeApiHeaders(response.Headers)
	}

	if spec.Extra != nil {
		extra := make(map[string]any, len(spec.Extra))
		for key, value := range spec.Extra {
			key = strings.TrimSpace(key)
			Assert(key != "", "api spec extra key cannot be empty")
			_, duplicate := extra[key]
			Assert(!duplicate, fmt.Sprintf("duplicate api spec extra key %q", key))
			extra[key] = value
		}
		spec.Extra = extra
	}
}

// validateApiSpecPathParams checks the path parameters of spec against the
// route pattern and forces them to be required.
func validateApiSpecPathParams(spec *apiSpec, segments []Segment) {
	routeParams := routeParamNameSet(segments)
	for i := range spec.Parameters {
		param := &spec.Parameters[i]
		if param.In != ApiParamInPath {
			continue
		}
		_, exists := routeParams[param.Name]
		Assert(
			exists,
			fmt.Sprintf("path parameter %q does not exist in the route pattern", param.Name),
		)
		param.Required = true
	}
}

// apiParameterKey returns the uniqueness key of a parameter. Header names are
// case-insensitive, so they are lowercased for the comparison.
func apiParameterKey(in, name string) string {
	if in == ApiParamInHeader {
		name = strings.ToLower(name)
	}
	return in + "\x00" + name
}

// routeParamNameSet returns every path parameter name a route can capture,
// matching the names used by [Context.GetParam]: plain and optional names as
// written, "*" or "*1", "*2", ... for wildcards, and "+" or "+1", "+2", ...
// for plus parts.
func routeParamNameSet(segments []Segment) map[string]struct{} {
	wildcards, pluses := 0, 0
	for _, segment := range segments {
		for _, part := range segment {
			switch part.Kind {
			case PartWildcard:
				wildcards++
			case PartPlus:
				pluses++
			}
		}
	}

	names := make(map[string]struct{})
	wildcardIndex, plusIndex := 0, 0
	for _, segment := range segments {
		for _, part := range segment {
			switch part.Kind {
			case PartParam, PartOptional:
				names[part.Text] = struct{}{}
			case PartWildcard:
				wildcardIndex++
				key := "*"
				if wildcards > 1 {
					key = "*" + strconv.Itoa(wildcardIndex)
				}
				names[key] = struct{}{}
			case PartPlus:
				plusIndex++
				key := "+"
				if pluses > 1 {
					key = "+" + strconv.Itoa(plusIndex)
				}
				names[key] = struct{}{}
			}
		}
	}
	return names
}

// normalizeApiMediaTypes trims media type keys and rejects empty keys,
// duplicates that only differ by surrounding whitespace, and media types that
// set both Example and Examples.
func normalizeApiMediaTypes(in map[string]ApiMediaType) map[string]ApiMediaType {
	if in == nil {
		return nil
	}
	out := make(map[string]ApiMediaType, len(in))
	for mediaType, value := range in {
		mediaType = strings.TrimSpace(mediaType)
		Assert(mediaType != "", "api media type cannot be empty")
		_, duplicate := out[mediaType]
		Assert(!duplicate, fmt.Sprintf("duplicate api media type %q", mediaType))
		Assert(
			value.Example == nil || len(value.Examples) == 0,
			fmt.Sprintf("api media type %q cannot set both example and examples", mediaType),
		)
		out[mediaType] = value
	}
	return out
}

// normalizeApiHeaders trims header names and rejects empty names, names that
// only differ by case, and styles other than "simple".
func normalizeApiHeaders(in map[string]ApiHeader) map[string]ApiHeader {
	if in == nil {
		return nil
	}
	out := make(map[string]ApiHeader, len(in))
	seen := make(map[string]struct{}, len(in))
	for name, value := range in {
		name = strings.TrimSpace(name)
		value.Style = strings.TrimSpace(value.Style)
		Assert(name != "", "api header name cannot be empty")
		lower := strings.ToLower(name)
		_, duplicate := seen[lower]
		Assert(!duplicate, fmt.Sprintf("duplicate api header %q", name))
		seen[lower] = struct{}{}
		Assert(
			value.Style == "" || value.Style == "simple",
			fmt.Sprintf("api header %q style must be simple", name),
		)
		out[name] = value
	}
	return out
}

func cloneApiParameter(in ApiParameter) ApiParameter {
	out := in
	if in.Explode != nil {
		value := *in.Explode
		out.Explode = &value
	}
	return out
}

func cloneApiHeader(in ApiHeader) ApiHeader {
	out := in
	if in.Explode != nil {
		value := *in.Explode
		out.Explode = &value
	}
	return out
}

func cloneApiHeaders(in map[string]ApiHeader) map[string]ApiHeader {
	if in == nil {
		return nil
	}
	out := make(map[string]ApiHeader, len(in))
	for name, header := range in {
		out[name] = cloneApiHeader(header)
	}
	return out
}

func cloneApiMediaType(in ApiMediaType) ApiMediaType {
	out := in
	out.Examples = maps.Clone(in.Examples)
	return out
}

func cloneApiMediaTypes(in map[string]ApiMediaType) map[string]ApiMediaType {
	if in == nil {
		return nil
	}
	out := make(map[string]ApiMediaType, len(in))
	for mediaType, value := range in {
		out[mediaType] = cloneApiMediaType(value)
	}
	return out
}

func cloneApiRequestBody(in *ApiRequestBody) *ApiRequestBody {
	if in == nil {
		return nil
	}
	out := *in
	out.Content = cloneApiMediaTypes(in.Content)
	return &out
}

func cloneApiResponse(in ApiResponse) ApiResponse {
	out := in
	out.Content = cloneApiMediaTypes(in.Content)
	out.Headers = cloneApiHeaders(in.Headers)
	return out
}

func cloneApiSecurityRequirement(in ApiSecurityRequirement) ApiSecurityRequirement {
	if in == nil {
		return nil
	}
	out := make(ApiSecurityRequirement, len(in))
	for scheme, scopes := range in {
		out[scheme] = slices.Clone(scopes)
	}
	return out
}

func cloneApiSpec(in apiSpec) apiSpec {
	out := in
	out.Tags = slices.Clone(in.Tags)
	if in.Parameters != nil {
		out.Parameters = make([]ApiParameter, len(in.Parameters))
		for i, parameter := range in.Parameters {
			out.Parameters[i] = cloneApiParameter(parameter)
		}
	}
	out.RequestBody = cloneApiRequestBody(in.RequestBody)
	if in.Responses != nil {
		out.Responses = make([]ApiResponse, len(in.Responses))
		for i, response := range in.Responses {
			out.Responses[i] = cloneApiResponse(response)
		}
	}
	if in.Security != nil {
		out.Security = make([]ApiSecurityRequirement, len(in.Security))
		for i, requirement := range in.Security {
			out.Security[i] = cloneApiSecurityRequirement(requirement)
		}
	}
	out.Extra = maps.Clone(in.Extra)
	return out
}
