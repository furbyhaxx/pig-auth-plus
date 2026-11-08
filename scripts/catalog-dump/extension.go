// Package catalogdump writes PiG's builtin catalog of one provider to a file; `just catalog`
// uses it to refresh the bundled first-run catalogs.
package catalogdump

import (
	"encoding/json"
	"errors"
	"os"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Extension registers /catalog-dump <provider>, writing to $CATALOG_DUMP_OUT.
func Extension() *sdk.Extension {
	e := sdk.New("catalog-dump")
	e.RegisterCommand("catalog-dump", sdk.CommandOptions{
		Description: "Write a builtin provider catalog to $CATALOG_DUMP_OUT",
		Handler: func(c sdk.Context, args string) error {
			out := os.Getenv("CATALOG_DUMP_OUT")
			provider := strings.TrimSpace(args)
			if out == "" || provider == "" {
				return errors.New("usage: CATALOG_DUMP_OUT=<file> /catalog-dump <provider>")
			}
			models, err := c.ModelRegistry().GetModelsOfType("chat", provider)
			if err != nil {
				return err
			}
			if len(models) == 0 {
				return errors.New("no models for " + provider)
			}
			b, err := json.MarshalIndent(models, "", "\t")
			if err != nil {
				return err
			}
			return os.WriteFile(out, append(b, '\n'), 0o644)
		},
	})
	return e
}
