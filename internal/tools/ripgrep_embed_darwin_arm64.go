//go:build darwin && arm64

package tools

import _ "embed"

//go:embed ripgrep_binaries/rg_darwin_arm64
var embeddedRgBinary []byte

const rgBinaryExt = ""

func embeddedRipgrepBytes() []byte { return embeddedRgBinary }
