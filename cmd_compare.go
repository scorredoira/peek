package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The outcome of one shot in a comparison.
const (
	shotSame      = "same"
	shotDifferent = "different"
	shotOnlyIn1   = "only before"
	shotOnlyIn2   = "only after"
)

// shotComparison is one PNG of the two sets, compared.
type shotComparison struct {
	File    string
	Outcome string
	// Changed pixels and the box around them, when different.
	Changed int
	Total   int
	Box     image.Rectangle
	// The diff and side-by-side images, relative to the report's directory.
	Diff string
	Side string
}

// The colour a changed pixel is marked with, and the gap between the side-by-side panels.
var changedColor = color.RGBA{R: 255, G: 0, B: 160, A: 255}

const sideGap = 16

func cmdCompare(args []string) error {
	if len(args) != 3 {
		return fmt.Errorf("usage: peek compare <before> <after> <out>")
	}
	before, after, out := args[0], args[1], args[2]
	if err := os.MkdirAll(out, 0755); err != nil {
		return fmt.Errorf("create %s: %w", out, err)
	}

	results, err := compareShotSets(before, after, out)
	if err != nil {
		return err
	}

	beforeManifest, err := readShotManifest(before)
	if err != nil {
		return err
	}
	afterManifest, err := readShotManifest(after)
	if err != nil {
		return err
	}

	reportText := writeCompareReport(before, after, beforeManifest, afterManifest, results)
	reportPath := filepath.Join(out, "report.md")
	if err := writeFile(reportPath, []byte(reportText)); err != nil {
		return err
	}

	differ := 0
	for _, r := range results {
		if r.Outcome != shotSame {
			differ++
			fmt.Printf("%-11s %s\n", r.Outcome, r.File)
		}
	}
	failedBefore := failedShots(beforeManifest)
	failedAfter := failedShots(afterManifest)
	fmt.Printf("%d shots compared, %d differ; failed: %d before, %d after → %s\n",
		len(results), differ, len(failedBefore), len(failedAfter), reportPath)

	if differ > 0 || len(failedBefore) > 0 || len(failedAfter) > 0 {
		return fmt.Errorf("the sets differ (%s)", reportPath)
	}
	return nil
}

// compareShotSets compares every PNG of two sets, and writes a diff and a side by side into
// out for each one that changed.
func compareShotSets(before, after, out string) ([]shotComparison, error) {
	beforeFiles, err := listShotFiles(before)
	if err != nil {
		return nil, err
	}
	afterFiles, err := listShotFiles(after)
	if err != nil {
		return nil, err
	}
	if len(beforeFiles) == 0 && len(afterFiles) == 0 {
		return nil, fmt.Errorf("no PNG in %s or %s", before, after)
	}

	all := map[string]bool{}
	for f := range beforeFiles {
		all[f] = true
	}
	for f := range afterFiles {
		all[f] = true
	}
	var files []string
	for f := range all {
		files = append(files, f)
	}
	sort.Strings(files)

	var results []shotComparison
	for _, f := range files {
		if !afterFiles[f] {
			results = append(results, shotComparison{File: f, Outcome: shotOnlyIn1})
			continue
		}
		if !beforeFiles[f] {
			results = append(results, shotComparison{File: f, Outcome: shotOnlyIn2})
			continue
		}

		beforePath := filepath.Join(before, f)
		afterPath := filepath.Join(after, f)
		r, err := comparePNGs(beforePath, afterPath, out, f)
		if err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func listShotFiles(dir string) (map[string]bool, error) {
	files := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".png") {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read the set %s: %w", dir, err)
	}
	return files, nil
}

func comparePNGs(beforePath, afterPath, out, file string) (shotComparison, error) {
	result := shotComparison{File: file}

	// The same code renders to the same bytes: most shots are settled without decoding.
	beforeBytes, err := os.ReadFile(beforePath)
	if err != nil {
		return result, fmt.Errorf("read %s: %w", beforePath, err)
	}
	afterBytes, err := os.ReadFile(afterPath)
	if err != nil {
		return result, fmt.Errorf("read %s: %w", afterPath, err)
	}
	if bytes.Equal(beforeBytes, afterBytes) {
		result.Outcome = shotSame
		return result, nil
	}

	a, err := readPNG(beforePath)
	if err != nil {
		return result, err
	}
	b, err := readPNG(afterPath)
	if err != nil {
		return result, err
	}

	diff, changed, box := diffImages(a, b)
	result.Total = diff.Bounds().Dx() * diff.Bounds().Dy()
	if changed == 0 {
		result.Outcome = shotSame
		return result, nil
	}
	result.Outcome = shotDifferent
	result.Changed = changed
	result.Box = box

	base := strings.TrimSuffix(file, ".png")
	result.Diff = base + ".diff.png"
	result.Side = base + ".side.png"

	diffPath := filepath.Join(out, result.Diff)
	if err := writePNG(diffPath, diff); err != nil {
		return result, err
	}
	side := sideBySide(a, b, diff)
	sidePath := filepath.Join(out, result.Side)
	if err := writePNG(sidePath, side); err != nil {
		return result, err
	}
	return result, nil
}

// diffImages lays both images over the union of their sizes: a pixel that differs (or exists
// in one only) is marked in changedColor, and an unchanged one is the after image washed out,
// so the marks stand out and the screen still reads. It answers the image, how many pixels
// changed and the box around them.
func diffImages(a, b image.Image) (*image.RGBA, int, image.Rectangle) {
	ab := a.Bounds()
	bb := b.Bounds()
	width := max(ab.Dx(), bb.Dx())
	height := max(ab.Dy(), bb.Dy())
	diff := image.NewRGBA(image.Rect(0, 0, width, height))

	changed := 0
	box := image.Rectangle{}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			inA := x < ab.Dx() && y < ab.Dy()
			inB := x < bb.Dx() && y < bb.Dy()
			same := inA && inB
			var pa, pb color.RGBA
			if inA {
				pa = color.RGBAModel.Convert(a.At(ab.Min.X+x, ab.Min.Y+y)).(color.RGBA)
			}
			if inB {
				pb = color.RGBAModel.Convert(b.At(bb.Min.X+x, bb.Min.Y+y)).(color.RGBA)
			}
			if same && pa == pb {
				// Washed out: three quarters white.
				diff.SetRGBA(x, y, color.RGBA{
					R: uint8((int(pb.R) + 3*255) / 4),
					G: uint8((int(pb.G) + 3*255) / 4),
					B: uint8((int(pb.B) + 3*255) / 4),
					A: 255,
				})
				continue
			}
			diff.SetRGBA(x, y, changedColor)
			changed++
			pixel := image.Rect(x, y, x+1, y+1)
			box = box.Union(pixel)
		}
	}
	return diff, changed, box
}

// sideBySide puts before, after and the diff in one row, on a grey background.
func sideBySide(panels ...image.Image) *image.RGBA {
	width := sideGap
	height := 0
	for _, p := range panels {
		width += p.Bounds().Dx() + sideGap
		height = max(height, p.Bounds().Dy())
	}
	height += 2 * sideGap

	side := image.NewRGBA(image.Rect(0, 0, width, height))
	grey := image.NewUniform(color.RGBA{R: 136, G: 136, B: 136, A: 255})
	draw.Draw(side, side.Bounds(), grey, image.Point{}, draw.Src)

	x := sideGap
	for _, p := range panels {
		target := image.Rect(x, sideGap, x+p.Bounds().Dx(), sideGap+p.Bounds().Dy())
		draw.Draw(side, target, p, p.Bounds().Min, draw.Src)
		x += p.Bounds().Dx() + sideGap
	}
	return side
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}
	return img, nil
}

func writePNG(path string, img image.Image) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// readShotManifest reads a set's shots.json; a set without one (PNGs from elsewhere) has none.
func readShotManifest(dir string) (*shotManifest, error) {
	path := filepath.Join(dir, shotsManifestFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var m shotManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return &m, nil
}

func failedShots(m *shotManifest) []shotEntry {
	if m == nil {
		return nil
	}
	var failed []shotEntry
	for _, s := range m.Shots {
		if s.Failed {
			failed = append(failed, s)
		}
	}
	return failed
}

// writeCompareReport answers report.md: only what differs, and the shots that failed.
func writeCompareReport(before, after string, beforeManifest, afterManifest *shotManifest,
	results []shotComparison) string {
	var b strings.Builder

	same := 0
	var differ []shotComparison
	for _, r := range results {
		if r.Outcome == shotSame {
			same++
		} else {
			differ = append(differ, r)
		}
	}

	fmt.Fprintf(&b, "# Shots: %s → %s\n\n", before, after)
	fmt.Fprintf(&b, "%d shots: %d the same, %d differ.\n\n", len(results), same, len(differ))

	if beforeManifest != nil && afterManifest != nil && beforeManifest.Clock != afterManifest.Clock {
		fmt.Fprintf(&b, "The clocks differ (%s and %s): what depends on the date may differ "+
			"for that alone.\n\n", beforeManifest.Clock, afterManifest.Clock)
	}

	for _, set := range []struct {
		label    string
		manifest *shotManifest
	}{{"before", beforeManifest}, {"after", afterManifest}} {
		failed := failedShots(set.manifest)
		if len(failed) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## Failed %s\n\n", set.label)
		for _, s := range failed {
			fmt.Fprintf(&b, "- `%s` (%d) %s\n", s.Name, s.Width, s.URL)
			for _, e := range s.Errors {
				fmt.Fprintf(&b, "  - %s\n", e)
			}
		}
		b.WriteString("\n")
	}

	for _, r := range differ {
		fmt.Fprintf(&b, "## %s — %s\n\n", r.File, r.Outcome)
		if r.Outcome != shotDifferent {
			continue
		}
		percent := 100 * float64(r.Changed) / float64(r.Total)
		fmt.Fprintf(&b, "%d pixels changed (%.2f%%), between x %d–%d and y %d–%d. "+
			"Before, after, diff:\n\n", r.Changed, percent, r.Box.Min.X, r.Box.Max.X,
			r.Box.Min.Y, r.Box.Max.Y)
		fmt.Fprintf(&b, "![%s](%s)\n\n", r.File, r.Side)
	}
	return b.String()
}
