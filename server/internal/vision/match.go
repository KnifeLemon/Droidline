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

// Options narrow or widen a search. The zero value searches the whole screen, at the size the template was cut,
// comparing brightness only.
type Options struct {
	Region []int     // left, top, right, bottom in screenshot pixels; empty means the whole screen
	Scales []float64 // template sizes to try, 1 being the size it was cut at; empty means just 1
	Color  bool      // also require the average color to agree, so a red and a green button differ
}

var ErrRegion = errors.New("region must be four numbers, left, top, right and bottom, inside the screen")

// Find looks for template on screen at the same scale. The best match is returned even below threshold, with ok
// false.
func Find(screen, template image.Image, threshold float64) (m Match, ok bool, err error) {
	return FindWith(screen, template, threshold, Options{})
}

// FindWith is Find with a region, other sizes of the template, or a color check.
func FindWith(screen, template image.Image, threshold float64, o Options) (m Match, ok bool, err error) {
	all, err := search(screen, template, threshold, o, false)
	if err != nil {
		return Match{}, false, err
	}
	best := Match{}
	for _, c := range all {
		if c.Score > best.Score {
			best = c
		}
	}
	return best, best.Score >= threshold, nil
}

// FindAll returns every place the template matches at or above threshold, best first, without overlaps, at most
// limit of them.
func FindAll(screen, template image.Image, threshold float64, o Options, limit int) ([]Match, error) {
	all, err := search(screen, template, threshold, o, true)
	if err != nil {
		return nil, err
	}
	var hits []Match
	for _, c := range all {
		if c.Score >= threshold {
			hits = append(hits, c)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	var out []Match
	for _, h := range hits {
		keep := true
		for _, k := range out {
			if overlap(h.Bounds, k.Bounds) > 0.3 {
				keep = false
				break
			}
		}
		if keep {
			out = append(out, h)
			if limit > 0 && len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

// overlap is the area two boxes share divided by the area they cover together.
func overlap(a, b [4]int) float64 {
	w := min(a[2], b[2]) - max(a[0], b[0])
	h := min(a[3], b[3]) - max(a[1], b[1])
	if w <= 0 || h <= 0 {
		return 0
	}
	inter := float64(w * h)
	area := func(r [4]int) float64 { return float64((r[2] - r[0]) * (r[3] - r[1])) }
	return inter / (area(a) + area(b) - inter)
}

type subImager interface {
	SubImage(r image.Rectangle) image.Image
}

// crop cuts the region out of the screen and returns it with its top-left corner in screen pixels.
func crop(screen image.Image, region []int) (image.Image, image.Point, error) {
	b := screen.Bounds()
	if len(region) == 0 {
		return screen, b.Min, nil
	}
	if len(region) != 4 {
		return nil, image.Point{}, ErrRegion
	}
	r := image.Rect(region[0], region[1], region[2], region[3]).Add(b.Min).Intersect(b)
	if r.Empty() {
		return nil, image.Point{}, ErrRegion
	}
	if si, ok := screen.(subImager); ok {
		return si.SubImage(r), r.Min, nil
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			out.Set(x, y, screen.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return out, r.Min, nil
}

// resize scales a gray image by f with bilinear sampling.
func (g gray) resize(f float64) gray {
	w, h := int(math.Round(float64(g.w)*f)), int(math.Round(float64(g.h)*f))
	out := gray{w: w, h: h, px: make([]float64, max(0, w*h))}
	for y := 0; y < h; y++ {
		sy := math.Max(0, math.Min(float64(g.h-1), (float64(y)+0.5)/f-0.5))
		y0 := int(sy)
		y1, fy := min(y0+1, g.h-1), sy-float64(y0)
		for x := 0; x < w; x++ {
			sx := math.Max(0, math.Min(float64(g.w-1), (float64(x)+0.5)/f-0.5))
			x0 := int(sx)
			x1, fx := min(x0+1, g.w-1), sx-float64(x0)
			top := g.px[y0*g.w+x0]*(1-fx) + g.px[y0*g.w+x1]*fx
			bot := g.px[y1*g.w+x0]*(1-fx) + g.px[y1*g.w+x1]*fx
			out.px[y*w+x] = top*(1-fy) + bot*fy
		}
	}
	return out
}

// meanRGB is the average color of r in img, each channel 0 to 255.
func meanRGB(img image.Image, r image.Rectangle) [3]float64 {
	var s [3]float64
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			s[0] += float64(cr) / 257
			s[1] += float64(cg) / 257
			s[2] += float64(cb) / 257
			n++
		}
	}
	if n > 0 {
		for i := range s {
			s[i] /= float64(n)
		}
	}
	return s
}

// search returns refined candidate matches in screen pixels, scored and color-checked, not yet filtered by
// threshold. With all false it keeps only the few best spots per size.
func search(screen, template image.Image, threshold float64, o Options, all bool) ([]Match, error) {
	area, origin, err := crop(screen, o.Region)
	if err != nil {
		return nil, err
	}
	s, tg0 := toGray(area), toGray(template)
	if newTmpl(tg0).norm < 1 {
		return nil, ErrFlat
	}
	scales := o.Scales
	if len(scales) == 0 {
		scales = []float64{1}
	}
	var want [3]float64
	if o.Color {
		want = meanRGB(template, template.Bounds())
	}
	fin := newIntegral(s)
	shrunk := map[int]struct {
		g  gray
		in integral
	}{}
	tried := false
	var out []Match
	for _, sc := range scales {
		if sc <= 0 {
			continue
		}
		tg := tg0
		if sc != 1 {
			tg = tg0.resize(sc)
		}
		if tg.w < 3 || tg.h < 3 || tg.w > s.w || tg.h > s.h {
			continue
		}
		full := newTmpl(tg)
		if full.norm < 1 {
			continue
		}
		tried = true
		f := max(1, min(4, min(tg.w, tg.h)/8))
		sh, ok := shrunk[f]
		if !ok {
			g := s.shrink(f)
			sh.g, sh.in = g, newIntegral(g)
			shrunk[f] = sh
		}
		ts := newTmpl(tg.shrink(f))
		if ts.g.w == 0 || ts.g.h == 0 || ts.norm == 0 {
			continue
		}

		type cand struct {
			x, y  int
			score float64
		}
		var cands []cand
		for y := 0; y+ts.g.h <= sh.g.h; y++ {
			for x := 0; x+ts.g.w <= sh.g.w; x++ {
				if v := ncc(sh.g, sh.in, ts, x, y); v > threshold-0.3 {
					cands = append(cands, cand{x, y, v})
				}
			}
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
		if all {
			// Keep one candidate per neighbourhood so a cluster of near-identical spots costs one refinement.
			var kept []cand
			for _, c := range cands {
				near := false
				for _, k := range kept {
					if abs(c.x-k.x)*2 < ts.g.w && abs(c.y-k.y)*2 < ts.g.h {
						near = true
						break
					}
				}
				if !near {
					kept = append(kept, c)
					if len(kept) == 300 {
						break
					}
				}
			}
			cands = kept
		} else if len(cands) > 8 {
			cands = cands[:8]
		}

		for _, c := range cands {
			best := Match{Score: -1}
			for y := max(0, c.y*f-f); y <= min(s.h-tg.h, c.y*f+f); y++ {
				for x := max(0, c.x*f-f); x <= min(s.w-tg.w, c.x*f+f); x++ {
					if v := ncc(s, fin, full, x, y); v > best.Score {
						best = Match{Bounds: [4]int{x, y, x + tg.w, y + tg.h}, Score: v}
					}
				}
			}
			if best.Score < 0 {
				continue
			}
			if o.Color {
				ab := area.Bounds().Min
				got := meanRGB(area, image.Rect(ab.X+best.Bounds[0], ab.Y+best.Bounds[1], ab.X+best.Bounds[2], ab.Y+best.Bounds[3]))
				d := math.Sqrt((got[0]-want[0])*(got[0]-want[0]) + (got[1]-want[1])*(got[1]-want[1]) + (got[2]-want[2])*(got[2]-want[2]))
				best.Score *= math.Max(0, 1-d/441.673)
			}
			bx, by := origin.X-screen.Bounds().Min.X, origin.Y-screen.Bounds().Min.Y
			best.Bounds = [4]int{best.Bounds[0] + bx, best.Bounds[1] + by, best.Bounds[2] + bx, best.Bounds[3] + by}
			out = append(out, best)
		}
	}
	if !tried {
		return nil, ErrTooBig
	}
	for i := range out {
		if out[i].Score < 0 {
			out[i].Score = 0
		}
	}
	return out, nil
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
