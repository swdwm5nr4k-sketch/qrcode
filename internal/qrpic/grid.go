package qrpic

import (
	"image"
	"sort"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"github.com/makiuchi-d/gozxing/qrcode/decoder"
)

// The general-purpose detector in gozxing locates finder patterns, estimates
// the module size from them and then samples the grid. On clean, dense symbols
// (which is exactly what this tool produces) that estimate is occasionally a
// module or two off and the symbol is rejected. Because the images written by
// this tool are pixel aligned - every module is exactly scale pixels wide and
// the symbol is surrounded by an exact quiet zone - the grid can be recovered
// directly instead: the dark pixels of a QR symbol always reach the very edges
// of the symbol on all four sides (the three finder patterns), so their
// bounding box *is* the symbol box.
//
// decodeByGrid therefore tries every legal symbol size, samples the module
// centres, scores the result against the known structure of a QR code and
// decodes the best candidates. It is both faster and more reliable than the
// detector for images produced by this tool, and it still copes with images
// that have been resized or re-encoded.

// minGridScale is the smallest number of pixels per module we are willing to
// sample from. Below roughly one pixel per module the image simply does not
// carry the information any more.
const minGridScale = 1.0

// gridCandidate is one possible interpretation of the image as a QR symbol.
type gridCandidate struct {
	modules int
	score   float64
	grid    [][]bool
}

// decodeByGrid tries to read a QR code by sampling the module grid directly.
// It reports false when the image does not look like a clean QR symbol at all.
func decodeByGrid(img image.Image) (string, bool) {
	dark, width, height := binarize(img)
	x0, y0, x1, y1, ok := darkBounds(dark, width, height)
	if !ok {
		return "", false
	}
	boxW, boxH := x1-x0+1, y1-y0+1
	if boxW < 21 || boxH < 21 {
		return "", false
	}
	// A QR symbol is square; allow a little slack for images that were scaled
	// with slightly different factors in x and y.
	if boxW*10 < boxH*9 || boxH*10 < boxW*9 {
		return "", false
	}

	var candidates []gridCandidate
	for modules := 21; modules <= 177; modules += 4 {
		scaleX := float64(boxW) / float64(modules)
		scaleY := float64(boxH) / float64(modules)
		if scaleX < minGridScale || scaleY < minGridScale {
			break // larger symbols need even more pixels per module
		}
		grid := sampleGrid(dark, width, height, x0, y0, scaleX, scaleY, modules)
		score := gridScore(grid, modules)
		if score < 0.75 {
			continue
		}
		candidates = append(candidates, gridCandidate{modules: modules, score: score, grid: grid})
	}
	// Best structural match first; the true size wins by a wide margin.
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	const maxTries = 4
	for i, candidate := range candidates {
		if i >= maxTries {
			break
		}
		result, err := decoder.NewDecoder().DecodeBoolMapWithoutHint(candidate.grid)
		if err != nil {
			continue
		}
		return result.GetText(), true
	}
	return "", false
}

// binarize converts an image to a boolean "is this pixel dark" map using a
// global threshold halfway between the darkest and the lightest pixel.
func binarize(img image.Image) (dark [][]bool, width, height int) {
	bounds := img.Bounds()
	width, height = bounds.Dx(), bounds.Dy()
	luminance := make([]uint8, width*height)
	low, high := uint8(255), uint8(0)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r, g, b, _ := img.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			// Rec. 601 luma, scaled back down to 8 bits.
			lum := uint8(((r*299 + g*587 + b*114) / 1000) >> 8)
			luminance[y*width+x] = lum
			if lum < low {
				low = lum
			}
			if lum > high {
				high = lum
			}
		}
	}
	threshold := uint16(low) + uint16(high)
	dark = make([][]bool, height)
	for y := 0; y < height; y++ {
		row := make([]bool, width)
		for x := 0; x < width; x++ {
			row[x] = uint16(luminance[y*width+x])*2 < threshold
		}
		dark[y] = row
	}
	return dark, width, height
}

// darkBounds returns the bounding box of the dark pixels of the image.
func darkBounds(dark [][]bool, width, height int) (x0, y0, x1, y1 int, ok bool) {
	x0, y0, x1, y1 = width, height, -1, -1
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if !dark[y][x] {
				continue
			}
			if x < x0 {
				x0 = x
			}
			if x > x1 {
				x1 = x
			}
			if y < y0 {
				y0 = y
			}
			if y > y1 {
				y1 = y
			}
		}
	}
	return x0, y0, x1, y1, x1 >= 0 && y1 >= 0
}

// sampleGrid reads one pixel from the centre of every module.
func sampleGrid(dark [][]bool, width, height, x0, y0 int, scaleX, scaleY float64, modules int) [][]bool {
	grid := make([][]bool, modules)
	for row := 0; row < modules; row++ {
		line := make([]bool, modules)
		sy := y0 + int((float64(row)+0.5)*scaleY)
		if sy >= height {
			sy = height - 1
		}
		for col := 0; col < modules; col++ {
			sx := x0 + int((float64(col)+0.5)*scaleX)
			if sx >= width {
				sx = width - 1
			}
			line[col] = dark[sy][sx]
		}
		grid[row] = line
	}
	return grid
}

// gridScore rates how much a sampled grid looks like a QR symbol: the three
// finder patterns and the two timing patterns are fixed by the specification,
// so a wrong module size scores badly.
func gridScore(grid [][]bool, modules int) float64 {
	const finderModules = 49
	finderWeight, timingWeight := 3.0, 1.0

	matched, total := 0.0, 0.0
	for _, corner := range [][2]int{{0, 0}, {modules - 7, 0}, {0, modules - 7}} {
		matched += finderWeight * finderMatch(grid, corner[0], corner[1])
		total += finderWeight * finderModules
	}
	timingMatches, timingTotal := 0, 0
	for i := 8; i < modules-8; i++ {
		want := i%2 == 0
		if grid[6][i] == want {
			timingMatches++
		}
		if grid[i][6] == want {
			timingMatches++
		}
		timingTotal += 2
	}
	matched += timingWeight * float64(timingMatches)
	total += timingWeight * float64(timingTotal)
	if total == 0 {
		return 0
	}
	return matched / total
}

// finderMatch counts how many of the 49 modules of a finder pattern are right.
func finderMatch(grid [][]bool, ox, oy int) float64 {
	matches := 0
	for dy := 0; dy < 7; dy++ {
		for dx := 0; dx < 7; dx++ {
			want := dx == 0 || dx == 6 || dy == 0 || dy == 6 || (dx >= 2 && dx <= 4 && dy >= 2 && dy <= 4)
			if grid[oy+dy][ox+dx] == want {
				matches++
			}
		}
	}
	return float64(matches)
}

// decodeWithDetector runs the general-purpose detector, which copes with
// rotation, perspective and photos of a screen or a printout.
func decodeWithDetector(img image.Image) (string, error) {
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", err
	}
	hints := map[gozxing.DecodeHintType]interface{}{
		gozxing.DecodeHintType_TRY_HARDER: true,
	}
	result, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
	if err != nil {
		return "", err
	}
	return result.GetText(), nil
}
