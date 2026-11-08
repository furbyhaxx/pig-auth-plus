package oauthx

import (
	"context"
	"errors"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/httpx"
)

// Context is the login's cancellation context; an interaction without one never cancels.
func Context(in sdk.AuthInteraction) context.Context {
	if in.Signal != nil {
		return in.Signal
	}
	return context.Background()
}

// Retry runs do up to three times, waiting base, then 2×base, after an HTTP 5xx or a transport
// failure. A 4xx, a permanent error and cancellation return at once.
func Retry(ctx context.Context, base time.Duration, permanent func(error) bool, do func() error) error {
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(base << (attempt - 1)):
			}
		}
		err := do()
		if err == nil || ctx.Err() != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
		if attempt >= 2 || !transient(err, permanent) {
			return err
		}
	}
}

func transient(err error, permanent func(error) bool) bool {
	var se *httpx.StatusError
	if errors.As(err, &se) {
		return se.Status >= 500
	}
	return permanent == nil || !permanent(err)
}
