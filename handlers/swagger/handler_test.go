package swagger

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/assaidy/moon"
	"github.com/stretchr/testify/require"
)

func TestHandle_ServesUi(t *testing.T) {
	app := moon.New()
	app.MapGet("/swagger/*", New(
		WithTitle("Test API"),
		WithVersion("2.3.4"),
	).Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/swagger", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	body := readBody(t, resp)
	require.Contains(t, body, "Test API")
	require.Contains(t, body, "SwaggerUIBundle")
	require.Contains(t, body, `"/swagger/openapi.json"`)
	require.Contains(t, body, "swagger-ui.css")
	require.Contains(t, body, "swagger-ui-bundle.js")

	resp = app.Test(httptest.NewRequest(http.MethodGet, "/swagger/index.html", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")

	body = readBody(t, resp)
	require.Contains(t, body, "Test API")
	require.Contains(t, body, `"/swagger/openapi.json"`)
}

func TestHandle_ServesSpec(t *testing.T) {
	app := moon.New()
	explode := true
	app.MapGet("/users/:id", func(ctx *moon.Context) error {
		return nil
	},
		moon.WithApiSpec(moon.NewApiSpec(moon.WithSummary("Get a user"),
			moon.WithDescription("Returns one user."),
			moon.WithOperationId("getUser"),
			moon.WithTags("users"),
			moon.WithDeprecated(true),
			moon.WithParameter(moon.ApiParameter{
				Name:        "id",
				In:          moon.ApiParamInPath,
				Description: "User id.",
				Schema:      moon.Object{"type": "string"},
			}),
			moon.WithParameter(moon.ApiParameter{
				Name:            "verbose",
				In:              moon.ApiParamInQuery,
				Required:        true,
				Style:           "form",
				Explode:         &explode,
				AllowEmptyValue: true,
				AllowReserved:   true,
				Schema:          moon.Object{"type": "boolean"},
				Example:         false,
			}),
			moon.WithParameter(moon.ApiParameter{
				Name: "session",
				In:   moon.ApiParamInCookie,
			}),
			moon.WithRequestBody(moon.ApiRequestBody{
				Description: "Filter.",
				Required:    true,
				Content: map[string]moon.ApiMediaType{
					"application/json": {
						Schema: moon.Object{"type": "object"},
					},
				},
			}),
			moon.WithResponse(moon.ApiResponse{
				Status:      http.StatusOK,
				Description: "The user.",
				Content: map[string]moon.ApiMediaType{
					"application/json": {Schema: moon.Object{"type": "object"}},
				},
				Headers: map[string]moon.ApiHeader{
					"X-Request-Id": {
						Description: "Request id.",
						Schema:      moon.Object{"type": "string"},
					},
				},
			}),
			moon.WithResponse(moon.ApiResponse{
				Status:      0,
				Description: "Error response.",
			}),
			moon.WithSecurity(moon.ApiSecurityRequirement{"bearerAuth": {"read:users"}}),
			moon.WithExtra("x-internal", true)),
		),
	)
	app.MapGet("/swagger/*", New(
		WithTitle("Test API"),
		WithVersion("2.3.4"),
		WithDescription("The test API."),
		WithServerUrl("https://api.example.com"),
		WithComponents(map[string]any{
			"securitySchemes": map[string]any{
				"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
			},
		}),
	).Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), "application/json")

	document := decodeDocument(t, readBody(t, resp))
	require.Equal(t, OpenApiVersion, document.OpenApi)
	require.Equal(t, "Test API", document.Info.Title)
	require.Equal(t, "The test API.", document.Info.Description)
	require.Equal(t, "2.3.4", document.Info.Version)
	require.Len(t, document.Servers, 1)
	require.Equal(t, "https://api.example.com", document.Servers[0].Url)
	require.Contains(t, document.Components, "securitySchemes")

	pathItem, exists := document.Paths["/users/{id}"]
	require.True(t, exists)
	operation := asMap(t, pathItem["get"])

	require.Equal(t, "Get a user", operation["summary"])
	require.Equal(t, "Returns one user.", operation["description"])
	require.Equal(t, "getUser", operation["operationId"])
	require.Equal(t, []any{"users"}, operation["tags"])
	require.Equal(t, true, operation["deprecated"])
	require.Equal(t, true, operation["x-internal"])

	parameters := asSlice(t, operation["parameters"])
	require.Len(t, parameters, 3)
	require.Equal(t, map[string]any{
		"name":        "id",
		"in":          "path",
		"description": "User id.",
		"required":    true,
		"schema":      map[string]any{"type": "string"},
	}, asMap(t, parameters[0]))
	require.Equal(t, map[string]any{
		"name":            "verbose",
		"in":              "query",
		"required":        true,
		"style":           "form",
		"explode":         true,
		"allowEmptyValue": true,
		"allowReserved":   true,
		"schema":          map[string]any{"type": "boolean"},
		"example":         false,
	}, asMap(t, parameters[1]))
	require.Equal(t, map[string]any{
		"name":   "session",
		"in":     "cookie",
		"schema": map[string]any{"type": "string"},
	}, asMap(t, parameters[2]))

	requestBody := asMap(t, operation["requestBody"])
	require.Equal(t, "Filter.", requestBody["description"])
	require.Equal(t, true, requestBody["required"])
	require.Contains(t, asMap(t, requestBody["content"]), "application/json")

	responses := asMap(t, operation["responses"])
	require.Contains(t, responses, "200")
	require.Contains(t, responses, "default")

	okResponse := asMap(t, responses["200"])
	require.Equal(t, "The user.", okResponse["description"])
	require.Contains(t, asMap(t, okResponse["headers"]), "X-Request-Id")
	require.Contains(t, asMap(t, okResponse["content"]), "application/json")

	require.Equal(t, "Error response.", asMap(t, responses["default"])["description"])
	require.Equal(t, []any{
		map[string]any{"bearerAuth": []any{"read:users"}},
	}, operation["security"])
}

func TestHandle_InfersPathParameters(t *testing.T) {
	app := moon.New()
	app.MapGet("/users/:id/posts/:post_id", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)
	operation := asMap(t, document.Paths["/users/{id}/posts/{post_id}"]["get"])

	parameters := asSlice(t, operation["parameters"])
	require.Len(t, parameters, 2)
	for i, want := range []string{"id", "post_id"} {
		parameter := asMap(t, parameters[i])
		require.Equal(t, want, parameter["name"])
		require.Equal(t, "path", parameter["in"])
		require.Equal(t, true, parameter["required"])
		require.Equal(t, map[string]any{"type": "string"}, parameter["schema"])
	}
}

func TestHandle_OptionalPathExpansion(t *testing.T) {
	app := moon.New()
	app.MapGet("/user/:name?", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithOperationId("getUser"),
		moon.WithResponse(moon.ApiResponse{Status: 200})),
	))
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)

	withName := asMap(t, document.Paths["/user/{name}"]["get"])
	require.Equal(t, "getUser", withName["operationId"])
	parameters := asSlice(t, withName["parameters"])
	require.Len(t, parameters, 1)
	require.Equal(t, "name", asMap(t, parameters[0])["name"])
	require.Equal(t, true, asMap(t, parameters[0])["required"])

	withoutName := asMap(t, document.Paths["/user"]["get"])
	require.Equal(t, "getUser_without_name", withoutName["operationId"])
	require.NotContains(t, withoutName, "parameters")
}

func TestHandle_OptionalPathExpansionAtCap(t *testing.T) {
	app := moon.New()
	app.MapGet("/user/:a?/:b?/:c?", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)

	for _, path := range []string{
		"/user",
		"/user/{a}",
		"/user/{b}",
		"/user/{c}",
		"/user/{a}/{b}",
		"/user/{a}/{c}",
		"/user/{b}/{c}",
		"/user/{a}/{b}/{c}",
	} {
		require.Contains(t, document.Paths, path)
	}
}

func TestHandle_OptionalPathExpansionIsCapped(t *testing.T) {
	app := moon.New()
	app.MapGet("/user/:a?/:b?/:c?/:d?", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)

	require.Contains(t, document.Paths, "/user/{a}/{b}/{c}/{d}")
	require.NotContains(t, document.Paths, "/user")
	require.NotContains(t, document.Paths, "/user/{a}")
	require.NotContains(t, document.Paths, "/user/{a}/{b}")
	require.NotContains(t, document.Paths, "/user/{a}/{b}/{c}")
}

func TestHandle_WildcardAndPlusPaths(t *testing.T) {
	app := moon.New()
	app.MapGet("/files/*", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	app.MapGet("/proxy/+", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)

	files := asMap(t, document.Paths["/files/{*}"]["get"])
	parameters := asSlice(t, files["parameters"])
	require.Len(t, parameters, 1)
	require.Equal(t, "*", asMap(t, parameters[0])["name"])
	require.Equal(t, "path", asMap(t, parameters[0])["in"])

	proxy := asMap(t, document.Paths["/proxy/{+}"]["get"])
	parameters = asSlice(t, proxy["parameters"])
	require.Len(t, parameters, 1)
	require.Equal(t, "+", asMap(t, parameters[0])["name"])
	require.Equal(t, "path", asMap(t, parameters[0])["in"])
}

func TestHandle_MethodsAndConnect(t *testing.T) {
	app := moon.New()
	for _, register := range []func(pattern string, handler moon.Handler, options ...moon.RouteOption){
		app.MapGet,
		app.MapPost,
		app.MapQuery,
		app.MapTrace,
	} {
		register("/x", func(ctx *moon.Context) error {
			return nil
		}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	}
	app.MapConnect("/connect", func(ctx *moon.Context) error {
		return nil
	}, moon.WithApiSpec(moon.NewApiSpec(moon.WithResponse(moon.ApiResponse{Status: 200}))))
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)

	x := document.Paths["/x"]
	require.Contains(t, x, "get")
	require.Contains(t, x, "post")
	require.Contains(t, x, "query")
	require.Contains(t, x, "trace")

	connect := document.Paths["/connect"]
	additional := asMap(t, connect["additionalOperations"])
	require.Contains(t, additional, "CONNECT")
}

func TestHandle_NoSpecUsesDefaultResponse(t *testing.T) {
	app := moon.New()
	app.MapGet("/plain", func(ctx *moon.Context) error {
		return nil
	})
	app.MapGet("/swagger/*", New().Handle)

	document := getDocument(t, app)

	operation := asMap(t, document.Paths["/plain"]["get"])
	responses := asMap(t, operation["responses"])
	require.Equal(t, "Undocumented response.", asMap(t, responses["default"])["description"])
}

func TestHandle_UnknownPathNotFound(t *testing.T) {
	app := moon.New()
	app.MapGet("/swagger/*", New().Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/swagger/nope", nil))
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandle_PassesUnknownPathsToNext(t *testing.T) {
	setups := []struct {
		name string
		use  func(*moon.App, moon.Handler)
	}{
		{"Use", func(app *moon.App, handler moon.Handler) {
			app.Use("/swagger", handler)
		}},
		{"UseAll", func(app *moon.App, handler moon.Handler) {
			app.UseAll(handler)
		}},
	}

	for _, setup := range setups {
		t.Run(setup.name, func(t *testing.T) {
			app := moon.New()
			setup.use(app, New().Handle)
			app.MapGet("/other", func(ctx *moon.Context) error {
				return ctx.Write(http.StatusOK, "other")
			})
			app.MapGet("/swagger/other", func(ctx *moon.Context) error {
				return ctx.Write(http.StatusOK, "swagger other")
			})

			resp := app.Test(httptest.NewRequest(http.MethodGet, "/swagger", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Contains(t, readBody(t, resp), "SwaggerUIBundle")

			resp = app.Test(httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)

			resp = app.Test(httptest.NewRequest(http.MethodGet, "/other", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "other", readBody(t, resp))

			resp = app.Test(httptest.NewRequest(http.MethodGet, "/swagger/other", nil))
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "swagger other", readBody(t, resp))
		})
	}
}

func TestHandle_CustomMountPath(t *testing.T) {
	app := moon.New()
	app.MapGet("/docs/*", New(WithMountPath("Docs")).Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/docs", nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body := readBody(t, resp)
	require.Contains(t, body, `"/Docs/openapi.json"`)

	document := getDocument(t, app, "/Docs/openapi.json")
	require.Contains(t, document.Paths, "/docs/{*}")

	resp = app.Test(httptest.NewRequest(http.MethodGet, "/docs/nope", nil))
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestHandle_CaseInsensitiveMountMatching(t *testing.T) {
	app := moon.New()
	app.MapGet("/docs/*", New(WithMountPath("/Docs")).Handle)

	for _, path := range []string{"/docs", "/DOCS/INDEX.HTML", "/docs/OpenAPI.JSON"} {
		resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
	}
}

func TestHandle_CaseSensitiveMountMatching(t *testing.T) {
	app := moon.New()
	app.MapGet("/Docs/*", New(
		WithMountPath("/Docs"),
		WithCaseSensitiveMountMatching(true),
	).Handle)

	for _, path := range []string{"/Docs", "/Docs/index.html", "/Docs/openapi.json"} {
		resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusOK, resp.StatusCode, "path: %s", path)
	}

	for _, path := range []string{"/docs", "/Docs/INDEX.HTML", "/Docs/OpenAPI.JSON"} {
		resp := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusNotFound, resp.StatusCode, "path: %s", path)
	}
}

func TestHandle_CustomUiAssets(t *testing.T) {
	app := moon.New()
	app.MapGet("/swagger/*", New(WithUiAssets(
		"https://cdn.example.com/swagger-ui.css",
		"https://cdn.example.com/swagger-ui-bundle.js",
	)).Handle)

	resp := app.Test(httptest.NewRequest(http.MethodGet, "/swagger", nil))
	body := readBody(t, resp)
	require.Contains(t, body, "https://cdn.example.com/swagger-ui.css")
	require.Contains(t, body, "https://cdn.example.com/swagger-ui-bundle.js")
	require.NotContains(t, body, "unpkg.com")
}

func TestHandle_CachesDocument(t *testing.T) {
	app := moon.New()
	app.MapGet("/first", func(ctx *moon.Context) error {
		return nil
	})
	app.MapGet("/swagger/*", New().Handle)

	before := getDocument(t, app)
	require.Contains(t, before.Paths, "/first")
	require.NotContains(t, before.Paths, "/late")

	app.MapGet("/late", func(ctx *moon.Context) error {
		return nil
	})

	after := getDocument(t, app)
	require.NotContains(t, after.Paths, "/late")
}

func TestNew_OptionsPanicOnInvalidValues(t *testing.T) {
	require.Panics(t, func() { New(WithMountPath(" ")) })
	require.Panics(t, func() { New(WithTitle(" ")) })
	require.Panics(t, func() { New(WithVersion(" ")) })
	require.Panics(t, func() { New(WithServerUrl(" ")) })
	require.Panics(t, func() { New(WithUiAssets("", "https://example.com/ui.js")) })
	require.Panics(t, func() { New(WithUiAssets("https://example.com/ui.css", "")) })
}

func TestOpenApiVersion(t *testing.T) {
	require.Equal(t, "3.2.1", OpenApiVersion)
	require.Equal(t, "/swagger", DefaultMountPath)
	require.Equal(t, "openapi.json", SpecFileName)
}

func TestOpenApiPath(t *testing.T) {
	testCases := []struct {
		name    string
		pattern string
		omitted map[string]bool
		want    string
	}{
		{"root", "/", nil, "/"},
		{"literal", "/api/users", nil, "/api/users"},
		{"parameter", "/users/:id", nil, "/users/{id}"},
		{"optional present", "/user/:name?", nil, "/user/{name}"},
		{"optional omitted", "/user/:name?", map[string]bool{"name": true}, "/user"},
		{"wildcard", "/files/*", nil, "/files/{*}"},
		{"plus", "/proxy/+", nil, "/proxy/{+}"},
		{"wildcard with suffix", "/files/*.json", nil, "/files/{*}.json"},
		{"multiple wildcards", "/v1/*/shop/*", nil, "/v1/{*1}/shop/{*2}"},
		{"escaped colon", `/v1/name\:verb`, nil, "/v1/name:verb"},
		{"wildcards inside a segment", "/*v1*/proxy", nil, "/{*1}v1{*2}/proxy"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			segments, _, err := moon.ParsePattern(tc.pattern)
			require.NoError(t, err)
			require.Equal(t, tc.want, openApiPath(segments, tc.omitted))
		})
	}
}

func getDocument(t *testing.T, app *moon.App, paths ...string) openApiDocument {
	t.Helper()
	specPath := "/swagger/openapi.json"
	if len(paths) > 0 {
		specPath = paths[0]
	}
	resp := app.Test(httptest.NewRequest(http.MethodGet, specPath, nil))
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return decodeDocument(t, readBody(t, resp))
}

func decodeDocument(t *testing.T, body string) openApiDocument {
	t.Helper()
	var document openApiDocument
	require.NoError(t, json.Unmarshal([]byte(body), &document))
	return document
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected object, got %T", value)
	return object
}

func asSlice(t *testing.T, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	require.True(t, ok, "expected array, got %T", value)
	return array
}
