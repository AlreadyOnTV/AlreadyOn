package playback

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// BenchmarkInputsDir is an immutable image path in containers and an ignored
// generated directory for native previews. It is a link-time path, not a setting.
var BenchmarkInputsDir = "../.private/benchmark-inputs"

type benchmarkInputs struct {
	files       map[string]string
	fingerprint string
}

func loadBenchmarkInputs(directory string) (*benchmarkInputs, error) {
	raw, err := os.ReadFile(filepath.Join(directory, "SHA256SUMS"))
	if err != nil {
		return nil, err
	}
	recipe, err := os.ReadFile(filepath.Join(directory, "recipe.sha256"))
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid benchmark manifest")
		}
		name := strings.TrimPrefix(fields[1], "./")
		if filepath.Base(name) != name || !strings.HasSuffix(name, ".mkv") {
			return nil, fmt.Errorf("invalid benchmark input name")
		}
		path, err := filepath.Abs(filepath.Join(directory, name))
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("benchmark input is not a regular file: %s", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		file.Close()
		if err != nil || hex.EncodeToString(hash.Sum(nil)) != fields[0] {
			return nil, fmt.Errorf("benchmark input checksum failed: %s", name)
		}
		files[name] = path
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	for _, s := range capacitySources {
		for _, fps := range []float64{s.fps} {
			if _, ok := files[inputName(s.class, fps)]; !ok {
				return nil, fmt.Errorf("missing benchmark input for %s", s.class)
			}
		}
	}
	for _, class := range []SourceClass{SourceHEVCPQ, SourceHEVCHLG} {
		for _, fps := range []float64{24, 30, 60} {
			if _, ok := files[inputName(class, fps)]; !ok {
				return nil, fmt.Errorf("missing HDR benchmark input")
			}
		}
	}
	hash := sha256.Sum256(append(raw, recipe...))
	return &benchmarkInputs{files: files, fingerprint: hex.EncodeToString(hash[:])}, nil
}
func inputName(class SourceClass, fps float64) string {
	name := string(class)
	if class == SourceHEVCPQ || class == SourceHEVCHLG {
		name += "-" + strconv.FormatFloat(fps, 'f', -1, 64)
	}
	return name + ".mkv"
}
func preparedInput(class SourceClass, fps float64) (string, bool) {
	path, err := filepath.Abs(filepath.Join(BenchmarkInputsDir, inputName(class, fps)))
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(path)
	return path, err == nil && info.Mode().IsRegular()
}

// Legacy full-matrix callers reuse the same prepared inputs. Their synthetic
// fallback remains useful for isolated regression tests without a built image.
func preparedCapacityInput(_ context.Context, source capacitySource) (string, bool) {
	return preparedInput(source.class, source.fps)
}
