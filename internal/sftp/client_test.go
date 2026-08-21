// owner: muswood | Email: mumu920@outlook.com
package sftp

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedicatedClientClosesAuthCloser(t *testing.T) {
	closed := 0
	client := NewDedicatedClient(nil, nil, closeFunc(func() error {
		closed++
		return nil
	}))
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("auth closer calls = %d, want 1", closed)
	}
}

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func TestDigestLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("verified payload"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := digestLocal(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := digestLocal(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("same file returned different digest")
	}
}

func TestDigestHonorsCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	if err := os.WriteFile(path, []byte("payload"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := digestLocal(ctx, path); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestReadLineRangeReturnsContinuationMetadata(t *testing.T) {
	result, err := readLineRange(bufio.NewReader(strings.NewReader("one\ntwo\nthree\nfour\nfive\n")), 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "two\nthree\n" || result.StartLine != 2 || result.EndLine != 3 || result.ReturnedLines != 2 {
		t.Fatalf("unexpected range result: %#v", result)
	}
	if !result.HasMore || result.NextStartLine != 4 {
		t.Fatalf("missing continuation metadata: %#v", result)
	}
}

func TestReadLineRangeHandlesFinalLineWithoutNewline(t *testing.T) {
	result, err := readLineRange(bufio.NewReader(strings.NewReader("one\ntwo\nthree")), 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "two\nthree" || result.EndLine != 3 || result.ReturnedLines != 2 || result.HasMore {
		t.Fatalf("unexpected final range result: %#v", result)
	}
}
