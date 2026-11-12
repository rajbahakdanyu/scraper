package fileutil

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSourceMetadataRequiresMatchingIdentityAndContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "item.bin")
	content := []byte("complete file")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if err := WriteSource(path, "source-a", hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySource(path, "source-a", int64(len(content))); err != nil || !ok {
		t.Fatalf("matching source not verified: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifySource(path, "source-b", int64(len(content))); err != nil || ok {
		t.Fatalf("different source verified: ok=%v err=%v", ok, err)
	}
	if ok, err := VerifySource(path, "source-a", int64(len(content)+1)); err != nil || ok {
		t.Fatalf("wrong expected size verified: ok=%v err=%v", ok, err)
	}
	if err := os.WriteFile(path, []byte("corrupt file"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySource(path, "source-a", 0); err != nil || ok {
		t.Fatalf("corrupt content verified: ok=%v err=%v", ok, err)
	}
}

func TestSourceMetadataMissingIsUnverified(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.bin")
	if err := os.WriteFile(path, []byte("legacy"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := VerifySource(path, "source", 0); err != nil || ok {
		t.Fatalf("legacy file verified: ok=%v err=%v", ok, err)
	}
}
