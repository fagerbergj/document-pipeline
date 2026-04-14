package filesystem_test

import (
	"testing"

	"github.com/fagerbergj/document-pipeline/server/store/filesystem"
)

func TestSaveRead(t *testing.T) {
	vault := t.TempDir()
	s := filesystem.New()

	data := []byte("hello artifact")
	if err := s.Save(vault, "art-1", "page.txt", data); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(vault, "art-1", "page.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(data) {
		t.Fatalf("got %q, want %q", got, data)
	}
}

func TestRead_NotFound(t *testing.T) {
	s := filesystem.New()
	_, err := s.Read(t.TempDir(), "missing", "nope.txt")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestSave_CreatesNestedDirs(t *testing.T) {
	vault := t.TempDir()
	s := filesystem.New()
	if err := s.Save(vault, "deep/nested", "file.bin", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Read(vault, "deep/nested", "file.bin")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("unexpected data: %v", got)
	}
}
