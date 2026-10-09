package vision

import (
	"image"
	"image/color"
	"testing"
)

// screen draws a light background with two different marks, so only one place matches.
func screen() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 400, 700))
	for y := 0; y < 700; y++ {
		for x := 0; x < 400; x++ {
			img.Set(x, y, color.RGBA{240, 236, 228, 255})
		}
	}
	for y := 0; y < 60; y++ {
		for x := 0; x < 60; x++ {
			if (x/10+y/10)%2 == 0 {
				img.Set(250+x, 480+y, color.RGBA{30, 40, 60, 255})
			}
			if x > y {
				img.Set(40+x, 100+y, color.RGBA{255, 107, 33, 255})
			}
		}
	}
	return img
}

func TestFindLocatesTheTemplate(t *testing.T) {
	s := screen()
	tm := s.SubImage(image.Rect(250, 480, 310, 540))
	m, ok, err := Find(s, tm, 0.9)
	if err != nil || !ok {
		t.Fatalf("not found: %v %v %+v", ok, err, m)
	}
	if m.Bounds != [4]int{250, 480, 310, 540} || m.Score < 0.99 {
		t.Fatalf("match %+v", m)
	}
	if x, y := m.Center(); x != 280 || y != 510 {
		t.Fatalf("center %d,%d", x, y)
	}
}

func TestFindReportsAMissingTemplate(t *testing.T) {
	s := screen()
	other := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			other.Set(x, y, color.RGBA{uint8(x * 6), uint8(y * 6), 90, 255})
		}
	}
	if _, ok, err := Find(s, other, 0.9); err != nil || ok {
		t.Fatalf("matched something that is not there: %v %v", ok, err)
	}
	flat := image.NewRGBA(image.Rect(0, 0, 20, 20))
	if _, _, err := Find(s, flat, 0.9); err != ErrFlat {
		t.Fatalf("flat template: %v", err)
	}
	if _, _, err := Find(other, s, 0.9); err != ErrTooBig {
		t.Fatalf("big template: %v", err)
	}
}

const tsv = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
	"1\t1\t0\t0\t0\t0\t0\t0\t800\t1200\t-1\t\n" +
	"5\t1\t1\t1\t1\t1\t100\t160\t90\t40\t95.1\t네트워크\n" +
	"5\t1\t1\t1\t1\t2\t200\t160\t30\t40\t93.0\t및\n" +
	"5\t1\t1\t1\t1\t3\t240\t160\t70\t40\t91.5\t인터넷\n" +
	"5\t1\t2\t1\t1\t1\t100\t360\t120\t38\t88.0\tBluetooth\n"

func TestParseTSVGroupsLines(t *testing.T) {
	lines := ParseTSV(tsv)
	if len(lines) != 2 || lines[0].Text != "네트워크 및 인터넷" || lines[0].Bounds != [4]int{100, 160, 310, 200} {
		t.Fatalf("lines %+v", lines)
	}
	if w, ok := FindText(lines, "Bluetooth"); !ok || w.Bounds != [4]int{100, 360, 220, 398} {
		t.Fatalf("word %+v %v", w, ok)
	}
	if l, ok := FindText(lines, "및 인터"); !ok || l.Text != "네트워크 및 인터넷" {
		t.Fatalf("line %+v %v", l, ok)
	}
	if _, ok := FindText(lines, "없음"); ok {
		t.Fatal("found text that is not there")
	}
}
