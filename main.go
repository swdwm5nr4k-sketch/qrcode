// Command qrcode moves text files through QR code images.
//
// Encoding gzips the input, splits the compressed stream into ordered chunks
// and writes one PNG per chunk; decoding reads one or more of those PNGs back,
// sorts them by their chunk index, reassembles the stream and unzips it.
//
// Usage:
//
//	qrcode encode [-o prefix] [-chunk bytes] [-level LMQH] [-scale px] input.txt
//	qrcode decode [-o output.txt] qr-01.png qr-02.png ...
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/swdwm5nr4k-sketch/qrcode/internal/qrgz"
	"github.com/swdwm5nr4k-sketch/qrcode/internal/qrpic"
)

// version is stamped at build time with -ldflags "-X main.version=...".
var version = "dev"

// defaultChunk is the number of compressed bytes put into one QR code when the
// user does not ask for a specific size. Chunks that large keep the number of
// images low while staying comfortably scannable.
const defaultChunk = 1500

var usage = `qrcode ` + version + ` — pack a text file into QR code images and unpack it again

Usage:
  qrcode encode [flags] <input.txt>          text file -> one or more QR PNGs
  qrcode decode [flags] <image.png> [...]    QR PNGs   -> text file
  qrcode help                                this message
  qrcode version                             print the version

The input file is gzip-compressed first and then cut into chunks in order, one
QR code per chunk. Long input therefore produces a numbered sequence of images
which decode accepts in any order. Use "-" as the input file to read stdin.

Run "qrcode encode -h" or "qrcode decode -h" for the flags of each command.
`

const encodeUsage = `Usage: qrcode encode [-o prefix] [-chunk bytes] [-level LMQH] [-scale px] <input.txt>

Reads a text file, gzips it and writes one QR code PNG per chunk.

Flags:
`

const decodeUsage = `Usage: qrcode decode [-o output.txt] <qr.png> [more.png ...]

Reads QR code images in any order, reassembles them and writes the original
text. Arguments may be files, globs (*.png) or directories. The text goes to
stdout unless -o names a file.

Flags:
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "qrcode: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return errors.New("missing command")
	}
	switch args[0] {
	case "encode", "enc":
		return runEncode(args[1:], stdout, stderr)
	case "decode", "dec":
		return runDecode(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	case "version", "-v", "--version":
		fmt.Fprintf(stdout, "qrcode %s\n", version)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try \"qrcode help\")", args[0])
	}
}

func runEncode(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("encode", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, encodeUsage)
		fs.PrintDefaults()
	}
	out := fs.String("o", "", "output prefix or directory (default: input file name without extension)")
	chunk := fs.Int("chunk", 0, fmt.Sprintf("compressed bytes per QR code (default: %d, capped by -level)", defaultChunk))
	levelName := fs.String("level", "M", "error-correction level: L, M, Q or H")
	scale := fs.Int("scale", 4, "pixels per QR code module")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("encode takes exactly one input file, got %d", fs.NArg())
	}
	input := fs.Arg(0)

	data, err := readInput(input)
	if err != nil {
		return err
	}
	level, err := qrpic.ParseLevel(*levelName)
	if err != nil {
		return err
	}
	if *scale < 1 {
		return fmt.Errorf("scale must be at least 1 pixel per module, got %d", *scale)
	}

	maxChunk := qrgz.MaxChunkSize(qrpic.Capacity(level))
	if maxChunk < 1 {
		return fmt.Errorf("level %s cannot hold a single chunk", qrpic.LevelName(level))
	}
	chunkSize := *chunk
	if chunkSize == 0 {
		chunkSize = defaultChunk
		if chunkSize > maxChunk {
			chunkSize = maxChunk
		}
	}
	if chunkSize > maxChunk {
		return fmt.Errorf("chunk size %d is too large for level %s: at most %d compressed bytes fit into one QR code",
			chunkSize, qrpic.LevelName(level), maxChunk)
	}
	if chunkSize < 1 {
		return fmt.Errorf("chunk size must be positive, got %d", chunkSize)
	}

	compressed, err := qrgz.Compress(data)
	if err != nil {
		return err
	}
	payloads, err := qrgz.Encode(compressed, chunkSize)
	if err != nil {
		return err
	}

	dir, base, err := outputBase(*out, input)
	if err != nil {
		return err
	}
	paths := outputPaths(dir, base, len(payloads))
	for i, payload := range payloads {
		png, err := qrpic.Render(payload, level, *scale)
		if err != nil {
			return fmt.Errorf("chunk %d/%d: %w", i+1, len(payloads), err)
		}
		if err := os.WriteFile(paths[i], png, 0o644); err != nil {
			return err
		}
	}

	fmt.Fprintf(stdout, "qrcode: %d bytes in, %d bytes gzipped, %d QR code(s) at level %s\n",
		len(data), len(compressed), len(payloads), qrpic.LevelName(level))
	if len(paths) <= 6 {
		for _, p := range paths {
			fmt.Fprintf(stdout, "  %s\n", p)
		}
		fmt.Fprintf(stdout, "qrcode: decode with \"qrcode decode -o restored.txt %s\"\n", strings.Join(paths, " "))
	} else {
		fmt.Fprintf(stdout, "  %s ... %s\n", paths[0], paths[len(paths)-1])
		fmt.Fprintf(stdout, "qrcode: decode with \"qrcode decode -o restored.txt %s\"\n",
			filepath.Join(dir, base+"-*.png"))
	}
	return nil
}

func runDecode(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("decode", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, decodeUsage)
		fs.PrintDefaults()
	}
	out := fs.String("o", "-", "file for the restored text, \"-\" writes to stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	images, err := expandInputs(fs.Args())
	if err != nil {
		return err
	}
	if len(images) == 0 {
		fs.Usage()
		return errors.New("decode needs at least one QR code image")
	}

	var frames []*qrgz.Frame
	for _, path := range images {
		payloads, err := qrpic.DecodeFile(path)
		if err != nil {
			return err
		}
		for _, payload := range payloads {
			frame, err := qrgz.Parse(payload)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			frames = append(frames, frame)
		}
	}

	compressed, err := qrgz.Reassemble(frames)
	if err != nil {
		return err
	}
	text, err := qrgz.Decompress(compressed)
	if err != nil {
		return err
	}

	target := "stdout"
	if *out == "-" {
		if _, err := stdout.Write(text); err != nil {
			return err
		}
	} else {
		if err := os.WriteFile(*out, text, 0o644); err != nil {
			return err
		}
		target = *out
	}
	fmt.Fprintf(stderr, "qrcode: restored %d bytes from %d QR code(s) in %d image(s) to %s\n",
		len(text), len(frames), len(images), target)
	return nil
}

// readInput reads path, or stdin when path is "-".
func readInput(path string) ([]byte, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		return data, nil
	}
	return os.ReadFile(path)
}

// outputBase works out which directory and file-name prefix to write to.
func outputBase(out, input string) (dir, base string, err error) {
	base = "qr"
	if input != "-" {
		if stem := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input)); stem != "" {
			base = stem
		}
	}
	if out == "" {
		return ".", base, nil
	}
	if info, statErr := os.Stat(out); statErr == nil && info.IsDir() {
		return out, base, nil
	}
	if strings.HasSuffix(out, string(os.PathSeparator)) {
		return strings.TrimRight(out, string(os.PathSeparator)), base, nil
	}
	dir = filepath.Dir(out)
	if b := filepath.Base(out); b != "." && b != string(os.PathSeparator) && b != "" {
		base = b
	}
	return dir, base, nil
}

// outputPaths names the images: a single chunk keeps the plain prefix, longer
// sequences are numbered with a fixed width so that a plain lexical sort of the
// file names is the correct chunk order.
func outputPaths(dir, base string, total int) []string {
	if total <= 1 {
		return []string{filepath.Join(dir, base+".png")}
	}
	width := len(strconv.Itoa(total))
	paths := make([]string, total)
	for i := range paths {
		paths[i] = filepath.Join(dir, fmt.Sprintf("%s-%0*d.png", base, width, i+1))
	}
	return paths
}

// expandInputs turns the command line arguments into a list of image files:
// directories are scanned for images, glob patterns are expanded, and the
// result is sorted so that a run is reproducible.
func expandInputs(args []string) ([]string, error) {
	var files []string
	for _, arg := range args {
		if strings.ContainsAny(arg, "*?[") {
			matches, err := filepath.Glob(arg)
			if err != nil {
				return nil, err
			}
			if len(matches) == 0 {
				return nil, fmt.Errorf("no files match %q", arg)
			}
			files = append(files, matches...)
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			files = append(files, arg)
			continue
		}
		entries, err := os.ReadDir(arg)
		if err != nil {
			return nil, err
		}
		found := 0
		for _, entry := range entries {
			if entry.IsDir() || !isImageName(entry.Name()) {
				continue
			}
			files = append(files, filepath.Join(arg, entry.Name()))
			found++
		}
		if found == 0 {
			return nil, fmt.Errorf("%s: no QR code images found", arg)
		}
	}
	sort.Strings(files)
	out := files[:0]
	for i, v := range files {
		if i == 0 || v != files[i-1] {
			out = append(out, v)
		}
	}
	return out, nil
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif":
		return true
	}
	return false
}
