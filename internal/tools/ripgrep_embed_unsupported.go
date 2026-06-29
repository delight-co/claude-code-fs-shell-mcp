//go:build !(linux && amd64) && !(linux && arm64) && !(darwin && amd64) && !(darwin && arm64) && !(windows && amd64)

package tools

const rgBinaryExt = ""

// embeddedRipgrepBytes returns nil on platforms outside the supported
// set, which causes ResolveRipgrep to fall through to a PATH lookup.
func embeddedRipgrepBytes() []byte { return nil }
