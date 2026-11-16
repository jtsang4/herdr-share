// Package mod embeds the Herdr mods so `herdr-share mod install` can put them
// in place without a separate download.
package mod

import "embed"

// Name is the Share dialog's folder name under the Herdr app's mods/.
const Name = "herdr-share"

// Button is the floating Share button's folder name.
const Button = "herdr-share-button"

// Names lists every mod folder herdr-share installs.
var Names = []string{Name, Button}

// Files holds herdr-share/ (the Share dialog) and herdr-share-button/.
//
//go:embed all:herdr-share all:herdr-share-button
var Files embed.FS
