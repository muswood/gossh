// owner: muswood | Email: mumu920@outlook.com
package ssh

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSessionLogWritesByDateAndFlushesImmediately(t *testing.T) {
	root := t.TempDir()
	createdAt := time.Date(2026, time.August, 12, 15, 4, 5, 0, time.FixedZone("CST", 8*60*60))
	log, err := openSessionLog(root, "conn-123-987654321", createdAt)
	if err != nil {
		t.Fatal(err)
	}

	wantPath := filepath.Join(root, "2026-08-12", "conn-123-987654321.log")
	if log.path != wantPath {
		t.Fatalf("log path = %q, want %q", log.path, wantPath)
	}
	if mode := fileMode(t, root); mode.Perm() != 0700 {
		t.Fatalf("log root mode = %o, want 700", mode.Perm())
	}
	if mode := fileMode(t, filepath.Dir(wantPath)); mode.Perm() != 0700 {
		t.Fatalf("log directory mode = %o, want 700", mode.Perm())
	}
	if mode := fileMode(t, wantPath); mode.Perm() != 0600 {
		t.Fatalf("log mode = %o, want 600", mode.Perm())
	}
	if err := log.Write([]byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, wantPath); got != "first\n" {
		t.Fatalf("flushed log = %q, want first line", got)
	}
	if err := log.Write([]byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, wantPath); got != "first\nsecond\n" {
		t.Fatalf("log = %q, want two lines", got)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLogSerializesConcurrentWrites(t *testing.T) {
	root := t.TempDir()
	log, err := openSessionLog(root, "session", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	const writers = 32
	var group sync.WaitGroup
	for i := 0; i < writers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := log.Write([]byte("record\n")); err != nil {
				t.Errorf("Write: %v", err)
			}
		}()
	}
	group.Wait()
	if got := strings.Count(readFile(t, log.path), "record\n"); got != writers {
		t.Fatalf("written records = %d, want %d", got, writers)
	}
}

func TestSessionLogReadsByOffset(t *testing.T) {
	log, err := openSessionLog(t.TempDir(), "session", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if err := log.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	got, next, eof, err := log.Read(2, 3)
	if err != nil || string(got) != "cde" || next != 5 || eof {
		t.Fatalf("Read = %q, %d, %v, %v", got, next, eof, err)
	}
	got, next, eof, err = log.Read(next, 3)
	if err != nil || string(got) != "f" || next != 6 || !eof {
		t.Fatalf("final Read = %q, %d, %v, %v", got, next, eof, err)
	}
}

func TestSessionLogKeepsSessionIDFilenameSafe(t *testing.T) {
	log, err := openSessionLog(t.TempDir(), "connection/with\\path", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	if strings.Contains(filepath.Base(log.path), string(filepath.Separator)) {
		t.Fatalf("unsafe log filename: %q", log.path)
	}
}

func TestManagerAppendSessionLogPreservesRawBytes(t *testing.T) {
	log, err := openSessionLog(t.TempDir(), "session", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()

	manager := NewManager()
	manager.sessions["session"] = &Session{ID: "session", log: log}
	want := []byte{0x00, 0x1b, 0xff, '\r', '\n'}
	if err := manager.AppendSessionLog("session", want); err != nil {
		t.Fatal(err)
	}
	if got := []byte(readFile(t, log.path)); string(got) != string(want) {
		t.Fatalf("logged bytes = %v, want %v", got, want)
	}
	if err := manager.AppendSessionLog("missing", []byte("x")); err == nil {
		t.Fatal("missing session append unexpectedly succeeded")
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
