package playback

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreparedInputsRejectCorruptionAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	names := map[string]bool{}
	for _, source := range capacitySources {
		names[inputName(source.class, source.fps)] = true
	}
	for _, class := range []SourceClass{SourceHEVCPQ, SourceHEVCHLG} {
		for _, fps := range []float64{24, 30, 60} {
			names[inputName(class, fps)] = true
		}
	}
	raw := []byte("synthetic fixture bytes")
	manifest := strings.Builder{}
	for name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&manifest, "%x  ./%s\n", sha256.Sum256(raw), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(manifest.String()), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "recipe.sha256"), []byte("recipe"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBenchmarkInputs(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, inputName(SourceHEVCPQ, 60))
	if err := os.WriteFile(path, []byte("corruption"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBenchmarkInputs(dir); err == nil {
		t.Fatal("corrupt fixture accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBenchmarkInputs(dir); err == nil {
		t.Fatal("linked fixture accepted")
	}
}
