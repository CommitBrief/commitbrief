// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import "errors"

var (
	ErrUnknownProvider   = errors.New("provider: unknown provider")
	ErrUnauthorized      = errors.New("provider: unauthorized (check API key)")
	ErrRateLimit         = errors.New("provider: rate limit exceeded")
	ErrContextTooLong    = errors.New("provider: input exceeds model context window")
	ErrModelNotSupported = errors.New("provider: model not supported by this provider")
	ErrTimeout           = errors.New("provider: request timed out")
)
