// Package herdr holds the herdr.dev listing of the Herdr mod
// (herdr/herdr-share: RECIPE.md, preview.png, keys.json, mods/) and embeds
// the mod so `herdr-share mod install` can put it in place.
package herdr

import (
	"embed"
	"io/fs"
)

// Name is the mod's folder name under the Herdr app's mods/.
const Name = "herdr-share"

//go:embed all:herdr-share/mods/herdr-share
var listing embed.FS

// Files holds herdr-share/ (main.js, mod.json), as under the app's mods/.
var Files = func() fs.FS {
	sub, err := fs.Sub(listing, "herdr-share/mods")
	if err != nil {
		panic(err)
	}
	return sub
}()
