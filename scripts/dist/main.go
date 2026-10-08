// Command dist builds the release archives for this repository.
//
// It asks the Go toolchain for every GOOS/GOARCH pair it knows about, builds
// the qrcode binary for each one and packs the binary together with the README
// and the licence into a single archive per target, plus a checksum file.
//
// Targets the toolchain cannot build without cgo (iOS and Android, for example)
// are reported and skipped rather than silently dropped.
//
// Usage:
//
//	go run ./scripts/dist -version v1.0.0 [-out dist] [-keep-binaries]
package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type target struct {
	goos   string
	goarch string
}

func (t target) String() string { return t.goos + "/" + t.goarch }

type archiveFile struct {
	name string // name inside the archive
	path string // path on disk
	mode int64
}

type skipped struct {
	target target
	reason string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dist:", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "version stamped into the binaries and used in the archive names, e.g. v1.0.0")
	outDir := flag.String("out", "dist", "directory the archives are written to")
	only := flag.String("targets", "", "optional comma separated GOOS/GOARCH list, e.g. linux/amd64,darwin/arm64")
	keep := flag.Bool("keep-binaries", false, "keep the unpacked binaries in <out>/bin")
	flag.Parse()

	if strings.TrimSpace(*version) == "" {
		return errors.New("-version is required")
	}

	targets, err := listTargets()
	if err != nil {
		return err
	}
	if *only != "" {
		targets, err = selectTargets(targets, *only)
		if err != nil {
			return err
		}
	}
	if dir := filepath.Join(*outDir, "bin"); !*keep {
		os.RemoveAll(dir)
	} else if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return err
	}

	started := time.Now()
	var archives []string
	var failures []skipped
	for _, t := range targets {
		binary, err := build(t, filepath.Join(*outDir, "bin"), *version)
		if err != nil {
			failures = append(failures, skipped{t, err.Error()})
			fmt.Printf("skip %-16s %s\n", t, err)
			continue
		}
		archive, err := pack(t, binary, *version, *outDir)
		if err != nil {
			return err
		}
		if !*keep {
			os.Remove(binary)
		}
		archives = append(archives, archive)
		info, _ := os.Stat(archive)
		fmt.Printf("built %-16s %s (%s)\n", t, filepath.Base(archive), humanSize(info.Size()))
	}

	checksums, err := writeChecksums(*outDir, archives)
	if err != nil {
		return err
	}
	total := int64(0)
	for _, a := range archives {
		if info, err := os.Stat(a); err == nil {
			total += info.Size()
		}
	}
	fmt.Printf("\n%d archives, %s total, checksums in %s, took %s\n",
		len(archives), humanSize(total), filepath.Base(checksums), time.Since(started).Round(time.Second))
	if len(failures) > 0 {
		fmt.Printf("%d target(s) skipped:\n", len(failures))
		for _, f := range failures {
			fmt.Printf("  %-16s %s\n", f.target, f.reason)
		}
	}
	return nil
}

// listTargets returns every GOOS/GOARCH pair the toolchain reports, sorted.
func listTargets() ([]target, error) {
	out, err := exec.Command("go", "tool", "dist", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("go tool dist list: %w", err)
	}
	var targets []target
	for _, line := range strings.Fields(string(out)) {
		parts := strings.SplitN(line, "/", 2)
		if len(parts) != 2 {
			continue
		}
		targets = append(targets, target{goos: parts[0], goarch: parts[1]})
	}
	if len(targets) == 0 {
		return nil, errors.New("the toolchain reported no build targets")
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].String() < targets[j].String() })
	return targets, nil
}

func selectTargets(all []target, list string) ([]target, error) {
	wanted := map[string]bool{}
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			wanted[item] = true
		}
	}
	var chosen []target
	for _, t := range all {
		if wanted[t.String()] {
			chosen = append(chosen, t)
			delete(wanted, t.String())
		}
	}
	if len(wanted) > 0 {
		var unknown []string
		for name := range wanted {
			unknown = append(unknown, name)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown target(s): %s", strings.Join(unknown, ", "))
	}
	return chosen, nil
}

// build cross-compiles the command for one target and returns the binary path.
func build(t target, binDir, version string) (string, error) {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(binDir, fmt.Sprintf("qrcode_%s_%s%s", t.goos, t.goarch, binaryExt(t)))
	cmd := exec.Command("go", "build",
		"-trimpath",
		"-ldflags", "-s -w -X main.version="+version,
		"-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+t.goos, "GOARCH="+t.goarch, "GOFLAGS=")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		reason := firstLine(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		return "", errors.New(reason)
	}
	return out, nil
}

// pack writes the release archive for one target.
func pack(t target, binary, version, outDir string) (string, error) {
	// The binary is named after the command inside the archive, while the copy
	// on disk carries the target so that parallel builds cannot clash.
	files := []archiveFile{
		{name: "qrcode" + binaryExt(t), path: binary, mode: 0o755},
		{name: "README.md", path: "README.md", mode: 0o644},
		{name: "LICENSE", path: "LICENSE", mode: 0o644},
	}
	name := fmt.Sprintf("qrcode_%s_%s_%s", version, t.goos, t.goarch)
	path := filepath.Join(outDir, name+".tar.gz")
	if t.goos == "windows" {
		path = filepath.Join(outDir, name+".zip")
		return path, writeZip(path, files)
	}
	return path, writeTarGz(path, files)
}

func writeTarGz(path string, files []archiveFile) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    f.name,
			Mode:    f.mode,
			Size:    info.Size(),
			ModTime: time.Now(),
		}); err != nil {
			return err
		}
		src, err := os.Open(f.path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func writeZip(path string, files []archiveFile) error {
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	for _, f := range files {
		info, err := os.Stat(f.path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = f.name
		header.Method = zip.Deflate
		header.SetMode(os.FileMode(f.mode))
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		src, err := os.Open(f.path)
		if err != nil {
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// writeChecksums writes sha256 sums of every archive, in the format used by
// sha256sum -c.
func writeChecksums(outDir string, archives []string) (string, error) {
	sort.Strings(archives)
	path := filepath.Join(outDir, "checksums.txt")
	var b strings.Builder
	for _, archive := range archives {
		sum, err := sha256File(archive)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, filepath.Base(archive))
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func binaryExt(t target) string {
	switch t.goos {
	case "windows":
		return ".exe"
	case "js", "wasip1":
		return ".wasm"
	}
	return ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	for _, suffix := range []string{"KiB", "MiB", "GiB"} {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, suffix)
		}
	}
	return fmt.Sprintf("%.1f TiB", value/unit)
}
