package controlurl

import (
	"errors"
	"fmt"
	"net/url"
)

// RedactError drops the URL from an HTTP client's error. Telegram's bot token and
// the reseller API's secret are part of the URL, and http.Client quotes the whole
// of it in every error it returns, from where it reaches logs and API responses.
func RedactError(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return fmt.Errorf("%s: %w", urlErr.Op, urlErr.Err)
	}
	return err
}
