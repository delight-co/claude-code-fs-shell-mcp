//go:build darwin && amd64

package tools

import _ "embed"

//go:embed ripgrep_binaries/rg_darwin_amd64
var embeddedRgBinary []byte

const rgBinaryExt = ""

func embeddedRipgrepBytes() []byte { return embeddedRgBinary }
