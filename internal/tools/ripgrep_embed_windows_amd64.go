//go:build windows && amd64

package tools

import _ "embed"

//go:embed ripgrep_binaries/rg_windows_amd64.exe
var embeddedRgBinary []byte

const rgBinaryExt = ".exe"

func embeddedRipgrepBytes() []byte { return embeddedRgBinary }
