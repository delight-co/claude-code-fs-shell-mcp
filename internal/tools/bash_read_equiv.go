package tools

import (
	"bytes"
	"context"
	"os"
	"strconv"
	"strings"
)

// readEquivCmd describes a single recognised read-equivalent shell
// invocation: the file it targeted, the line window it read (or zero
// values for a full read), whether the recognition is gated on the
// command's exit status, and a special tail-mode signal.
type readEquivCmd struct {
	Path             string
	Offset           int // 1-based; 0 means full read
	Limit            int // line count; 0 means full read
	RequiresExitZero bool
	// TailLines is non-zero only for `tail -N`-shaped commands.
	// seedReadEquivalents converts it to a concrete (offset, limit)
	// using the file's line count at seed time.
	TailLines int
}

// readEquivFileSizeCap is the upstream 10 MiB cap above which a
// read-equivalent is not seeded.
const readEquivFileSizeCap = 10 * 1024 * 1024

// detectReadEquivalents inspects a shell command and returns the
// recognised single-file read invocations it contains. The return
// value is empty when the command is not recognisable as a read-
// equivalent (for example because it uses a shell pipe or redirect,
// or when a multi-command sequence includes an unrecognised part).
//
// The recognised set and rules are pinned in docs/spec/bash.md under
// "Read-equivalent shell commands".
func detectReadEquivalents(command string) []readEquivCmd {
	if strings.ContainsAny(command, "|<>") {
		return nil
	}
	parts := splitShellSequence(command)
	if len(parts) == 0 {
		return nil
	}
	multiCmd := len(parts) > 1
	var out []readEquivCmd
	for _, p := range parts {
		if c := parseCatLike(p); c != nil {
			out = append(out, *c)
			continue
		}
		if c := parseSed(p); c != nil {
			out = append(out, *c)
			continue
		}
		if c := parseHead(p); c != nil {
			out = append(out, *c)
			continue
		}
		if c := parseTail(p); c != nil {
			out = append(out, *c)
			continue
		}
		if !multiCmd {
			if c := parseGrepLike(p); c != nil {
				out = append(out, *c)
				continue
			}
			if c := parseRg(p); c != nil {
				out = append(out, *c)
				continue
			}
		}
		if multiCmd && isFiller(p) {
			continue
		}
		// Unrecognised sub-command in a multi-command sequence:
		// reject the whole detection result.
		return nil
	}
	return out
}

// splitShellSequence splits a command on `;`, `&&`, `||` boundaries
// outside of quoted strings and returns the trimmed sub-commands.
func splitShellSequence(command string) []string {
	var parts []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			parts = append(parts, s)
		}
		cur.Reset()
	}
	i := 0
	for i < len(command) {
		c := command[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
			cur.WriteByte(c)
			i++
		case c == '"' && !inSingle:
			inDouble = !inDouble
			cur.WriteByte(c)
			i++
		case !inSingle && !inDouble && c == ';':
			flush()
			i++
		case !inSingle && !inDouble && i+1 < len(command) && c == '&' && command[i+1] == '&':
			flush()
			i += 2
		case !inSingle && !inDouble && i+1 < len(command) && c == '|' && command[i+1] == '|':
			flush()
			i += 2
		default:
			cur.WriteByte(c)
			i++
		}
	}
	flush()
	return parts
}

// isFiller reports whether cmd is a tolerated filler inside a
// multi-command sequence (echo, printf, true, : with optional args).
func isFiller(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	for _, prefix := range []string{"echo", "printf", "true", ":"} {
		if trimmed == prefix {
			return true
		}
		if strings.HasPrefix(trimmed, prefix+" ") || strings.HasPrefix(trimmed, prefix+"\t") {
			return true
		}
	}
	return false
}

// tokeniseShellArgs splits a sub-command into argv-like tokens,
// honouring single and double quotes.
func tokeniseShellArgs(cmd string) []string {
	var tokens []string
	var cur strings.Builder
	inSingle := false
	inDouble := false
	pushToken := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case (c == ' ' || c == '\t') && !inSingle && !inDouble:
			pushToken()
		default:
			cur.WriteByte(c)
		}
	}
	pushToken()
	return tokens
}

// parseCatLike covers cat / nl / bat / batcat (single-file reads
// with a small allowed flag set).
func parseCatLike(cmd string) *readEquivCmd {
	tokens := tokeniseShellArgs(cmd)
	if len(tokens) == 0 {
		return nil
	}
	var allowed map[string]struct{}
	switch tokens[0] {
	case "cat":
		allowed = map[string]struct{}{"-n": {}, "--number": {}}
	case "nl":
		allowed = map[string]struct{}{}
	case "bat", "batcat":
		allowed = map[string]struct{}{"-n": {}, "--number": {}, "-p": {}, "--plain": {}}
	default:
		return nil
	}
	var file string
	for _, t := range tokens[1:] {
		if strings.HasPrefix(t, "-") {
			if _, ok := allowed[t]; !ok {
				return nil
			}
			continue
		}
		if file != "" {
			return nil
		}
		file = t
	}
	if file == "" || file == "-" {
		return nil
	}
	return &readEquivCmd{Path: file}
}

// parseSed covers sed -n 'Np' / sed -n 'N,Mp'.
func parseSed(cmd string) *readEquivCmd {
	tokens := tokeniseShellArgs(cmd)
	if len(tokens) < 4 || tokens[0] != "sed" {
		return nil
	}
	seenN := false
	var pattern, file string
	for i := 1; i < len(tokens); i++ {
		t := tokens[i]
		switch {
		case t == "-n" || t == "--quiet" || t == "--silent":
			seenN = true
		case t == "-i" || strings.HasPrefix(t, "--in-place") || t == "-e" || strings.HasPrefix(t, "-e="):
			return nil
		case pattern == "":
			pattern = strings.Trim(t, "'\"")
		case file == "":
			file = t
		default:
			return nil
		}
	}
	if !seenN || pattern == "" || file == "" {
		return nil
	}
	pattern = strings.TrimSuffix(pattern, "p")
	if pattern == "" {
		return nil
	}
	var start, end int
	var err error
	if comma := strings.Index(pattern, ","); comma >= 0 {
		start, err = strconv.Atoi(pattern[:comma])
		if err != nil {
			return nil
		}
		end, err = strconv.Atoi(pattern[comma+1:])
		if err != nil {
			return nil
		}
	} else {
		start, err = strconv.Atoi(pattern)
		if err != nil {
			return nil
		}
		end = start
	}
	if start < 1 || end < start {
		return nil
	}
	return &readEquivCmd{Path: file, Offset: start, Limit: end - start + 1}
}

// parseHead covers head [-n N | -N | --lines=N].
func parseHead(cmd string) *readEquivCmd { return parseHeadOrTail(cmd, "head", false) }

// parseTail covers tail [-n N | -N | --lines=N].
func parseTail(cmd string) *readEquivCmd { return parseHeadOrTail(cmd, "tail", true) }

func parseHeadOrTail(cmd, verb string, isTail bool) *readEquivCmd {
	tokens := tokeniseShellArgs(cmd)
	if len(tokens) < 2 || tokens[0] != verb {
		return nil
	}
	count := 10
	var file string
	i := 1
	for i < len(tokens) {
		t := tokens[i]
		switch {
		case t == "-n":
			if i+1 >= len(tokens) {
				return nil
			}
			n, err := strconv.Atoi(tokens[i+1])
			if err != nil || n < 1 {
				return nil
			}
			count = n
			i += 2
		case strings.HasPrefix(t, "--lines="):
			n, err := strconv.Atoi(strings.TrimPrefix(t, "--lines="))
			if err != nil || n < 1 {
				return nil
			}
			count = n
			i++
		case strings.HasPrefix(t, "-") && len(t) > 1 && t[1] >= '0' && t[1] <= '9':
			n, err := strconv.Atoi(t[1:])
			if err != nil || n < 1 {
				return nil
			}
			count = n
			i++
		case strings.HasPrefix(t, "-"):
			return nil
		default:
			if file != "" {
				return nil
			}
			file = t
			i++
		}
	}
	if file == "" || file == "-" {
		return nil
	}
	if isTail {
		return &readEquivCmd{Path: file, TailLines: count}
	}
	return &readEquivCmd{Path: file, Offset: 1, Limit: count}
}

// parseGrepLike covers grep / egrep / fgrep (single command, requires
// exit zero).
func parseGrepLike(cmd string) *readEquivCmd {
	tokens := tokeniseShellArgs(cmd)
	if len(tokens) < 3 {
		return nil
	}
	switch tokens[0] {
	case "grep", "egrep", "fgrep":
	default:
		return nil
	}
	return parseGrepRgCommon(tokens[1:])
}

// parseRg covers rg (single command, requires exit zero).
func parseRg(cmd string) *readEquivCmd {
	tokens := tokeniseShellArgs(cmd)
	if len(tokens) < 3 || tokens[0] != "rg" {
		return nil
	}
	return parseGrepRgCommon(tokens[1:])
}

func parseGrepRgCommon(args []string) *readEquivCmd {
	var nonFlagCount int
	var file string
	for _, t := range args {
		if strings.HasPrefix(t, "-") {
			continue
		}
		nonFlagCount++
		if nonFlagCount == 2 {
			file = t
		}
		if nonFlagCount > 2 {
			return nil
		}
	}
	if file == "" || file == "-" || strings.ContainsAny(file, "*?[{") {
		return nil
	}
	return &readEquivCmd{Path: file, RequiresExitZero: true}
}

// seedReadEquivalents records each recognised read-equivalent into the
// session's read-tracking state. Honours the upstream exit-zero gate
// for grep/rg results and the 10 MiB file-size cap.
func seedReadEquivalents(ctx context.Context, registry ReadStateSeed, sessionID string, results []readEquivCmd, exitCode int) {
	if sessionID == "" || registry == nil {
		return
	}
	for _, r := range results {
		if ctx.Err() != nil {
			return
		}
		if r.RequiresExitZero && exitCode != 0 {
			continue
		}
		info, err := os.Stat(r.Path)
		if err != nil {
			continue
		}
		if info.Size() > readEquivFileSizeCap {
			continue
		}
		raw, err := os.ReadFile(r.Path)
		if err != nil {
			continue
		}
		normalised := bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		offset := r.Offset
		limit := r.Limit
		if r.TailLines > 0 {
			lines := strings.Split(strings.TrimRight(string(normalised), "\n"), "\n")
			n := r.TailLines
			if n > len(lines) {
				n = len(lines)
			}
			if n == 0 {
				continue
			}
			offset = len(lines) - n + 1
			limit = n
		}
		entry := ReadEntry{
			Content:       normalised,
			ContentHash:   hashContent(normalised),
			ModTimeMillis: info.ModTime().UnixMilli(),
			Offset:        offset,
			Limit:         limit,
		}
		registry.Seed(sessionID, r.Path, entry)
	}
}
