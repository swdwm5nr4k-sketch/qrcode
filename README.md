# qrcode

![ci](https://github.com/buenos-work/qrcode/actions/workflows/ci.yml/badge.svg)

Move a text file from one machine to another through QR code images: a small Go
command line tool that gzips the text, splits it into ordered chunks, draws one
PNG per chunk — and puts the text back together from those images.

* no network, no server, no cgo, no runtime dependencies
* long input becomes a numbered sequence of images (`qr-01.png`, `qr-02.png`, ...)
* images may be decoded in any order; the chunk index decides how they join
* every payload carries a magic header, so foreign QR codes are rejected with a
  clear message instead of producing garbage

## Install

```sh
go install github.com/buenos-work/qrcode@latest
```

Or build from a checkout:

```sh
git clone https://github.com/buenos-work/qrcode.git
cd qrcode
make build          # produces ./qrcode
# or simply: go build -o qrcode .
```

## Prebuilt binaries

Every [release](https://github.com/buenos-work/qrcode/releases) ships one archive
per target the Go toolchain supports — 49 GOOS/GOARCH pairs, from `linux/amd64` to
`wasip1/wasm` and `plan9/386`:

```text
qrcode_v1.0.0_linux_amd64.tar.gz
qrcode_v1.0.0_darwin_arm64.tar.gz
qrcode_v1.0.0_windows_amd64.zip
...
checksums.txt
```

Each archive holds the statically linked binary (no libc, no cgo), the README and
the licence. Verify a download and unpack it with:

```sh
sha256sum --check --ignore-missing checksums.txt
tar xzf qrcode_v1.0.0_linux_amd64.tar.gz
./qrcode version
```

Rebuild the whole matrix locally with `make dist VERSION=v1.0.0`; the archives land
in `dist/`.

## Usage

### Encode a text file into QR code images

```sh
qrcode encode notes.txt                 # writes notes.png, or notes-01.png ...
qrcode encode -o out/sheet notes.txt    # choose the prefix (or a directory)
qrcode encode -scale 8 -chunk 700 big.txt
cat notes.txt | qrcode encode -o notes -   # read from stdin
```

A short file fits into a single QR code and is written as `notes.png`.
As soon as the compressed data needs more than one chunk, the images are
numbered with a fixed width so that a plain lexical sort is the correct order:

```text
$ qrcode encode notes.txt
qrcode: 292000 bytes in, 199250 bytes gzipped, 133 QR code(s) at level M
  notes-001.png ... notes-133.png
qrcode: decode with "qrcode decode -o restored.txt notes-*.png"
```

Flags for `encode`:

| flag | default | meaning |
| --- | --- | --- |
| `-o` | input file name without extension | output prefix, or an existing/trailing-slash directory |
| `-chunk` | 1500 | compressed bytes per QR code |
| `-level` | M | error-correction level: `L`, `M`, `Q` or `H` |
| `-scale` | 4 | pixels per QR module (a quiet zone of 4 modules is added on top) |

Larger `-chunk` values mean fewer images but denser symbols; the
sizes that actually fit are checked before anything is written, and a chunk that
is too large for the chosen level is rejected with the maximum allowed value.

### Decode the images back into text

```sh
qrcode decode notes-*.png -o notes.txt   # a glob, a list of files or a directory
qrcode decode shots/notes-*.png > notes.txt
qrcode decode notes-001.png notes-002.png      # prints to stdout by default
```

Flags for `decode`:

| flag | default | meaning |
| --- | --- | --- |
| `-o` | `-` (stdout) | where the restored text is written |

Arguments may be individual images, glob patterns or directories, and they may be
given in any order or mixed from different folders. PNG, JPEG and GIF are
accepted. Missing chunks, conflicting duplicates and images that do not belong
to the same sequence are reported as errors, and nothing is written in that
case.

## How it works

```text
input text ──gzip(BestCompression)──> compressed stream
   │
   ├─ cut into chunks of -chunk bytes (in order)
   ├─ each chunk becomes one payload:  GZQR1:<index>/<total>:<base64url>
   └─ each payload becomes one PNG QR code (byte mode, quiet zone of 4 modules)
```

The payload is plain ASCII on purpose: base64url keeps the data free of any
character that a QR reader, a shell, a file name or a charset heuristic could
mangle, so a payload survives any reader that follows the specification. The
`GZQR1:` tag carries the format version, and `<index>/<total>` makes
reassembly independent of the order in which the images are handed to
`decode`. Base64 costs a third of the capacity of a QR code, which is
why a chunk holds 1500 compressed bytes rather than ~2300.

Decoding tries two readers, in this order:

1. **Exact grid sampling.** The images this tool writes are pixel aligned, and
   the dark pixels of a QR symbol always reach the symbol's own edges (the three
   finder patterns), so their bounding box *is* the symbol box. The module grid
   is then sampled directly at every legal symbol size and scored against the
   fixed structure of a QR code (finder and timing patterns) before decoding.
   This is fast and deterministic, and it also copes with resized or re-encoded
   images. It exists because the general-purpose detector occasionally
   mis-estimates the module size of dense symbols and gives up on codes that are
   perfectly valid — verified against an independent decoder.
2. **General-purpose detection** (`gozxing`), which handles rotation,
   perspective and photographs of a screen or a printout.

## Limits

| limit | value |
| --- | --- |
| chunks per sequence | 99999 |
| decompressed output | 1 GiB |
| payload per QR code | 2331 bytes at level M (see the capacity table in `internal/qrpic`) |

## Tips

* The images this tool writes are meant to be read by this tool, and they are
  also ordinary QR codes: any scanner app can read a single one and show you the
  base64 payload.
* Codes intended to be photographed from a screen or printed and scanned by a
  phone deserve bigger modules and smaller chunks:
  `qrcode encode -scale 8 -chunk 700 -level Q notes.txt`.
* Text compresses well: a 292 KB text file became 133 images, and a 60 KB one
  about 27. Random binary data does not compress at all, so it produces roughly
  one image per ~1.5 KB.

## Development

```sh
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt -l -w .
```

The test suite covers the payload framing, gzip handling, PNG rendering, the
grid sampler (including resized images) and full command line round trips.

## 中文速览

`qrcode` 是一个用 Go 写的命令行工具，用来把文本文件"搬运"到另一台机器：

* **编码**：`qrcode encode notes.txt` 先用 gzip 压缩文本，再按固定大小
  切成有序分片，每片生成一张二维码图片。内容较短只生成 `notes.png`；
  较长则按顺序输出 `notes-001.png`、`notes-002.png` …
* **解码**：`qrcode decode notes-*.png -o notes.txt` 接受一张或一组二维码
  图片（顺序任意，也支持目录、JPEG/GIF），按分片编号拼回压缩流并解压还原原始文本。
* 每张二维码里是 ASCII 文本 `GZQR1:<序号>/<总数>:<base64url>`，因此任何扫码
  工具都能读出内容，而缺失分片、重复冲突、混入别的二维码都会被明确报错。
* 默认每个二维码装 1500 字节压缩数据（纠错等级 M），可用 `-chunk`、`-level`、
  `-scale` 调整；要用手机拍摄屏幕上的二维码，建议
  `-scale 8 -chunk 700 -level Q`。

## License

MIT — see [LICENSE](LICENSE).
