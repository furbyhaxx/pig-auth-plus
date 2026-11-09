// Package pigauthplus is the pig-auth-plus factory: registration and wiring only.
// Feature logic lives in internal/ as plain functions over plain data.
package pigauthplus

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/furbyhaxx/pig-auth-plus/internal/app"
)

// name is the extension identity; it must equal the selecting directory or Package member.
const name = "pig-auth-plus"

// Extension is the conventional Go factory PiG builds, packs and can fuse into a Piglet Binary.
func Extension() *sdk.Extension {
	e := sdk.New(name)
	app.Register(e)
	return e
}
