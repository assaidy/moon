// Package basic_auth provides a handler that implements HTTP Basic
// authentication (RFC 7617) on top of the HTTP authentication framework
// (RFC 9110). Register [Handler.Handle] as a middleware:
//
//	app.UseAll(basic_auth.New(
//		basic_auth.WithAuthorizer(func(user, password string, ctx *moon.Context) (bool, error) {
//			return user == "admin" && password == "secret", nil
//		}),
//	).Handle)
//
// The authorizer receives the decoded user-id and password and reports whether
// they are valid. An error means the decision could not be made, for example
// because the user store is unavailable, and is answered with 500 rather than
// 401 so a backend failure is not mistaken for bad credentials.
//
// On success the user-id is stored in a context local (see [DefaultLocalKey]
// and [WithLocalKey]) and the chain runs. Missing, malformed or rejected
// credentials get a 401 with a Basic challenge; a malformed Authorization
// field gets 400. The challenge advertises UTF-8 and a configurable realm (see
// [WithRealm]).
//
// Requests for which the [WithSkip] predicate returns true run the chain
// untouched.
package basic_auth

import (
	"encoding/base64"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/assaidy/moon"
)

// Handler authenticates requests with HTTP Basic credentials. Use [New] to
// construct it, passing [Option]s to configure it, then register
// [Handler.Handle] in the chain.
type Handler struct {
	options options
}

// New returns a handler built from sensible defaults, customized by the given
// options, then register [Handler.Handle] in the chain:
//
//	app.UseAll(basic_auth.New(basic_auth.WithAuthorizer(authorize)).Handle)
//
// The authorizer is required and cannot be nil; New panics without it.
// Default realm: "Restricted" (see [WithRealm]).
// Default local key: [DefaultLocalKey] (see [WithLocalKey]).
// Default header limit: -1 (no limit, see [WithHeaderLimit]).
// Requests for which the [WithSkip] predicate returns true run the chain
// untouched.
func New(opts ...Option) *Handler {
	options := options{
		headerLimit: -1,
		realm:       "Restricted",
		localKey:    DefaultLocalKey,
	}
	for _, opt := range opts {
		opt(&options)
	}

	moon.Assert(options.authorizer != nil, "authorizer must be set; set it using WithAuthorizer option")

	return &Handler{options: options}
}

// options holds the configuration of a [Handler]. All fields are private;
// pass [Option]s to [New] to configure it.
type options struct {
	skip        func(*moon.Context) bool
	headerLimit int
	authorizer  func(user, password string, ctx *moon.Context) (bool, error)
	realm       string
	localKey    string
}

// DefaultLocalKey is the context local key used when [WithLocalKey] is not
// given. Read the user-id with [moon.Context.GetLocal] using this key.
const DefaultLocalKey = "moon.handlers.basic_auth.local_key"

// Option configures a [Handler]. Pass them to [New]:
//
//	app.UseAll(basic_auth.New(
//		basic_auth.WithAuthorizer(authorize),
//		basic_auth.WithRealm("Admin"),
//	).Handle)
type Option func(*options)

// WithSkip bypasses authentication for requests where f returns true.
// The chain still runs; no credentials are required and nothing is stored.
// Pass it to [New].
//
// Default: nil (nothing is skipped)
func WithSkip(f func(*moon.Context) bool) Option {
	return func(o *options) {
		o.skip = f
	}
}

// WithAuthorizer sets the function that validates credentials. It receives
// the decoded user-id and password, plus the request context, and returns true
// to accept them. An error means the decision could not be made, for example
// because the user store is unavailable; the handler answers with 500 instead
// of 401. Rejecting credentials is done with (false, nil).
//
// It is required and panics on nil. Pass it to [New].
func WithAuthorizer(f func(user, password string, ctx *moon.Context) (bool, error)) Option {
	moon.Assert(f != nil, "authorizer func cannot be nil")
	return func(o *options) {
		o.authorizer = f
	}
}

// WithHeaderLimit caps the length of the Authorization header value in bytes.
// Longer values are rejected with 431 (Request Header Fields Too Large).
// A negative value disables the cap. Zero is not accepted: the cap applies
// only to positive values, so zero would silently mean "no limit" instead of
// being an explicit choice. Pass it to [New].
//
// Default: -1 (no limit)
func WithHeaderLimit(i int) Option {
	moon.Assert(i != 0, "header limit cannot be zero; use a negative value for no limit")
	return func(o *options) {
		o.headerLimit = i
	}
}

// WithRealm sets the realm advertised in the WWW-Authenticate challenge.
// Surrounding whitespace is trimmed. The value is written as an RFC 9110
// quoted-string, so quotes and backslashes are escaped. It panics on an empty
// value or on control characters, which cannot appear in a quoted-string even
// in escaped form. Pass it to [New].
//
// Default: "Restricted"
func WithRealm(s string) Option {
	s = strings.TrimSpace(s)
	moon.Assert(s != "", "realm cannot be empty")
	moon.Assert(isValidRealm(s), "realm must not contain control characters")
	return func(o *options) {
		o.realm = s
	}
}

// WithLocalKey sets the context local key under which the authenticated
// user-id is stored. The key is used as-is, including surrounding whitespace.
// It panics on an empty key. Read the user-id with [moon.Context.GetLocal]
// using the same key. Pass it to [New].
//
// Default: [DefaultLocalKey].
func WithLocalKey(s string) Option {
	moon.Assert(s != "", "local key cannot be empty")
	return func(o *options) {
		o.localKey = s
	}
}

// Handle authenticates the request. Register it with [moon.App.Use] or as a
// route middleware:
//
//	app.UseAll(h.Handle)
//
// Status codes follow RFC 9110. A malformed Authorization field is a client
// error (400), while credentials that are well-formed but cannot be accepted
// (wrong scheme, undecodable token, invalid UTF-8 or control characters,
// unknown user, wrong password) get a 401 challenge so the user agent can
// supply credentials again. An authorizer failure is reported as 500, because
// it says nothing about the credentials themselves.
func (me *Handler) Handle(ctx *moon.Context) error {
	if me.options.skip != nil && me.options.skip(ctx) {
		return ctx.Next()
	}

	// Authorization is a singleton field (RFC 9110 §11.6.2).
	// Reject it instead of silently picking the first value.
	values := ctx.GetHttpRequest().Header.Values("Authorization")
	if len(values) > 1 {
		return moon.ErrBadRequest
	}

	var header string
	if len(values) == 1 {
		header = values[0]
	}
	if header == "" {
		return me.unauthorized(ctx)
	}

	if me.options.headerLimit > 0 && len(header) > me.options.headerLimit {
		return moon.ErrRequestHeaderFieldsTooLarge
	}

	// credentials = auth-scheme [ 1*SP ( token68 / #auth-param ) ]
	kind, rest, hasRest := strings.Cut(header, " ")

	// auth-scheme is a token, which is ASCII-only. Checking it before the
	// case-insensitive comparison also prevents Unicode case folding from
	// accepting lookalikes such as "Baſic".
	if !isToken(kind) {
		return moon.ErrBadRequest
	}
	if !strings.EqualFold(kind, "basic") {
		// The field is well-formed but carries credentials for a scheme this
		// handler does not implement. Challenge with Basic so the client can
		// authenticate for this protection space.
		return me.unauthorized(ctx)
	}

	// A bare "Basic" (or "Basic" followed only by spaces) carries no
	// credentials at all. RFC 9110 §11.4 recommends a 401 challenge for
	// partial credentials.
	var token string
	if hasRest {
		token = strings.TrimLeft(rest, " ")
	}
	if token == "" {
		return me.unauthorized(ctx)
	}

	// token68 is a superset of base64 (RFC 9110 §11.2). Checking it first
	// separates malformed field syntax (400) from credentials that are
	// syntactically valid but cannot be decoded (401).
	if !isToken68(token) {
		return moon.ErrBadRequest
	}

	decoded, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		// token68 allows base64 with or without padding (RFC 9110 §11.2), and
		// some clients omit the trailing "=". Accept the unpadded form too.
		decoded, err = base64.RawStdEncoding.DecodeString(token)
	}
	if err != nil {
		return me.unauthorized(ctx)
	}

	// RFC 7617 §2: user-pass is "user-id ':' password". Only the first colon
	// separates the two, so passwords may contain colons; text after the first
	// colon always belongs to the password.
	pair := strings.SplitN(string(decoded), ":", 2)
	if len(pair) != 2 {
		return me.unauthorized(ctx)
	}
	user, password := pair[0], pair[1]

	// The challenge advertises charset="UTF-8" (RFC 7617 §2.1), so reject
	// anything that is not valid UTF-8 rather than passing undecodable bytes to
	// the authorizer. Normalization is deliberately left to the authorizer:
	// rewriting credentials here would break comparisons against password
	// hashes created from the original bytes.
	if !utf8.ValidString(user) || !utf8.ValidString(password) {
		return me.unauthorized(ctx)
	}

	// RFC 7617 §2: user-id and password MUST NOT contain control characters.
	// "CTL" is the ASCII definition (%x00-1F / %x7F); UTF-8 continuation bytes
	// are >= 0x80, so a byte scan is safe after the validity check above.
	if hasControlByte(user) || hasControlByte(password) {
		return me.unauthorized(ctx)
	}

	ok, err := me.options.authorizer(user, password, ctx)
	if err != nil {
		// The credentials may be fine; the authorizer itself failed. Report an
		// internal error so the client is not told to retry its password, and
		// keep the cause for the logs (the default error handler hides Details
		// from the response but logs them).
		return moon.ErrInternalServerError.WithDetails(err)
	}
	if !ok {
		return me.unauthorized(ctx)
	}

	ctx.SetLocal(me.options.localKey, user)

	return ctx.Next()
}

// unauthorized writes the Basic challenge and returns 401.
//
// RFC 9110 §11.6.1 requires every 401 to carry at least one challenge. RFC
// 7617 §2 requires the realm parameter, and §2.1 defines "UTF-8" as the only
// allowed charset value.
func (me *Handler) unauthorized(ctx *moon.Context) error {
	ctx.SetHeader("WWW-Authenticate", fmt.Sprintf(`Basic realm=%s, charset="UTF-8"`, quoteString(me.options.realm)))
	// The challenge is specific to the caller. Keep it out of shared caches,
	// and make caches key on the header that selects the identity.
	ctx.SetHeader("Cache-Control", "no-store")
	ctx.AddHeader("Vary", "Authorization")

	return moon.ErrUnauthorized
}

// quoteString renders s as an RFC 9110 §5.6.4 quoted-string. Only DQUOTE and
// backslash need escaping; control characters are rejected by [WithRealm]
// because qdtext cannot contain them even in escaped form.
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
// Tokens are ASCII-only, which is why the scheme comparison can be
// case-insensitive without Unicode folding surprises.
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

// isToken68 reports whether s matches the RFC 9110 §11.2 token68 production:
// one or more base64-alphabet characters followed by zero or more "=".
func isToken68(s string) bool {
	i := 0
	for i < len(s) && s[i] != '=' {
		if !isToken68Char(s[i]) {
			return false
		}
		i++
	}
	if i == 0 {
		return false // at least one non-padding character is required
	}
	for i < len(s) {
		if s[i] != '=' {
			return false // "=" is only allowed as trailing padding
		}
		i++
	}
	return true
}

// isToken68Char reports whether c is allowed in the non-padding part of a
// token68: ALPHA / DIGIT / "-" / "." / "_" / "~" / "+" / "/".
func isToken68Char(c byte) bool {
	switch {
	case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		return true
	}
	switch c {
	case '-', '.', '_', '~', '+', '/':
		return true
	}
	return false
}

// hasControlByte reports whether s contains an ASCII control character, the
// "CTL" production from RFC 5234 Appendix B.1.
func hasControlByte(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
