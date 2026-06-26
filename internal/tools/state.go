package tools

// ReadEntry is a cached record of a successful Read on a single file
// within a single MCP session. It carries the data the Write tool
// family needs to enforce the read-before-overwrite and
// modified-since-read contracts.
//
// For full reads (Offset == 0 and Limit == 0), Content holds the file
// contents after CRLF→LF normalisation and ContentHash holds a SHA-1
// digest (base64url-encoded, no padding) of that normalised content.
// For partial reads, Content and ContentHash are zero values: the
// modified-since-read check refuses unconditionally when the original
// read was partial and mtime has advanced.
type ReadEntry struct {
	Content       []byte
	ContentHash   string
	ModTimeMillis int64
	Offset        int
	Limit         int
	IsPartialView bool
}

// ReadStateSeed is the interface tool handlers use to record successful
// reads with the server-level state registry. The state registry lives
// in the server package; this interface lives in tools to keep the
// dependency arrow pointing from server → tools.
type ReadStateSeed interface {
	Seed(sessionID, path string, entry ReadEntry)
}
