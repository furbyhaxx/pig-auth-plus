// Package apikey resolves and collects provider API keys the way PiG's builtin providers do.
package apikey

import (
	"errors"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// ErrEmpty is returned when the user submits a blank key.
var ErrEmpty = errors.New("API key cannot be empty")

// Resolve prefers the stored key, then the environment variable env. It returns nil without a key.
func Resolve(in sdk.APIKeyAuthInput, env string) (*sdk.AuthResult, error) {
	if key := in.Credential.String("key"); key != "" {
		src := "stored API key"
		return &sdk.AuthResult{Auth: map[string]any{"apiKey": key}, Source: &src}, nil
	}
	if env == "" || in.Ctx.Env == nil {
		return nil, nil
	}
	v, err := in.Ctx.Env(env)
	if err != nil {
		return nil, err
	}
	if v == nil || strings.TrimSpace(*v) == "" {
		return nil, nil
	}
	src := env
	return &sdk.AuthResult{Auth: map[string]any{"apiKey": strings.TrimSpace(*v)}, Source: &src}, nil
}

// Prompt asks for a key with message and returns the credential to store.
func Prompt(in sdk.AuthInteraction, message string) (*sdk.Credential, error) {
	key, err := in.Prompt(map[string]any{"type": "secret", "message": message})
	if err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, ErrEmpty
	}
	return sdk.NewCredential("type", "api_key", "key", key), nil
}

// Auth is a plain API-key method: a prompted key or the environment variable env.
func Auth(name, env, message string) *sdk.APIKeyAuth {
	return &sdk.APIKeyAuth{
		Name:    name,
		Login:   func(in sdk.AuthInteraction) (*sdk.Credential, error) { return Prompt(in, message) },
		Resolve: func(in sdk.APIKeyAuthInput) (*sdk.AuthResult, error) { return Resolve(in, env) },
	}
}
