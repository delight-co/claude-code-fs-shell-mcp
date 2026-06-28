package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectReadEquivalents_CatFamily(t *testing.T) {
	cases := []struct {
		cmd      string
		wantLen  int
		wantPath string
	}{
		{"cat /tmp/foo.txt", 1, "/tmp/foo.txt"},
		{"cat -n /tmp/foo.txt", 1, "/tmp/foo.txt"},
		{"cat --number /tmp/foo.txt", 1, "/tmp/foo.txt"},
		{"cat /tmp/foo.txt /tmp/bar.txt", 0, ""},
		{"cat -v /tmp/foo.txt", 0, ""},
		{"nl /tmp/foo.txt", 1, "/tmp/foo.txt"},
		{"bat -p /tmp/foo.txt", 1, "/tmp/foo.txt"},
		{"batcat --plain /tmp/foo.txt", 1, "/tmp/foo.txt"},
	}
	for _, c := range cases {
		got := detectReadEquivalents(c.cmd)
		if len(got) != c.wantLen {
			t.Errorf("detectReadEquivalents(%q): len=%d, want %d", c.cmd, len(got), c.wantLen)
			continue
		}
		if c.wantLen > 0 && got[0].Path != c.wantPath {
			t.Errorf("detectReadEquivalents(%q): path=%q, want %q", c.cmd, got[0].Path, c.wantPath)
		}
	}
}

func TestDetectReadEquivalents_Sed(t *testing.T) {
	cases := []struct {
		cmd              string
		wantOff, wantLim int
	}{
		{"sed -n '5p' /tmp/foo.txt", 5, 1},
		{"sed -n '5,10p' /tmp/foo.txt", 5, 6},
		{"sed -n '100,200p' /tmp/foo.txt", 100, 101},
	}
	for _, c := range cases {
		got := detectReadEquivalents(c.cmd)
		if len(got) != 1 {
			t.Errorf("detectReadEquivalents(%q): len=%d, want 1", c.cmd, len(got))
			continue
		}
		if got[0].Offset != c.wantOff || got[0].Limit != c.wantLim {
			t.Errorf("detectReadEquivalents(%q): offset=%d limit=%d, want %d/%d",
				c.cmd, got[0].Offset, got[0].Limit, c.wantOff, c.wantLim)
		}
	}

	if got := detectReadEquivalents("sed -i 's/a/b/' /tmp/foo.txt"); got != nil {
		t.Errorf("sed -i should not be recognised: %+v", got)
	}
	if got := detectReadEquivalents("sed -n -e '5p' /tmp/foo.txt"); got != nil {
		t.Errorf("sed -e should not be recognised: %+v", got)
	}
}

func TestDetectReadEquivalents_Head(t *testing.T) {
	cases := []struct {
		cmd     string
		wantLim int
	}{
		{"head /tmp/foo.txt", 10},
		{"head -n 5 /tmp/foo.txt", 5},
		{"head -5 /tmp/foo.txt", 5},
		{"head --lines=20 /tmp/foo.txt", 20},
	}
	for _, c := range cases {
		got := detectReadEquivalents(c.cmd)
		if len(got) != 1 {
			t.Errorf("detectReadEquivalents(%q): len=%d, want 1", c.cmd, len(got))
			continue
		}
		if got[0].Limit != c.wantLim || got[0].Offset != 1 {
			t.Errorf("detectReadEquivalents(%q): offset=%d limit=%d, want 1/%d",
				c.cmd, got[0].Offset, got[0].Limit, c.wantLim)
		}
	}
}

func TestDetectReadEquivalents_Tail(t *testing.T) {
	got := detectReadEquivalents("tail -n 3 /tmp/foo.txt")
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d", len(got))
	}
	if got[0].TailLines != 3 {
		t.Errorf("TailLines=%d, want 3", got[0].TailLines)
	}
}

func TestDetectReadEquivalents_Grep(t *testing.T) {
	got := detectReadEquivalents("grep foo /tmp/foo.txt")
	if len(got) != 1 || !got[0].RequiresExitZero || got[0].Path != "/tmp/foo.txt" {
		t.Errorf("unexpected: %+v", got)
	}

	if got := detectReadEquivalents("grep foo /tmp/a /tmp/b"); got != nil {
		t.Errorf("multi-file grep should not be recognised: %+v", got)
	}
}

func TestDetectReadEquivalents_Rg(t *testing.T) {
	got := detectReadEquivalents("rg foo /tmp/foo.txt")
	if len(got) != 1 || !got[0].RequiresExitZero {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestDetectReadEquivalents_PipeReject(t *testing.T) {
	if got := detectReadEquivalents("cat /tmp/foo.txt | grep bar"); got != nil {
		t.Errorf("pipe should reject: %+v", got)
	}
	if got := detectReadEquivalents("cat /tmp/foo.txt > /tmp/bar.txt"); got != nil {
		t.Errorf("output redirect should reject: %+v", got)
	}
	if got := detectReadEquivalents("cat < /tmp/foo.txt"); got != nil {
		t.Errorf("input redirect should reject: %+v", got)
	}
}

func TestDetectReadEquivalents_MultiCmd(t *testing.T) {
	got := detectReadEquivalents("cat /tmp/a && head -5 /tmp/b")
	if len(got) != 2 {
		t.Errorf("expected 2 results, got %d", len(got))
	}

	got = detectReadEquivalents("echo hello && cat /tmp/foo.txt")
	if len(got) != 1 {
		t.Errorf("expected 1 result with filler, got %d", len(got))
	}

	if got := detectReadEquivalents("cat /tmp/a && grep foo /tmp/b"); got != nil {
		t.Errorf("grep in multi-cmd should reject: %+v", got)
	}

	if got := detectReadEquivalents("cat /tmp/a && rm /tmp/b"); got != nil {
		t.Errorf("unrecognised sub-cmd should reject: %+v", got)
	}
}

// In-memory mock for ReadStateSeed.
type seedKey struct{ sid, path string }

type memReg struct {
	cache map[seedKey]ReadEntry
}

func newMemReg() *memReg { return &memReg{cache: map[seedKey]ReadEntry{}} }

func (m *memReg) Seed(sid, path string, entry ReadEntry) {
	m.cache[seedKey{sid, path}] = entry
}

func TestSeedReadEquivalents_FullRead(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "foo.txt")
	if err := os.WriteFile(p, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := newMemReg()
	seedReadEquivalents(context.Background(), reg, "sid-1", []readEquivCmd{{Path: p}}, 0)
	entry, ok := reg.cache[seedKey{"sid-1", p}]
	if !ok {
		t.Fatalf("entry not seeded")
	}
	if string(entry.Content) != "hello\nworld\n" {
		t.Errorf("content: %q", entry.Content)
	}
	if entry.Offset != 0 || entry.Limit != 0 {
		t.Errorf("offset=%d limit=%d, want 0/0", entry.Offset, entry.Limit)
	}
}

func TestSeedReadEquivalents_GrepExitNonZero(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "foo.txt")
	if err := os.WriteFile(p, []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := newMemReg()
	seedReadEquivalents(context.Background(), reg, "sid-1", []readEquivCmd{{Path: p, RequiresExitZero: true}}, 1)
	if _, ok := reg.cache[seedKey{"sid-1", p}]; ok {
		t.Errorf("entry should not be seeded when grep exit != 0")
	}
}

func TestSeedReadEquivalents_FileSizeCapped(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "big.bin")
	big := make([]byte, readEquivFileSizeCap+1)
	if err := os.WriteFile(p, big, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := newMemReg()
	seedReadEquivalents(context.Background(), reg, "sid-1", []readEquivCmd{{Path: p}}, 0)
	if _, ok := reg.cache[seedKey{"sid-1", p}]; ok {
		t.Errorf("entry should not be seeded when file size > 10 MiB")
	}
}

func TestSeedReadEquivalents_TailRange(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "lines.txt")
	if err := os.WriteFile(p, []byte("l1\nl2\nl3\nl4\nl5\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg := newMemReg()
	seedReadEquivalents(context.Background(), reg, "sid-1", []readEquivCmd{{Path: p, TailLines: 3}}, 0)
	entry, ok := reg.cache[seedKey{"sid-1", p}]
	if !ok {
		t.Fatalf("entry not seeded")
	}
	if entry.Offset != 3 || entry.Limit != 3 {
		t.Errorf("offset=%d limit=%d, want 3/3", entry.Offset, entry.Limit)
	}
}

func TestSeedReadEquivalents_EmptySessionID(t *testing.T) {
	reg := newMemReg()
	seedReadEquivalents(context.Background(), reg, "", []readEquivCmd{{Path: "/nonexistent"}}, 0)
	if len(reg.cache) != 0 {
		t.Errorf("empty session id should not seed: %+v", reg.cache)
	}
}
