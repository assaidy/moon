// Package swagger serves an OpenAPI document generated from a moon app's
// route specs, together with a Swagger UI page that renders it.
//
// Register [Handler.Handle] on a wildcard route:
//
//	app.MapGet("/swagger/*", swagger.New(
//		swagger.WithTitle("My API"),
//		swagger.WithVersion("1.0.0"),
//	).Handle)
//
// To use a custom mount, pass [WithMountPath] and use the same path in the
// wildcard route:
//
//	app.MapGet("/docs/*", swagger.New(
//		swagger.WithMountPath("/docs"),
//		swagger.WithTitle("My API"),
//	).Handle)
//
// The UI is served at the mount path ("/swagger" by default) or at
// "<mount>/index.html"; the document is served at "<mount>/openapi.json". The
// document follows OpenAPI 3.2.1 and uses Swagger UI 5.34.0 from unpkg by
// default; override the asset URLs with [WithUiAssets] to self-host them.
//
// Optional path parameters are expanded into separate paths, up to eight
// variants per route; a route with more optional parameters is documented only
// with every optional parameter present. Routes that describe the same path
// template with different parameter names are emitted as separate Path Items;
// use the same parameter names for a path shape to keep the document valid.
//
// The handler builds the document and the UI on first use and caches them.
// Register every route before the first request, including the Swagger route
// itself, then let the handler cache the complete API.
package swagger

import (
	"bytes"
	"fmt"
	"html/template"
	"maps"
	"math/bits"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/assaidy/moon"
)

const (
	// OpenApiVersion is the OpenAPI Specification version written to the
	// generated document.
	OpenApiVersion = "3.2.1"
	// DefaultMountPath is the path the handler serves the UI on by default.
	DefaultMountPath = "/swagger"
	// SpecFileName is the file name of the generated OpenAPI document below
	// the mount path.
	SpecFileName = "openapi.json"
)

// swaggerUiVersion is the Swagger UI release used by the default asset URLs.
const swaggerUiVersion = "5.34.0"

// maxOptionalVariants caps how many path variants one route may expand to for
// optional path parameters. If every combination fits within the cap, all of
// them are generated; otherwise only the variant with every optional parameter
// present is generated.
const maxOptionalVariants = 8

// Handler serves an OpenAPI document and the Swagger UI page rendering it.
// Use [New] to construct it, passing [Option]s to configure it, then register
// [Handler.Handle] on a wildcard route.
type Handler struct {
	options options

	specOnce sync.Once
	specJson []byte
	specErr  error

	uiOnce sync.Once
	uiHtml []byte
	uiErr  error
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	mountPath             string
	caseSensitiveMatching bool
	title                 string
	version               string
	description           string
	serverUrl             string
	components            map[string]any
	uiCssUrl              string
	uiJsUrl               string
}

// New returns a handler built from sensible defaults, customized by the given
// options. Register [Handler.Handle] on a wildcard route:
//
//	app.MapGet("/swagger/*", swagger.New(
//		swagger.WithTitle("My API"),
//		swagger.WithVersion("1.0.0"),
//	).Handle)
//
// The default mount path is [DefaultMountPath]. The generated document is
// built on the first request and cached, so register all routes first. Use one
// Handler per [moon.App]: the cache belongs to the handler, not the app.
func New(opts ...Option) *Handler {
	options := options{
		mountPath: DefaultMountPath,
		title:     "API Documentation",
		version:   "1.0.0",
	}
	options.uiCssUrl, options.uiJsUrl = defaultUiAssets()
	for _, opt := range opts {
		opt(&options)
	}
	return &Handler{options: options}
}

// Option configures a [Handler]. Pass them to [New].
type Option func(*options)

// WithMountPath sets the path the handler is mounted on. A missing leading
// slash is added, and the path is cleaned. Letter case is preserved; matching
// follows [WithCaseSensitiveMountMatching]. The UI is served at the mount path and
// at "<mount>/index.html"; the OpenAPI document is served at
// "<mount>/openapi.json". Register the handler with a wildcard so those paths
// reach it:
//
//	app.MapGet("/docs/*", swagger.New(swagger.WithMountPath("/docs")).Handle)
//
// Default: [DefaultMountPath]
func WithMountPath(mountPath string) Option {
	mountPath = strings.TrimSpace(mountPath)
	moon.Assert(mountPath != "", "swagger mount path cannot be empty")
	if !strings.HasPrefix(mountPath, "/") {
		mountPath = "/" + mountPath
	}
	mountPath = path.Clean(mountPath)

	return func(o *options) {
		o.mountPath = mountPath
	}
}

// WithCaseSensitiveMountMatching determines whether request paths are matched
// against the mount path case-sensitively. It applies to the whole mounted
// path, including "<mount>/index.html" and "<mount>/openapi.json". Pass it to
// [New].
//
// Default: false (case-insensitive matching)
func WithCaseSensitiveMountMatching(b bool) Option {
	return func(o *options) {
		o.caseSensitiveMatching = b
	}
}

// WithTitle sets the document title and the Swagger UI page title.
// Surrounding whitespace is trimmed; it panics on an empty title.
//
// Default: "API Documentation"
func WithTitle(title string) Option {
	title = strings.TrimSpace(title)
	moon.Assert(title != "", "swagger title cannot be empty")

	return func(o *options) {
		o.title = title
	}
}

// WithVersion sets the API document version written to the OpenAPI info
// object. It is the version of the described API, not the OpenAPI version.
// Surrounding whitespace is trimmed; it panics on an empty version.
//
// Default: "1.0.0"
func WithVersion(version string) Option {
	version = strings.TrimSpace(version)
	moon.Assert(version != "", "swagger version cannot be empty")

	return func(o *options) {
		o.version = version
	}
}

// WithDescription sets the description of the API in the OpenAPI info object.
// The text is stored as given and may contain markdown.
//
// Default: empty
func WithDescription(description string) Option {
	return func(o *options) {
		o.description = description
	}
}

// WithServerUrl adds a server URL to the OpenAPI document. It may be absolute
// or relative. When unset, no servers array is emitted and Swagger UI uses the
// origin serving the document.
//
// Default: empty
func WithServerUrl(serverUrl string) Option {
	serverUrl = strings.TrimSpace(serverUrl)
	moon.Assert(serverUrl != "", "swagger server URL cannot be empty")

	return func(o *options) {
		o.serverUrl = serverUrl
	}
}

// WithComponents sets the OpenAPI components object, used for reusable
// schemas, security schemes, parameters, responses and similar definitions.
// The map is shallow-cloned; nested values are stored as-is.
//
// Default: empty (no components object)
func WithComponents(components map[string]any) Option {
	return func(o *options) {
		o.components = maps.Clone(components)
	}
}

// WithUiAssets overrides the Swagger UI stylesheet and script URLs. Use it to
// self-host Swagger UI instead of loading it from unpkg. Both URLs must be
// non-empty.
//
// Default: Swagger UI 5.34.0 from unpkg.
func WithUiAssets(cssUrl, jsUrl string) Option {
	jsUrl = strings.TrimSpace(jsUrl)
	cssUrl = strings.TrimSpace(cssUrl)
	moon.Assert(cssUrl != "", "swagger UI CSS URL cannot be empty")
	moon.Assert(jsUrl != "", "swagger UI JavaScript URL cannot be empty")

	return func(o *options) {
		o.uiCssUrl = cssUrl
		o.uiJsUrl = jsUrl
	}
}

// Handle serves the Swagger UI page or the OpenAPI document, depending on the
// request path. Unknown paths call [moon.Context.Next], so the handler can also
// be used as middleware; when no handler follows, Next returns [moon.ErrNotFound].
func (me *Handler) Handle(ctx *moon.Context) error {
	suffix, ok := me.suffix(ctx.GetPath())
	if !ok {
		return ctx.Next()
	}
	switch {
	case suffix == "" || me.pathMatches(suffix, "/index.html"):
		return me.writeUi(ctx)
	case me.pathMatches(suffix, "/"+SpecFileName):
		return me.writeSpec(ctx)
	default:
		return ctx.Next()
	}
}

// suffix returns the part of requestPath below the mount path, or false when
// the path is outside the mount. The mount comparison follows
// [WithCaseSensitiveMountMatching].
func (me *Handler) suffix(requestPath string) (string, bool) {
	mountPath := me.options.mountPath
	if mountPath == "/" {
		if requestPath == "/" {
			return "", true
		}
		return requestPath, strings.HasPrefix(requestPath, "/")
	}
	if me.pathMatches(requestPath, mountPath) {
		return "", true
	}
	prefix := mountPath + "/"
	if len(requestPath) >= len(prefix) && me.pathMatches(requestPath[:len(prefix)], prefix) {
		return requestPath[len(mountPath):], true
	}
	return "", false
}

// pathMatches reports whether a and b are equal, ignoring letter case unless
// [WithCaseSensitiveMountMatching] is enabled.
func (me *Handler) pathMatches(a, b string) bool {
	if me.options.caseSensitiveMatching {
		return a == b
	}
	return strings.EqualFold(a, b)
}

func (me *Handler) writeUi(ctx *moon.Context) error {
	me.uiOnce.Do(func() {
		me.uiHtml, me.uiErr = me.buildUi()
	})
	if me.uiErr != nil {
		return me.uiErr
	}
	ctx.SetHeader("Content-Type", "text/html; charset=utf-8")
	return ctx.Write(http.StatusOK, me.uiHtml)
}

func (me *Handler) writeSpec(ctx *moon.Context) error {
	me.specOnce.Do(func() {
		me.specJson, me.specErr = me.buildSpecJson(ctx.GetApiSpecs())
	})
	if me.specErr != nil {
		return me.specErr
	}
	ctx.SetHeader("Content-Type", "application/json; charset=utf-8")
	return ctx.Write(http.StatusOK, me.specJson)
}

func (me *Handler) buildUi() ([]byte, error) {
	var buffer bytes.Buffer
	err := uiTemplate.Execute(&buffer, struct {
		Title   string
		CssUrl  string
		JsUrl   string
		SpecUrl string
	}{
		Title:   me.options.title,
		CssUrl:  me.options.uiCssUrl,
		JsUrl:   me.options.uiJsUrl,
		SpecUrl: path.Join(me.options.mountPath, SpecFileName),
	})
	if err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (me *Handler) buildSpecJson(entries []moon.ApiSpecEntry) ([]byte, error) {
	document, err := me.buildDocument(entries)
	if err != nil {
		return nil, err
	}
	data, err := marshalJson(document)
	if err != nil {
		return nil, fmt.Errorf("swagger: encode OpenAPI document: %w", err)
	}
	return data, nil
}

func (me *Handler) buildDocument(entries []moon.ApiSpecEntry) (openApiDocument, error) {
	document := openApiDocument{
		OpenApi: OpenApiVersion,
		Info: openApiInfo{
			Title:       me.options.title,
			Description: me.options.description,
			Version:     me.options.version,
		},
		Paths:      make(map[string]map[string]any),
		Components: maps.Clone(me.options.components),
	}
	if me.options.serverUrl != "" {
		document.Servers = []openApiServer{{Url: me.options.serverUrl}}
	}

	for _, entry := range entries {
		segments, _, err := moon.ParsePattern(entry.Pattern)
		if err != nil {
			return openApiDocument{}, fmt.Errorf(
				"swagger: invalid route pattern %q: %w",
				entry.Pattern,
				err,
			)
		}
		for _, omitted := range optionalVariants(segments) {
			operationId := variantOperationId(entry.Spec.OperationId, omitted)
			addOperation(
				document.Paths,
				entry.Method,
				openApiPath(segments, omitted),
				buildOperation(entry, segments, omitted, operationId),
			)
		}
	}
	return document, nil
}

// optionalVariants returns combinations of present and omitted optional path
// parameters, capped by maxOptionalVariants. The nil map means every optional
// parameter is present.
func optionalVariants(segments []moon.Segment) []map[string]bool {
	var names []string
	for _, segment := range segments {
		if len(segment) == 1 && segment[0].Kind == moon.PartOptional {
			names = append(names, segment[0].Text)
		}
	}
	if len(names) == 0 {
		return []map[string]bool{nil}
	}
	if len(names) >= bits.Len(uint(maxOptionalVariants)) {
		return []map[string]bool{nil}
	}

	variants := make([]map[string]bool, 0, 1<<len(names))
	for mask := 0; mask < 1<<len(names); mask++ {
		if mask == 0 {
			variants = append(variants, nil)
			continue
		}
		omitted := make(map[string]bool, len(names))
		for i, name := range names {
			if mask&(1<<i) != 0 {
				omitted[name] = true
			}
		}
		variants = append(variants, omitted)
	}
	return variants
}

// openApiPath converts the parsed moon pattern to OpenAPI path templating:
// parameter parts become "{name}", wildcard and plus parts get their capture
// names ("*", "*1", "+", ...), and omitted optional segments are dropped.
func openApiPath(segments []moon.Segment, omitted map[string]bool) string {
	wildcards, pluses := countWildcardParts(segments)

	var builder strings.Builder
	wildcardIndex, plusIndex := 0, 0
	for _, segment := range segments {
		if len(segment) == 1 && segment[0].Kind == moon.PartOptional && omitted[segment[0].Text] {
			continue
		}
		builder.WriteByte('/')
		for _, part := range segment {
			switch part.Kind {
			case moon.PartLiteral:
				builder.WriteString(part.Text)
			case moon.PartWildcard:
				wildcardIndex++
				builder.WriteString("{" + wildcardName(wildcardIndex, wildcards) + "}")
			case moon.PartPlus:
				plusIndex++
				builder.WriteString("{" + plusName(plusIndex, pluses) + "}")
			default:
				builder.WriteString("{" + part.Text + "}")
			}
		}
	}
	if builder.Len() == 0 {
		return "/"
	}
	return builder.String()
}

func countWildcardParts(segments []moon.Segment) (wildcards, pluses int) {
	for _, segment := range segments {
		for _, part := range segment {
			switch part.Kind {
			case moon.PartWildcard:
				wildcards++
			case moon.PartPlus:
				pluses++
			}
		}
	}
	return wildcards, pluses
}

func wildcardName(index, total int) string {
	if total > 1 {
		return "*" + strconv.Itoa(index)
	}
	return "*"
}

func plusName(index, total int) string {
	if total > 1 {
		return "+" + strconv.Itoa(index)
	}
	return "+"
}

func variantOperationId(base string, omitted map[string]bool) string {
	if base == "" || len(omitted) == 0 {
		return base
	}
	names := make([]string, 0, len(omitted))
	for name := range omitted {
		names = append(names, name)
	}
	slices.Sort(names)
	return base + "_without_" + strings.Join(names, "_and_")
}

func addOperation(paths map[string]map[string]any, method, routePath string, operation map[string]any) {
	pathItem := paths[routePath]
	if pathItem == nil {
		pathItem = make(map[string]any)
		paths[routePath] = pathItem
	}

	if method != moon.MethodConnect {
		key := strings.ToLower(method)
		if _, exists := pathItem[key]; exists {
			return
		}
		pathItem[key] = operation
		return
	}

	additional, _ := pathItem["additionalOperations"].(map[string]any)
	if additional == nil {
		additional = make(map[string]any)
		pathItem["additionalOperations"] = additional
	}
	if _, exists := additional[method]; exists {
		return
	}
	additional[method] = operation
}

func buildOperation(
	entry moon.ApiSpecEntry,
	segments []moon.Segment,
	omitted map[string]bool,
	operationId string,
) map[string]any {
	spec := entry.Spec
	operation := make(map[string]any, 12)
	if spec.Summary != "" {
		operation["summary"] = spec.Summary
	}
	if spec.Description != "" {
		operation["description"] = spec.Description
	}
	if operationId != "" {
		operation["operationId"] = operationId
	}
	if len(spec.Tags) > 0 {
		operation["tags"] = spec.Tags
	}
	if spec.Deprecated {
		operation["deprecated"] = true
	}
	if parameters := buildParameters(spec.Parameters, segments, omitted); len(parameters) > 0 {
		operation["parameters"] = parameters
	}
	if requestBody := buildRequestBody(spec.RequestBody); requestBody != nil {
		operation["requestBody"] = requestBody
	}
	operation["responses"] = buildResponses(spec.Responses)
	if len(spec.Security) > 0 {
		operation["security"] = spec.Security
	}
	for key, value := range spec.Extra {
		operation[key] = value
	}
	return operation
}

func buildParameters(
	parameters []moon.ApiParameter,
	segments []moon.Segment,
	omitted map[string]bool,
) []openApiParameter {
	out := make([]openApiParameter, 0, len(parameters))
	seen := make(map[string]struct{}, len(parameters))
	for _, parameter := range parameters {
		if parameter.In == moon.ApiParamInPath && omitted[parameter.Name] {
			continue
		}
		required := parameter.Required
		if parameter.In == moon.ApiParamInPath {
			required = true
		}
		out = append(out, openApiParameter{
			Name:            parameter.Name,
			In:              parameter.In,
			Description:     parameter.Description,
			Required:        required,
			Deprecated:      parameter.Deprecated,
			Style:           parameter.Style,
			Explode:         parameter.Explode,
			AllowEmptyValue: parameter.AllowEmptyValue,
			AllowReserved:   parameter.AllowReserved,
			Schema:          schemaOrDefault(parameter.Schema),
			Example:         parameter.Example,
		})
		seen[parameterKey(parameter.In, parameter.Name)] = struct{}{}
	}

	for _, name := range templateParameterNames(segments, omitted) {
		key := parameterKey(moon.ApiParamInPath, name)
		if _, exists := seen[key]; exists {
			continue
		}
		out = append(out, openApiParameter{
			Name:     name,
			In:       moon.ApiParamInPath,
			Required: true,
			Schema:   map[string]any{"type": "string"},
		})
		seen[key] = struct{}{}
	}
	return out
}

func parameterKey(in, name string) string {
	if in == moon.ApiParamInHeader {
		name = strings.ToLower(name)
	}
	return in + "\x00" + name
}

// schemaOrDefault returns schema, or a string schema when it is nil. An
// OpenAPI parameter or header must contain either a schema or content, and the
// moon metadata model only supports schema.
func schemaOrDefault(schema any) any {
	if schema != nil {
		return schema
	}
	return map[string]any{"type": "string"}
}

func templateParameterNames(segments []moon.Segment, omitted map[string]bool) []string {
	wildcards, pluses := countWildcardParts(segments)

	var names []string
	wildcardIndex, plusIndex := 0, 0
	for _, segment := range segments {
		for _, part := range segment {
			switch part.Kind {
			case moon.PartParam:
				names = append(names, part.Text)
			case moon.PartOptional:
				if !omitted[part.Text] {
					names = append(names, part.Text)
				}
			case moon.PartWildcard:
				wildcardIndex++
				names = append(names, wildcardName(wildcardIndex, wildcards))
			case moon.PartPlus:
				plusIndex++
				names = append(names, plusName(plusIndex, pluses))
			}
		}
	}
	return names
}

func buildRequestBody(body *moon.ApiRequestBody) *openApiRequestBody {
	if body == nil || len(body.Content) == 0 {
		return nil
	}
	return &openApiRequestBody{
		Description: body.Description,
		Required:    body.Required,
		Content:     buildContent(body.Content),
	}
}

func buildContent(content map[string]moon.ApiMediaType) map[string]openApiMediaType {
	if len(content) == 0 {
		return nil
	}
	out := make(map[string]openApiMediaType, len(content))
	for mediaType, value := range content {
		out[mediaType] = openApiMediaType{
			Schema:   value.Schema,
			Example:  value.Example,
			Examples: value.Examples,
		}
	}
	return out
}

func buildResponses(responses []moon.ApiResponse) map[string]any {
	if len(responses) == 0 {
		return map[string]any{
			"default": openApiResponse{Description: "Undocumented response."},
		}
	}

	out := make(map[string]any, len(responses))
	for _, response := range responses {
		key := "default"
		description := response.Description
		if response.Status != 0 {
			key = strconv.Itoa(response.Status)
			if description == "" {
				description = http.StatusText(response.Status)
			}
		}
		if description == "" {
			if response.Status == 0 {
				description = "Default response."
			} else {
				description = "Response."
			}
		}

		built := openApiResponse{Description: description}
		if len(response.Content) > 0 {
			built.Content = buildContent(response.Content)
		}
		if len(response.Headers) > 0 {
			built.Headers = buildHeaders(response.Headers)
		}
		out[key] = built
	}
	return out
}

func buildHeaders(headers map[string]moon.ApiHeader) map[string]openApiHeader {
	out := make(map[string]openApiHeader, len(headers))
	for name, header := range headers {
		out[name] = openApiHeader{
			Description: header.Description,
			Required:    header.Required,
			Deprecated:  header.Deprecated,
			Style:       header.Style,
			Explode:     header.Explode,
			Schema:      schemaOrDefault(header.Schema),
			Example:     header.Example,
		}
	}
	return out
}

func defaultUiAssets() (cssUrl, jsUrl string) {
	base := "https://unpkg.com/swagger-ui-dist@" + swaggerUiVersion
	return base + "/swagger-ui.css", base + "/swagger-ui-bundle.js"
}

var uiTemplate = template.Must(template.New("swagger-ui").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}}</title>
  <link rel="stylesheet" href="{{.CssUrl}}">
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="{{.JsUrl}}"></script>
  <script>
    window.addEventListener("load", function () {
      window.ui = SwaggerUIBundle({
        url: {{.SpecUrl}},
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis],
        layout: "BaseLayout"
      });
    });
  </script>
</body>
</html>
`))
