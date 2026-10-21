package main

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func solidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func writeTestPNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	if err := writePNG(path, img); err != nil {
		t.Fatal(err)
	}
}

func TestDiffImagesMarksOnlyTheChangedPixels(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := solidImage(20, 10, white)
	b := solidImage(20, 10, white)
	b.SetRGBA(3, 4, color.RGBA{R: 254, G: 255, B: 255, A: 255})
	b.SetRGBA(7, 6, color.RGBA{A: 255})

	diff, changed, box := diffImages(a, b)
	if changed != 2 {
		t.Fatalf("changed = %d, want 2", changed)
	}
	if box != image.Rect(3, 4, 8, 7) {
		t.Fatalf("box = %v, want (3,4)-(8,7)", box)
	}
	if diff.RGBAAt(3, 4) != changedColor || diff.RGBAAt(7, 6) != changedColor {
		t.Fatal("a changed pixel is not marked")
	}
	if diff.RGBAAt(0, 0) == changedColor {
		t.Fatal("an unchanged pixel is marked")
	}
}

func TestDiffImagesCountsTheAreaOnlyOneHas(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	a := solidImage(10, 10, white)
	b := solidImage(10, 12, white)

	diff, changed, _ := diffImages(a, b)
	if diff.Bounds() != image.Rect(0, 0, 10, 12) {
		t.Fatalf("diff bounds = %v, want the union", diff.Bounds())
	}
	if changed != 20 {
		t.Fatalf("changed = %d, want the 20 pixels only the taller one has", changed)
	}
}

func TestCompareShotSets(t *testing.T) {
	root := t.TempDir()
	before := filepath.Join(root, "before")
	after := filepath.Join(root, "after")
	out := filepath.Join(root, "out")
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	red := color.RGBA{R: 255, A: 255}

	writeTestPNG(t, filepath.Join(before, "1440", "same.png"), solidImage(8, 8, white))
	writeTestPNG(t, filepath.Join(after, "1440", "same.png"), solidImage(8, 8, white))
	writeTestPNG(t, filepath.Join(before, "1440", "changed.png"), solidImage(8, 8, white))
	writeTestPNG(t, filepath.Join(after, "1440", "changed.png"), solidImage(8, 8, red))
	writeTestPNG(t, filepath.Join(before, "1440", "gone.png"), solidImage(8, 8, white))
	writeTestPNG(t, filepath.Join(after, "390", "new.png"), solidImage(8, 8, white))

	results, err := compareShotSets(before, after, out)
	if err != nil {
		t.Fatal(err)
	}
	outcomes := map[string]string{}
	for _, r := range results {
		outcomes[r.File] = r.Outcome
	}
	want := map[string]string{
		"1440/same.png":    shotSame,
		"1440/changed.png": shotDifferent,
		"1440/gone.png":    shotOnlyIn1,
		"390/new.png":      shotOnlyIn2,
	}
	if len(outcomes) != len(want) {
		t.Fatalf("outcomes = %v, want %v", outcomes, want)
	}
	for file, outcome := range want {
		if outcomes[file] != outcome {
			t.Errorf("%s: %q, want %q", file, outcomes[file], outcome)
		}
	}

	for _, f := range []string{"1440/changed.diff.png", "1440/changed.side.png"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("%s was not written: %v", f, err)
		}
	}
	if _, err := os.Stat(filepath.Join(out, "1440", "same.diff.png")); err == nil {
		t.Error("a diff was written for a shot that did not change")
	}

	report := writeCompareReport(before, after, nil, nil, results)
	if !strings.Contains(report, "1440/changed.png — different") ||
		!strings.Contains(report, "1440/gone.png — only before") ||
		!strings.Contains(report, "390/new.png — only after") {
		t.Errorf("the report misses a difference:\n%s", report)
	}
	if strings.Contains(report, "same.png") {
		t.Errorf("the report lists a shot that did not change:\n%s", report)
	}
}

func TestCompareReportListsFailedShots(t *testing.T) {
	after := &shotManifest{Shots: []shotEntry{
		{Name: "billing.tax", Width: 1440, URL: "http://x/admin/billing/tax", Failed: true,
			Errors: []string{"the server answered 500"}},
		{Name: "billing.document", Width: 1440},
	}}

	report := writeCompareReport("a", "b", nil, after, nil)
	if !strings.Contains(report, "## Failed after") ||
		!strings.Contains(report, "the server answered 500") {
		t.Errorf("the report hides a failed shot:\n%s", report)
	}
	if strings.Contains(report, "billing.document") {
		t.Errorf("the report lists a shot that did not fail:\n%s", report)
	}
}

func TestShotName(t *testing.T) {
	cases := map[string]string{
		"/billing/document": "billing.document",
		"/user/1":           "user.1",
		"/settings":         "settings",
		"/":                 "home",
		"/cron/run?state=x": "cron.run_state_x",
	}
	for path, want := range cases {
		got := shotName(path)
		if got != want {
			t.Errorf("shotName(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestCollectPathsWalksEveryShapeOnce(t *testing.T) {
	entry := navEntry{
		Path: "/settings",
		Items: []navEntry{
			{Path: "/billing/document"},
			{Path: "/site", Website: true},
		},
		Groups: []navGroup{{Items: []navEntry{{Path: "/billing/tax"}}}},
	}
	entry.Columns = append(entry.Columns, struct {
		Items  []navEntry `json:"items"`
		Groups []navGroup `json:"groups"`
	}{Items: []navEntry{{Path: "/billing/document"}, {Path: "/cron"}}})

	paths := entry.collectPaths(nil, map[string]bool{})
	got := strings.Join(paths, " ")
	want := "/settings /billing/document /billing/tax /cron"
	if got != want {
		t.Fatalf("paths = %q, want %q", got, want)
	}
}

func TestParseShotSizes(t *testing.T) {
	sizes, err := parseShotSizes("1440x900,390x844")
	if err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0] != (shotSize{1440, 900}) || sizes[1] != (shotSize{390, 844}) {
		t.Fatalf("sizes = %v", sizes)
	}
	for _, bad := range []string{"1440", "x900", "0x900", "1440x-1", ""} {
		if _, err := parseShotSizes(bad); err == nil {
			t.Errorf("parseShotSizes(%q) accepted a malformed size", bad)
		}
	}
}
