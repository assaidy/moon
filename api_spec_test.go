package moon

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApiSpec_SecurityIsNormalized(t *testing.T) {
	var got []ApiSpecEntry

	app := New()
	app.MapGet("/x", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	}, WithApiSpec(NewApiSpec(WithSecurity(
		ApiSecurityRequirement{" bearerAuth ": nil},
		nil,
	)),
	))

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))

	require.Len(t, got, 1)
	require.Len(t, got[0].Spec.Security, 2)
	require.Equal(t, ApiSecurityRequirement{"bearerAuth": {}}, got[0].Spec.Security[0])
	require.Equal(t, ApiSecurityRequirement{}, got[0].Spec.Security[1])
}

func TestApiSpec_DefaultIsEmpty(t *testing.T) {
	var got []ApiSpecEntry

	app := New()
	app.MapGet("/x", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	})

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))

	require.Equal(t, []ApiSpecEntry{{
		Method:  MethodGet,
		Pattern: "/x",
		Spec:    apiSpec{},
	}}, got)
}

func TestApiSpec_Options(t *testing.T) {
	var got []ApiSpecEntry
	explode := true

	app := New()
	app.MapGet("/users/:id", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	},
		WithApiSpec(NewApiSpec(WithSummary("  Get a user  "),
			WithDescription("Returns one user by id.\n\nMay contain markdown."),
			WithOperationId("  getUser  "),
			WithTags(" users ", "admin", "users"),
			WithDeprecated(true),
			WithParameter(ApiParameter{
				Name:        " id ",
				In:          ApiParamInPath,
				Description: "The user id.",
				Schema:      Object{"type": "string"},
			}),
			WithParameter(ApiParameter{
				Name:            "verbose",
				In:              ApiParamInQuery,
				Required:        true,
				Style:           "form",
				Explode:         &explode,
				AllowEmptyValue: true,
				AllowReserved:   true,
				Schema:          Object{"type": "boolean"},
			}),
			WithRequestBody(ApiRequestBody{
				Description: "The user payload.",
				Content: map[string]ApiMediaType{
					"application/json": {
						Schema:   Object{"type": "object"},
						Examples: map[string]any{"one": Object{"value": Object{"name": "Ahmad"}}},
					},
				},
			}),
			WithResponse(ApiResponse{
				Status:      http.StatusOK,
				Description: "The user.",
				Content: map[string]ApiMediaType{
					"application/json": {Schema: Object{"type": "object"}},
				},
				Headers: map[string]ApiHeader{
					"X-Request-Id": {Description: "Request id.", Schema: Object{"type": "string"}},
				},
			}),
			WithResponse(ApiResponse{Status: 0}),
			WithSecurity(ApiSecurityRequirement{"bearerAuth": {"read:users"}}),
			WithExtra("x-internal", true)),
		),
	)

	app.Test(httptest.NewRequest(http.MethodGet, "/users/42", nil))

	require.Len(t, got, 1)
	entry := got[0]
	require.Equal(t, MethodGet, entry.Method)
	require.Equal(t, "/users/:id", entry.Pattern)

	spec := entry.Spec
	require.Equal(t, "Get a user", spec.Summary)
	require.Equal(t, "Returns one user by id.\n\nMay contain markdown.", spec.Description)
	require.Equal(t, "getUser", spec.OperationId)
	require.Equal(t, []string{"users", "admin"}, spec.Tags)
	require.True(t, spec.Deprecated)

	require.Len(t, spec.Parameters, 2)
	require.Equal(t, ApiParameter{
		Name:        "id",
		In:          ApiParamInPath,
		Description: "The user id.",
		Required:    true,
		Schema:      Object{"type": "string"},
	}, spec.Parameters[0])
	require.Equal(t, ApiParameter{
		Name:            "verbose",
		In:              ApiParamInQuery,
		Required:        true,
		Style:           "form",
		Explode:         &explode,
		AllowEmptyValue: true,
		AllowReserved:   true,
		Schema:          Object{"type": "boolean"},
	}, spec.Parameters[1])

	require.NotNil(t, spec.RequestBody)
	require.Equal(t, "The user payload.", spec.RequestBody.Description)
	require.Contains(t, spec.RequestBody.Content, "application/json")

	require.Len(t, spec.Responses, 2)
	require.Equal(t, http.StatusOK, spec.Responses[0].Status)
	require.Equal(t, "The user.", spec.Responses[0].Description)
	require.Contains(t, spec.Responses[0].Headers, "X-Request-Id")
	require.Equal(t, 0, spec.Responses[1].Status)

	require.Equal(t, []ApiSecurityRequirement{{"bearerAuth": {"read:users"}}}, spec.Security)
	require.Equal(t, map[string]any{"x-internal": true}, spec.Extra)
}

func TestApiSpec_RepeatedOptionsAccumulate(t *testing.T) {
	var got []ApiSpecEntry

	app := New()
	app.MapGet("/x", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	},
		WithApiSpec(NewApiSpec(WithTags("one", "two"),
			WithTags("two", "three"),
			WithParameter(ApiParameter{Name: "a", In: ApiParamInQuery}),
			WithParameter(ApiParameter{Name: "b", In: ApiParamInHeader}),
			WithResponse(ApiResponse{Status: 200}),
			WithResponse(ApiResponse{Status: 404}),
			WithSecurity(ApiSecurityRequirement{"a": {"x"}}),
			WithSecurity(ApiSecurityRequirement{"b": {"y"}})),
		),
	)

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))

	require.Len(t, got, 1)
	spec := got[0].Spec
	require.Equal(t, []string{"one", "two", "three"}, spec.Tags)
	require.Equal(t, []ApiParameter{
		{Name: "a", In: ApiParamInQuery},
		{Name: "b", In: ApiParamInHeader},
	}, spec.Parameters)
	require.Equal(t, []ApiResponse{{Status: 200}, {Status: 404}}, spec.Responses)
	require.Equal(t, []ApiSecurityRequirement{{"a": {"x"}}, {"b": {"y"}}}, spec.Security)
}

func TestApiSpec_MapAll(t *testing.T) {
	var got []ApiSpecEntry

	app := New()
	app.MapAll("/x", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	}, WithApiSpec(NewApiSpec(WithSummary("all methods"))))

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))

	require.Len(t, got, len(allMethods))
	methods := make([]string, 0, len(got))
	for _, entry := range got {
		methods = append(methods, entry.Method)
		require.Equal(t, "all methods", entry.Spec.Summary)
	}
	slices.Sort(methods)
	wantMethods := slices.Clone(allMethods)
	slices.Sort(wantMethods)
	require.Equal(t, wantMethods, methods)
}

func TestApiSpec_CollectionOrderAndFiltering(t *testing.T) {
	app := New()
	app.Use("/api", func(ctx *Context) error { return ctx.Next() })
	app.MapGet("/a", func(ctx *Context) error { return nil })
	app.MapPost("/a", func(ctx *Context) error { return nil })
	app.MapGet("/b/*", func(ctx *Context) error { return nil })
	app.MapGet("/", func(ctx *Context) error { return nil })

	got := app.collectApiSpecs()

	require.Equal(t, []ApiSpecEntry{
		{Method: MethodGet, Pattern: "/a"},
		{Method: MethodPost, Pattern: "/a"},
		{Method: MethodGet, Pattern: "/b/*"},
		{Method: MethodGet, Pattern: "/"},
	}, got)
}

func TestApiSpec_ReflectsRoutesRegisteredLater(t *testing.T) {
	var first, second []ApiSpecEntry

	app := New()
	app.MapGet("/collect", func(ctx *Context) error {
		if first == nil {
			first = ctx.GetApiSpecs()
		} else {
			second = ctx.GetApiSpecs()
		}
		return nil
	})

	app.Test(httptest.NewRequest(http.MethodGet, "/collect", nil))
	app.MapGet("/late", func(ctx *Context) error { return nil })
	app.Test(httptest.NewRequest(http.MethodGet, "/collect", nil))

	require.Len(t, first, 1)
	require.Len(t, second, 2)
	require.Equal(t, "/late", second[1].Pattern)
}

func TestApiSpec_SnapshotIsolation(t *testing.T) {
	var first, second []ApiSpecEntry
	explode := true

	app := New()
	app.MapGet("/x", func(ctx *Context) error {
		if first == nil {
			first = ctx.GetApiSpecs()
			first[0].Spec.Tags[0] = "changed"
			first[0].Spec.Parameters[0].Name = "changed"
			*first[0].Spec.Parameters[0].Explode = false
			first[0].Spec.RequestBody.Content["application/json"] = ApiMediaType{}
			first[0].Spec.Responses[0].Content["application/json"] = ApiMediaType{}
			first[0].Spec.Responses[0].Headers["X-Test"] = ApiHeader{Description: "changed"}
			first[0].Spec.Security[0]["scheme"] = []string{"changed"}
			first[0].Spec.Extra["x-test"] = "changed"
		} else {
			second = ctx.GetApiSpecs()
		}
		return nil
	}, WithApiSpec(NewApiSpec(WithTags("original"),
		WithParameter(ApiParameter{
			Name:    "id",
			In:      ApiParamInQuery,
			Explode: &explode,
			Schema:  Object{"type": "string"},
		}),
		WithRequestBody(ApiRequestBody{
			Content: map[string]ApiMediaType{"application/json": {Schema: Object{"type": "object"}}},
		}),
		WithResponse(ApiResponse{
			Status:  200,
			Content: map[string]ApiMediaType{"application/json": {}},
			Headers: map[string]ApiHeader{"X-Test": {Description: "original"}},
		}),
		WithSecurity(ApiSecurityRequirement{"scheme": {"scope"}}),
		WithExtra("x-test", "original")),
	))

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))
	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))

	require.Len(t, second, 1)
	spec := second[0].Spec
	require.Equal(t, []string{"original"}, spec.Tags)
	require.Equal(t, "id", spec.Parameters[0].Name)
	require.Equal(t, true, *spec.Parameters[0].Explode)
	require.Equal(t, Object{"type": "object"}, spec.RequestBody.Content["application/json"].Schema)
	require.Equal(t, "original", spec.Responses[0].Headers["X-Test"].Description)
	require.Equal(t, []string{"scope"}, spec.Security[0]["scheme"])
	require.Equal(t, "original", spec.Extra["x-test"])
	require.Equal(t, Object{"type": "string"}, spec.Parameters[0].Schema)
}

// Schema and example values are intentionally passed through rather than
// deep-copied, so documentation generators can carry any JSON-compatible
// value. Callers must treat them as immutable.
func TestApiSpec_SchemaAndExamplesArePassedThrough(t *testing.T) {
	var got []ApiSpecEntry
	schema := Object{"type": "string"}
	example := Object{"name": "Ahmad"}

	app := New()
	app.MapGet("/x", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	}, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{
		Name:    "q",
		In:      ApiParamInQuery,
		Schema:  schema,
		Example: example,
	}),
		WithRequestBody(ApiRequestBody{
			Content: map[string]ApiMediaType{
				"application/json": {
					Schema:  schema,
					Example: example,
				},
				"application/xml": {
					Schema:   schema,
					Examples: map[string]any{"one": Object{"value": example}},
				},
			},
		})),
	))

	app.Test(httptest.NewRequest(http.MethodGet, "/x", nil))

	gotSchema := got[0].Spec.Parameters[0].Schema.(Object)
	gotSchema["type"] = "integer"
	require.Equal(t, "integer", schema["type"])

	gotExample := got[0].Spec.RequestBody.Content["application/json"].Example.(Object)
	gotExample["name"] = "Changed"
	require.Equal(t, "Changed", example["name"])
	gotExampleObject := got[0].Spec.RequestBody.Content["application/xml"].Examples["one"].(Object)
	require.Equal(t, "Changed", gotExampleObject["value"].(Object)["name"])
}

func TestNewApiSpecNilOptionPanics(t *testing.T) {
	require.Panics(t, func() { NewApiSpec(nil) })
}

func TestApiSpec_ValidationPanics(t *testing.T) {
	handler := func(ctx *Context) error { return nil }

	testCases := []struct {
		name  string
		mapFn func(app *App)
	}{
		{
			name: "invalid parameter location",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{
					Name: "q", In: "body",
				}))))
			},
		},
		{
			name: "empty parameter name",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{
					Name: "  ", In: ApiParamInQuery,
				}))))
			},
		},
		{
			name: "duplicate parameter",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{Name: "q", In: ApiParamInQuery}),
					WithParameter(ApiParameter{Name: " q ", In: ApiParamInQuery})),
				))
			},
		},
		{
			name: "duplicate header parameter regardless of case",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{Name: "X-Request-Id", In: ApiParamInHeader}),
					WithParameter(ApiParameter{Name: "x-request-id", In: ApiParamInHeader})),
				))
			},
		},
		{
			name: "allow empty value outside query",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{
					Name: "X-Test", In: ApiParamInHeader, AllowEmptyValue: true,
				}))))
			},
		},
		{
			name: "path parameter missing from pattern",
			mapFn: func(app *App) {
				app.MapGet("/users/:id", handler, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{
					Name: "missing", In: ApiParamInPath,
				}))))
			},
		},
		{
			name: "duplicate response",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithResponse(ApiResponse{Status: 200}),
					WithResponse(ApiResponse{Status: 200})),
				))
			},
		},
		{
			name: "invalid response status",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithResponse(ApiResponse{Status: 99}))))
			},
		},
		{
			name: "invalid response status above range",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithResponse(ApiResponse{Status: 600}))))
			},
		},
		{
			name: "empty tag",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithTags(" "))))
			},
		},
		{
			name: "empty security scheme",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithSecurity(ApiSecurityRequirement{
					" ": {"read"},
				}))))
			},
		},
		{
			name: "duplicate security scheme after trimming",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithSecurity(ApiSecurityRequirement{
					" bearerAuth ": {"read"},
					"bearerAuth":   {"write"},
				}))))
			},
		},
		{
			name: "empty extra key",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithExtra(" ", true))))
			},
		},
		{
			name: "duplicate extra key",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithExtra("x-test", true),
					WithExtra(" x-test ", false)),
				))
			},
		},
		{
			name: "empty media type",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithRequestBody(ApiRequestBody{
					Content: map[string]ApiMediaType{" ": {}},
				}))))
			},
		},
		{
			name: "media type example and examples together",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithRequestBody(ApiRequestBody{
					Content: map[string]ApiMediaType{
						"application/json": {
							Example:  Object{"name": "Ahmad"},
							Examples: map[string]any{"one": Object{"value": Object{"name": "Ahmad"}}},
						},
					},
				}))))
			},
		},
		{
			name: "duplicate media type after trimming",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithRequestBody(ApiRequestBody{
					Content: map[string]ApiMediaType{
						"application/json":  {},
						" application/json": {},
					},
				}))))
			},
		},
		{
			name: "empty response header name",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithResponse(ApiResponse{
					Status:  200,
					Headers: map[string]ApiHeader{" ": {}},
				}))))
			},
		},
		{
			name: "response header style other than simple",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithResponse(ApiResponse{
					Status: 200,
					Headers: map[string]ApiHeader{
						"X-Test": {Style: "form"},
					},
				}))))
			},
		},
		{
			name: "duplicate response header regardless of case",
			mapFn: func(app *App) {
				app.MapGet("/x", handler, WithApiSpec(NewApiSpec(WithResponse(ApiResponse{
					Status: 200,
					Headers: map[string]ApiHeader{
						"X-Test": {},
						"x-test": {},
					},
				}))))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			require.Panics(t, func() { tc.mapFn(New()) })
		})
	}
}

func TestApiSpec_WildcardPathParametersAreAccepted(t *testing.T) {
	var got []ApiSpecEntry

	app := New()
	app.MapGet("/files/*", func(ctx *Context) error {
		got = ctx.GetApiSpecs()
		return nil
	}, WithApiSpec(NewApiSpec(WithParameter(ApiParameter{Name: "*", In: ApiParamInPath}))))

	app.Test(httptest.NewRequest(http.MethodGet, "/files/a/b.txt", nil))

	require.Len(t, got, 1)
	require.Len(t, got[0].Spec.Parameters, 1)
	require.True(t, got[0].Spec.Parameters[0].Required)
}
