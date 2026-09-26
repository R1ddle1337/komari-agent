package utils

import (
	"errors"
	"net/url"
	"strconv"
	"strings"
)

type sanitizedHTTPError struct {
	message string
	cause   error
}

func (err *sanitizedHTTPError) Error() string { return err.message }
func (err *sanitizedHTTPError) Unwrap() error { return err.cause }

// SanitizeHTTPError retains the method, host, path and cause while removing
// credentials from URL errors before they reach logs or remote error results.
// The original error chain remains available to errors.Is and errors.As.
func SanitizeHTTPError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	for cause := err; cause != nil; cause = errors.Unwrap(cause) {
		urlErr, ok := cause.(*url.Error)
		if !ok || urlErr.URL == "" {
			continue
		}
		safeURL := "[invalid URL]"
		if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil {
			parsed.User = nil
			parsed.RawQuery = ""
			parsed.ForceQuery = false
			parsed.Fragment = ""
			parsed.RawFragment = ""
			safeURL = parsed.String()
		}
		message = strings.ReplaceAll(message, strconv.Quote(urlErr.URL), strconv.Quote(safeURL))
		message = strings.ReplaceAll(message, urlErr.URL, safeURL)
	}
	if message == err.Error() {
		return err
	}
	return &sanitizedHTTPError{message: message, cause: err}
}
