// Package key_auth provides a handler that authenticates requests with an API
// key. Register [Handler.Handle] as a middleware:
//
//	app.UseAll(key_auth.New(
//		key_auth.WithValidator(func(ctx *moon.Context, key string) (bool, error) {
//			return key == "secret", nil
//		}),
//	).Handle)
package key_auth

import (
	"net/url"
	"strings"

	"github.com/assaidy/moon"
)

// Handler authenticates requests with an API key. Use [New] to construct it,
// passing [Option]s to configure it, then register [Handler.Handle] in the
// chain.
type Handler struct {
	options options
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register [Handler.Handle] in the chain:
//
//	app.UseAll(key_auth.New(key_auth.WithValidator(validate)).Handle)
//
// The validator is required and cannot be nil; New panics without it.
// Default source: [SourceAuthorizationHeader]("Bearer") (see [WithKeySource]).
func New(opts ...Option) *Handler {
	options := options{
		source: SourceAuthorizationHeader("Bearer"),
	}
	for _, opt := range opts {
		opt(&options)
	}

	moon.Assert(options.validator != nil, "validator must be set; set it using WithValidator option")

	return &Handler{options: options}
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	skip      func(*moon.Context) bool
	source    KeySource
	validator func(*moon.Context, string) (bool, error)
}

// Option configures a [Handler]. Pass them to [New]:
//
//	app.UseAll(key_auth.New(
//		key_auth.WithKeySource(key_auth.SourceXApiKeyHeader()),
//		key_auth.WithValidator(validate),
//	).Handle)
type Option func(*options)

// WithSkip bypasses authentication for requests where f returns true. The
// chain still runs; no key is required and the validator is not called. Pass
// it to [New].
//
// Default: nil (nothing is skipped)
func WithSkip(f func(*moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// WithKeySource sets where the key is read from and the challenge sent when
// it is missing or rejected. The extractor is required, and the challenge is
// normalized and validated. Use one of the predefined sources or build a
// custom [KeySource]. Pass it to [New].
//
// Default: [SourceAuthorizationHeader]("Bearer")
func WithKeySource(s KeySource) Option {
	moon.Assert(s.Extractor != nil, "key source extractor cannot be nil")
	s.Challenge = normalizeChallenge(s.Challenge)
	return func(o *options) {
		o.source = s
	}
}

// WithValidator sets the function that validates the extracted key. It
// receives the request context and the key, and returns true to accept it. An
// error means the decision could not be made, for example because the API key
// store is unavailable; the handler answers with 500 instead of 401.
// Rejecting a key is done with (false, nil).
//
// It is required and panics on nil. Pass it to [New].
func WithValidator(f func(ctx *moon.Context, key string) (bool, error)) Option {
	moon.Assert(f != nil, "validator func cannot be nil")
	return func(o *options) {
		o.validator = f
	}
}

// KeySource describes where an API key is read from and how a request without
// a valid key is challenged. Build one with [SourceAuthorizationHeader],
// [SourceXApiKeyHeader], [SourceCookie], or a literal for a custom source.
type KeySource struct {
	// Extractor returns the key carried by the request, or "" when none is
	// present.
	Extractor func(ctx *moon.Context) string

	// Challenge is sent in WWW-Authenticate when the key is missing or
	// rejected.
	Challenge Challenge
}

// Challenge is the WWW-Authenticate challenge sent with a 401 response.
type Challenge struct {
	// Scheme is the authentication scheme, for example "Bearer" or "ApiKey".
	// It defaults to "ApiKey" when empty and must be a valid HTTP token.
	Scheme string

	// Realm describes the protection space. It defaults to "Restricted" when
	// empty.
	Realm string

	// Error is the RFC 6750 error code, for example "invalid_token". It is
	// omitted when empty.
	Error string

	// ErrorDescription is a human-readable explanation of Error. It is
	// omitted when empty.
	ErrorDescription string

	// ErrorUrl identifies a page explaining the error. It is omitted when
	// empty.
	ErrorUrl string

	// Scope lists the scopes required to access the resource. It is omitted
	// when empty.
	Scope string
}

// SourceAuthorizationHeader returns a source that reads the key from the
// Authorization header using the given scheme, which is also used as the
// challenge scheme. An empty scheme defaults to "ApiKey".
func SourceAuthorizationHeader(scheme string) KeySource {
	challenge := normalizeChallenge(Challenge{Scheme: scheme})
	return KeySource{
		Extractor: func(ctx *moon.Context) string {
			return extractAuthorization(ctx.GetHeader("Authorization"), challenge.Scheme)
		},
		Challenge: challenge,
	}
}

// SourceXApiKeyHeader returns a source that reads the key from the X-API-Key
// request header.
func SourceXApiKeyHeader() KeySource {
	return KeySource{
		Extractor: func(ctx *moon.Context) string {
			return ctx.GetHeader("X-API-Key")
		},
		Challenge: normalizeChallenge(Challenge{Scheme: "ApiKey"}),
	}
}

// SourceCookie returns a source that reads the key from the named cookie.
func SourceCookie(name string) KeySource {
	name = strings.TrimSpace(name)
	moon.Assert(name != "", "cookie name cannot be empty")
	return KeySource{
		Extractor: func(ctx *moon.Context) string {
			return ctx.GetCookie(name)
		},
		Challenge: normalizeChallenge(Challenge{Scheme: "ApiKey"}),
	}
}

// Handle extracts and validates the API key. Requests skipped by [WithSkip]
// run the chain untouched. A missing or rejected key gets a 401 challenge; a
// validator error is reported as 500.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	key := me.options.source.Extractor(ctx)
	if key == "" {
		return unauthorized(ctx, me.options.source.Challenge)
	}

	if ok, err := me.options.validator(ctx, key); err != nil {
		return moon.ErrInternalServerError.WithDetails(err)
	} else if !ok {
		return unauthorized(ctx, me.options.source.Challenge)
	}

	return ctx.Next()
}

// unauthorized writes the configured challenge and returns 401.
func unauthorized(ctx *moon.Context, challenge Challenge) error {
	ctx.SetHeader("WWW-Authenticate", challenge.generate())
	return moon.ErrUnauthorized
}

// generate renders the challenge as a WWW-Authenticate header value.
func (me Challenge) generate() string {
	var b strings.Builder
	b.WriteString(me.Scheme)
	b.WriteString(` realm=`)
	b.WriteString(quoteString(me.Realm))
	if me.Error != "" {
		b.WriteString(`, error=`)
		b.WriteString(quoteString(me.Error))
	}
	if me.ErrorDescription != "" {
		b.WriteString(`, error_description=`)
		b.WriteString(quoteString(me.ErrorDescription))
	}
	if me.ErrorUrl != "" {
		b.WriteString(`, error_uri=`)
		b.WriteString(quoteString(me.ErrorUrl))
	}
	if me.Scope != "" {
		b.WriteString(`, scope=`)
		b.WriteString(quoteString(me.Scope))
	}
	return b.String()
}

// normalizeChallenge trims every field, applies the scheme and realm
// defaults and validates the values that end up in the challenge.
func normalizeChallenge(c Challenge) Challenge {
	c.Scheme = strings.TrimSpace(c.Scheme)
	if c.Scheme == "" {
		c.Scheme = "ApiKey"
	}
	moon.Assert(isToken(c.Scheme), "challenge scheme must be a valid token")

	c.Realm = strings.TrimSpace(c.Realm)
	if c.Realm == "" {
		c.Realm = "Restricted"
	}
	moon.Assert(isValidRealm(c.Realm), "realm must not contain control characters")

	c.Error = strings.TrimSpace(c.Error)
	moon.Assert(c.Error == "" || isValidErrorValue(c.Error), "error must contain only valid characters")

	c.ErrorDescription = strings.TrimSpace(c.ErrorDescription)
	moon.Assert(c.ErrorDescription == "" || isValidErrorValue(c.ErrorDescription), "error description must contain only valid characters")

	c.ErrorUrl = strings.TrimSpace(c.ErrorUrl)
	moon.Assert(c.ErrorUrl == "" || isValidErrorUrl(c.ErrorUrl), "error url must be a valid absolute URI")

	c.Scope = strings.TrimSpace(c.Scope)
	moon.Assert(c.Scope == "" || isValidScope(c.Scope), "scope must be a space-delimited list of valid scope tokens")

	return c
}

// extractAuthorization returns the credentials of an Authorization value
// carrying the given scheme. The scheme comparison is case-insensitive.
func extractAuthorization(header, scheme string) string {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], scheme) {
		return ""
	}
	return fields[1]
}

// isValidErrorValue reports whether s matches the error and error_description
// value character set from RFC 6749 Appendixes A.7 and A.8.
func isValidErrorValue(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == ' ' || isValidUrlChar(c) {
			continue
		}
		return false
	}
	return true
}

// isValidErrorUrl reports whether s is an absolute URI carried in the
// error_uri parameter.
func isValidErrorUrl(s string) bool {
	if !isValidUrlValue(s) {
		return false
	}
	parsed, err := url.Parse(s)
	return err == nil && parsed.IsAbs()
}

// isValidScope reports whether s is a space-delimited list of scope tokens
// from RFC 6749 Appendix A.4.
func isValidScope(s string) bool {
	for token := range strings.SplitSeq(s, " ") {
		if !isValidUrlValue(token) {
			return false
		}
	}
	return true
}

// isValidUrlValue reports whether s contains only characters allowed in an
// RFC 3986 URI-reference that is carried in a quoted auth parameter.
func isValidUrlValue(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isValidUrlChar(s[i]) {
			return false
		}
	}
	return true
}

// isValidUrlChar reports whether c is in the RFC 6749 set shared by
// error_uri and scope-token values.
func isValidUrlChar(c byte) bool {
	return c == '!' || (c >= '#' && c <= '[') || (c >= ']' && c <= '~')
}

// quoteString renders s as an RFC 9110 §5.6.4 quoted-string. Only DQUOTE and
// backslash need escaping; control characters are rejected during challenge
// normalization.
func quoteString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '"' || c == '\\' {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// isValidRealm reports whether s can be carried inside a quoted-string.
//
// RFC 9110 §5.6.4 allows HTAB, SP, VCHAR and obs-text in qdtext. CTLs other
// than HTAB are invalid there and cannot be escaped, so they are rejected.
func isValidRealm(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c == '\t' || (c >= 0x20 && c != 0x7f) {
			continue
		}
		return false
	}
	return true
}

// isToken reports whether s matches the RFC 9110 §5.6.2 token production.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTChar(s[i]) {
			return false
		}
	}
	return true
}

// isTChar reports whether c is an RFC 9110 §5.6.2 tchar.
func isTChar(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
