// Package vision finds a picture on a screenshot and reads text with Tesseract.
// Both run on the PC; the phone only sends the screenshot.
package vision

import (
	"errors"
	"image"
	"math"
	"sort"
)

// Match is where a template was found, in screenshot pixels.
type Match struct {
	Bounds [4]int  // left, top, right, bottom
	Score  float64 // normalised cross-correlation, 1 is a perfect match
}

func (m Match) Center() (int, int) {
	return (m.Bounds[0] + m.Bounds[2]) / 2, (m.Bounds[1] + m.Bounds[3]) / 2
}

var (
	ErrTooBig = errors.New("the image is larger than the screen")
	ErrFlat   = errors.New("the image is a single flat color, so there is nothing to match")
)

type gray struct {
	w, h int
	px   []float64
}

func toGray(img image.Image) gray {
	b := img.Bounds()
	g := gray{w: b.Dx(), h: b.Dy(), px: make([]float64, b.Dx()*b.Dy())}
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			g.px[y*g.w+x] = (0.299*float64(r) + 0.587*float64(gg) + 0.114*float64(bb)) / 257
		}
	}
	return g
}

// shrink averages f×f blocks.
func (g gray) shrink(f int) gray {
	if f == 1 {
		return g
	}
	out := gray{w: g.w / f, h: g.h / f}
	out.px = make([]float64, out.w*out.h)
	for y := 0; y < out.h; y++ {
		for x := 0; x < out.w; x++ {
			var s float64
			for dy := 0; dy < f; dy++ {
				row := (y*f + dy) * g.w
				for dx := 0; dx < f; dx++ {
					s += g.px[row+x*f+dx]
				}
			}
			out.px[y*out.w+x] = s / float64(f*f)
		}
	}
	return out
}

// integral holds running sums of pixels and squared pixels for window statistics.
type integral struct {
	w       int
	sum, sq []float64
}

func newIntegral(g gray) integral {
	w := g.w + 1
	in := integral{w: w, sum: make([]float64, w*(g.h+1)), sq: make([]float64, w*(g.h+1))}
	for y := 0; y < g.h; y++ {
		var rs, rq float64
		for x := 0; x < g.w; x++ {
			v := g.px[y*g.w+x]
			rs += v
			rq += v * v
			in.sum[(y+1)*w+x+1] = in.sum[y*w+x+1] + rs
			in.sq[(y+1)*w+x+1] = in.sq[y*w+x+1] + rq
		}
	}
	return in
}

func (in integral) window(x, y, w, h int) (sum, sq float64) {
	a, b, c, d := y*in.w+x, y*in.w+x+w, (y+h)*in.w+x, (y+h)*in.w+x+w
	return in.sum[d] - in.sum[b] - in.sum[c] + in.sum[a], in.sq[d] - in.sq[b] - in.sq[c] + in.sq[a]
}

type tmpl struct {
	g    gray
	zero []float64 // pixels minus their mean
	norm float64
}

func newTmpl(g gray) tmpl {
	var mean float64
	for _, v := range g.px {
		mean += v
	}
	mean /= float64(len(g.px))
	t := tmpl{g: g, zero: make([]float64, len(g.px))}
	for i, v := range g.px {
		t.zero[i] = v - mean
		t.norm += t.zero[i] * t.zero[i]
	}
	t.norm = math.Sqrt(t.norm)
	return t
}

// ncc scores the template at (x, y) of the screen.
func ncc(s gray, in integral, t tmpl, x, y int) float64 {
	n := float64(len(t.zero))
	sum, sq := in.window(x, y, t.g.w, t.g.h)
	v := sq - sum*sum/n
	if v <= 1e-9 || t.norm == 0 {
		return 0
	}
	var dot float64
	for ty := 0; ty < t.g.h; ty++ {
		srow := (y+ty)*s.w + x
		trow := ty * t.g.w
		for tx := 0; tx < t.g.w; tx++ {
			dot += t.zero[trow+tx] * s.px[srow+tx]
		}
	}
	return dot / (t.norm * math.Sqrt(v))
}

// Find looks for template on screen at the same scale. It searches a shrunken copy
// first, then checks the best spots at full size. The best match is returned even
// below threshold, with ok false.
func Find(screen, template image.Image, threshold float64) (m Match, ok bool, err error) {
	s, tg := toGray(screen), toGray(template)
	if tg.w > s.w || tg.h > s.h {
		return Match{}, false, ErrTooBig
	}
	full := newTmpl(tg)
	if full.norm < 1 {
		return Match{}, false, ErrFlat
	}
	f := max(1, min(4, min(tg.w, tg.h)/8))
	ss, ts := s.shrink(f), newTmpl(tg.shrink(f))
	sin := newIntegral(ss)

	type cand struct {
		x, y  int
		score float64
	}
	var cands []cand
	for y := 0; y+ts.g.h <= ss.h; y++ {
		for x := 0; x+ts.g.w <= ss.w; x++ {
			if sc := ncc(ss, sin, ts, x, y); sc > threshold-0.3 {
				cands = append(cands, cand{x, y, sc})
			}
		}
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if len(cands) > 8 {
		cands = cands[:8]
	}

	fin := newIntegral(s)
	best := Match{Score: -1}
	for _, c := range cands {
		for y := max(0, c.y*f-f); y <= min(s.h-tg.h, c.y*f+f); y++ {
			for x := max(0, c.x*f-f); x <= min(s.w-tg.w, c.x*f+f); x++ {
				if sc := ncc(s, fin, full, x, y); sc > best.Score {
					best = Match{Bounds: [4]int{x, y, x + tg.w, y + tg.h}, Score: sc}
				}
			}
		}
	}
	if best.Score < 0 {
		best.Score = 0
	}
	return best, best.Score >= threshold, nil
}
