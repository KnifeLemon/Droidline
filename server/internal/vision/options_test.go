package vision

import (
	"image"
	"image/color"
	"testing"
)

// coin draws a 40x40 ring of the given color with a dark dot in the middle onto img at (x, y).
func coin(img *image.RGBA, x, y int, c color.RGBA) {
	for dy := 0; dy < 40; dy++ {
		for dx := 0; dx < 40; dx++ {
			d := (dx-20)*(dx-20) + (dy-20)*(dy-20)
			switch {
			case d < 36:
				img.Set(x+dx, y+dy, color.RGBA{20, 20, 20, 255})
			case d < 400:
				img.Set(x+dx, y+dy, c)
			}
		}
	}
}

func board() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{240, 236, 228, 255})
		}
	}
	gold := color.RGBA{230, 180, 30, 255}
	coin(img, 30, 50, gold)
	coin(img, 200, 300, gold)
	coin(img, 320, 600, gold)
	coin(img, 100, 450, color.RGBA{110, 226, 110, 255}) // green with the same brightness as gold
	return img
}

func TestFindAllReturnsEveryMatch(t *testing.T) {
	b := board()
	tm := b.SubImage(image.Rect(30, 50, 70, 90))
	ms, err := FindAll(b, tm, 0.9, Options{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Brightness alone cannot tell the green coin from the gold ones.
	if len(ms) != 4 {
		t.Fatalf("got %d matches %+v", len(ms), ms)
	}
	ms, _ = FindAll(b, tm, 0.9, Options{Color: true}, 0)
	if len(ms) != 3 {
		t.Fatalf("with color, got %d matches %+v", len(ms), ms)
	}
	for _, m := range ms {
		if m.Bounds[0] == 100 && m.Bounds[1] == 450 {
			t.Fatal("the green coin matched with color on")
		}
	}
	if ms, _ := FindAll(b, tm, 0.9, Options{}, 2); len(ms) != 2 {
		t.Fatalf("limit 2 gave %d", len(ms))
	}
}

func TestRegionLimitsTheSearchAndKeepsScreenCoordinates(t *testing.T) {
	b := board()
	tm := b.SubImage(image.Rect(30, 50, 70, 90))
	m, ok, err := FindWith(b, tm, 0.9, Options{Region: []int{150, 250, 400, 700}})
	if err != nil || !ok {
		t.Fatalf("not found in region: %v %v", ok, err)
	}
	if m.Bounds[0] < 150 || m.Bounds[1] < 250 {
		t.Fatalf("match %+v is outside the region", m)
	}
	if _, ok, _ := FindWith(b, tm, 0.9, Options{Region: []int{0, 600, 150, 700}}); ok {
		t.Fatal("found a coin in an empty region")
	}
	if _, _, err := FindWith(b, tm, 0.9, Options{Region: []int{10, 20}}); err != ErrRegion {
		t.Fatalf("short region: %v", err)
	}
	if _, _, err := FindWith(b, tm, 0.9, Options{Region: []int{500, 800, 600, 900}}); err != ErrRegion {
		t.Fatalf("region off screen: %v", err)
	}
}

func TestScalesFindABiggerCopy(t *testing.T) {
	small := board()
	tm := small.SubImage(image.Rect(30, 50, 70, 90))

	big := image.NewRGBA(image.Rect(0, 0, 400, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 400; x++ {
			big.Set(x, y, color.RGBA{240, 236, 228, 255})
		}
	}
	// The same coin drawn at 1.5x, as on a phone with a denser screen.
	for dy := 0; dy < 60; dy++ {
		for dx := 0; dx < 60; dx++ {
			big.Set(150+dx, 200+dy, tm.At(30+dx*2/3, 50+dy*2/3))
		}
	}
	if _, ok, _ := FindWith(big, tm, 0.85, Options{}); ok {
		t.Fatal("matched a different size without scales")
	}
	m, ok, err := FindWith(big, tm, 0.85, Options{Scales: []float64{1, 1.25, 1.5}})
	if err != nil || !ok {
		t.Fatalf("not found with scales: %v %v %+v", ok, err, m)
	}
	if w := m.Bounds[2] - m.Bounds[0]; w != 60 {
		t.Fatalf("matched width %d, want 60", w)
	}
}
