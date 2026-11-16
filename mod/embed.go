// Package mod embeds the Herdr mod so `herdr-share mod install` can put it
// in place without a separate download.
package mod

import "embed"

// Name is the mod's folder name under the Herdr app's mods/.
const Name = "herdr-share"

// Files holds herdr-share/ (main.js, mod.json, assets).
//
//go:embed all:herdr-share
var Files embed.FS
