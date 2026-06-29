//go:build linux && arm64

package tools

import _ "embed"

//go:embed ripgrep_binaries/rg_linux_arm64
var embeddedRgBinary []byte

const rgBinaryExt = ""

func embeddedRipgrepBytes() []byte { return embeddedRgBinary }
