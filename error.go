package moon

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is the standard JSON error body written by the default [ErrorHandler].
// StatusCode sets the response status and is never serialized. Kind is a
// machine-readable code matched by [Error.Is]. Details carries optional extra
// context and is omitted when nil.
type Error struct {
	StatusCode  int    `json:"-"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Details     any    `json:"details,omitempty"`
}

// Error implements [error]. It returns the Kind,
// or "kind(details)" when Details is set.
func (me Error) Error() string {
	if me.Details != nil {
		return fmt.Sprintf("%s(%s)", me.Kind, me.Details)
	}
	return me.Kind
}

// Is reports whether target is an [Error] with the same Kind, so
// [errors.Is] matches across Details and wrapping.
func (me Error) Is(target error) bool {
	e, ok := errors.AsType[Error](target)
	if !ok {
		return false
	}
	return e.Kind == me.Kind
}

// NewError creates an [Error] with the given status code, kind and description.
func NewError(statusCode int, kind, description string) Error {
	return Error{StatusCode: statusCode, Kind: kind, Description: description}
}

// WithDetails returns a copy of the error with Details set.
// The original is unchanged.
func (me Error) WithDetails(d any) Error {
	me.Details = d
	return me
}

var (
	ErrInvalidEndpoint  = NewError(http.StatusNotFound, "invalid_endpoint", "The requested resource or endpoint could not be found.")
	ErrValidationFailed = NewError(http.StatusUnprocessableEntity, "validation_failed", "The provided data failed validation rules.")

	ErrBadRequest                   = NewError(http.StatusBadRequest, "bad_request", "The server could not process the request due to a client error.")
	ErrUnauthorized                 = NewError(http.StatusUnauthorized, "unauthorized", "The request lacks valid authentication credentials.")
	ErrPaymentRequired              = NewError(http.StatusPaymentRequired, "payment_required", "Payment is required to access the requested resource.")
	ErrForbidden                    = NewError(http.StatusForbidden, "forbidden", "The server understood the request but refuses to authorize it.")
	ErrNotFound                     = NewError(http.StatusNotFound, "not_found", "The requested resource could not be found.")
	ErrMethodNotAllowed             = NewError(http.StatusMethodNotAllowed, "method_not_allowed", "The request method is not supported for the requested resource.")
	ErrNotAcceptable                = NewError(http.StatusNotAcceptable, "not_acceptable", "The server cannot produce a response matching the requirements of the request.")
	ErrProxyAuthRequired            = NewError(http.StatusProxyAuthRequired, "proxy_auth_required", "The request requires authentication with a proxy.")
	ErrRequestTimeout               = NewError(http.StatusRequestTimeout, "request_timeout", "The server timed out waiting for the request.")
	ErrConflict                     = NewError(http.StatusConflict, "conflict", "The request could not be completed due to a conflict with the current state of the resource.")
	ErrGone                         = NewError(http.StatusGone, "gone", "The requested resource is no longer available and will not be available again.")
	ErrLengthRequired               = NewError(http.StatusLengthRequired, "length_required", "The request requires a valid Content-Length header.")
	ErrPreconditionFailed           = NewError(http.StatusPreconditionFailed, "precondition_failed", "One or more conditions given in the request headers were not met.")
	ErrRequestEntityTooLarge        = NewError(http.StatusRequestEntityTooLarge, "request_entity_too_large", "The request payload is larger than the server is willing or able to process.")
	ErrRequestUriTooLong            = NewError(http.StatusRequestURITooLong, "request_uri_too_long", "The request URI is longer than the server is willing to interpret.")
	ErrUnsupportedMediaType         = NewError(http.StatusUnsupportedMediaType, "unsupported_media_type", "The request payload format is not supported by the server.")
	ErrRequestedRangeNotSatisfiable = NewError(http.StatusRequestedRangeNotSatisfiable, "request_range_not_satisfiable", "The requested range cannot be satisfied.")
	ErrExpectationFailed            = NewError(http.StatusExpectationFailed, "expectation_failed", "The expectation given in the request could not be met by the server.")
	ErrTeapot                       = NewError(http.StatusTeapot, "teapot", "The server refuses to brew coffee because it is, permanently, a teapot.")
	ErrMisdirectedRequest           = NewError(http.StatusMisdirectedRequest, "misdirected_request", "The request was directed at a server that is not able to produce a response.")
	ErrUnprocessableEntity          = NewError(http.StatusUnprocessableEntity, "unprocessable_entity", "The server understands the request but cannot process the contained instructions.")
	ErrLocked                       = NewError(http.StatusLocked, "locked", "The requested resource is locked.")
	ErrFailedDependency             = NewError(http.StatusFailedDependency, "failed_dependency", "The request failed because it depended on another request that failed.")
	ErrTooEarly                     = NewError(http.StatusTooEarly, "too_early", "The server is unwilling to risk processing the request because it may be replayed.")
	ErrUpgradeRequired              = NewError(http.StatusUpgradeRequired, "upgrade_required", "The server requires the client to use a different protocol.")
	ErrPreconditionRequired         = NewError(http.StatusPreconditionRequired, "precondition_required", "The server requires the request to contain a precondition.")
	ErrTooManyRequests              = NewError(http.StatusTooManyRequests, "too_many_requests", "The client has sent too many requests in a given amount of time.")
	ErrRequestHeaderFieldsTooLarge  = NewError(http.StatusRequestHeaderFieldsTooLarge, "request_header_fields_too_large", "Request's header fields are too large.")
	ErrUnavailableForLegalReasons   = NewError(http.StatusUnavailableForLegalReasons, "unavailable_for_legal_reasons", "The requested resource is unavailable for legal reasons.")

	ErrInternalServerError           = NewError(http.StatusInternalServerError, "internal_server_error", "An unexpected server error occurred.")
	ErrNotImplemented                = NewError(http.StatusNotImplemented, "not_implemented", "The requested functionality is not supported.")
	ErrBadGateway                    = NewError(http.StatusBadGateway, "bad_gateway", "The upstream server returned an invalid response.")
	ErrServiceUnavailable            = NewError(http.StatusServiceUnavailable, "service_unavailable", "The service is temporarily unavailable.")
	ErrGatewayTimeout                = NewError(http.StatusGatewayTimeout, "gateway_timeout", "The upstream server did not respond in time.")
	ErrHttpVersionNotSupported       = NewError(http.StatusHTTPVersionNotSupported, "http_version_not_supported", "The HTTP version is not supported.")
	ErrVariantAlsoNegotiates         = NewError(http.StatusVariantAlsoNegotiates, "variant_also_negotiates", "The server encountered a content negotiation error.")
	ErrInsufficientStorage           = NewError(http.StatusInsufficientStorage, "insufficient_storage", "The server cannot store the required data.")
	ErrLoopDetected                  = NewError(http.StatusLoopDetected, "loop_detected", "The server detected an infinite loop.")
	ErrNotExtended                   = NewError(http.StatusNotExtended, "not_extended", "The request requires additional extensions.")
	ErrNetworkAuthenticationRequired = NewError(http.StatusNetworkAuthenticationRequired, "network_authentication_required", "Network authentication is required.")
)

// ErrorHandler handles an error returned by the handler chain.
// This includes [ErrInvalidEndpoint] for unknown paths and
// [ErrMethodNotAllowed] for unregistered methods, so a custom handler can
// inspect or override them.
// Use the [Context] Write methods to send the response.
// Set it via [WithErrorHandler]. Request logging, when enabled via
// [WithRequestLogging], runs after the handler and logs the error.
type ErrorHandler func(ctx *Context, err error)

// DefaultErrorHandler writes an [Error] as JSON with its status code,
// stripping Details for internal_server_error. Any other error becomes a
// generic [ErrInternalServerError] response.
func DefaultErrorHandler(ctx *Context, err error) {
	if e, ok := errors.AsType[Error](err); ok {
		if errors.Is(e, ErrInternalServerError) {
			e.Details = nil
		}
		ctx.WriteAs(e.StatusCode, CodecJson, e)
	} else {
		ctx.WriteAs(http.StatusInternalServerError, CodecJson, ErrInternalServerError)
	}
}
