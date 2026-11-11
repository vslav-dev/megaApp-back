package appendfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppendCreatesFileAndParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "log.ndjson")

	if err := Append(path, []byte("one\n")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "one\n" {
		t.Fatalf("file = %q, want %q", data, "one\n")
	}
}

func TestAppendAddsToExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log.ndjson")

	if err := Append(path, []byte("one\n")); err != nil {
		t.Fatalf("first Append() error = %v", err)
	}
	if err := Append(path, []byte("two\n")); err != nil {
		t.Fatalf("second Append() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != "one\ntwo\n" {
		t.Fatalf("file = %q, want %q", data, "one\ntwo\n")
	}
}

func TestAppendRejectsDirectoryAsPath(t *testing.T) {
	dir := t.TempDir()
	if err := Append(dir, []byte("x")); err == nil {
		t.Fatal("Append() error = nil, want error")
	}
}
