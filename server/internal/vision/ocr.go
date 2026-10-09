package vision

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ErrNoTesseract means OCR was asked for but Tesseract is not installed.
var ErrNoTesseract = errors.New("Tesseract is not installed")

// Line is one line of text Tesseract read, in screenshot pixels.
type Line struct {
	Text   string
	Bounds [4]int
	Conf   float64
	Words  []Word
}

type Word struct {
	Text   string
	Bounds [4]int
	Conf   float64
}

// Tesseract finds the tesseract program: the configured path, then PATH, then
// the Windows installer's default folder.
func Tesseract(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("%w: %s does not exist", ErrNoTesseract, configured)
		}
		return configured, nil
	}
	if p, err := exec.LookPath("tesseract"); err == nil {
		return p, nil
	}
	if runtime.GOOS == "windows" {
		for _, dir := range []string{os.Getenv("ProgramFiles"), os.Getenv("LOCALAPPDATA") + `\Programs`} {
			p := filepath.Join(dir, "Tesseract-OCR", "tesseract.exe")
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", ErrNoTesseract
}

// OCR runs Tesseract on a PNG and groups its words into lines.
func OCR(bin string, png []byte, lang string, timeout time.Duration) ([]Line, error) {
	if lang == "" {
		lang = "eng"
	}
	cmd := exec.Command(bin, "stdin", "stdout", "-l", lang, "tsv")
	cmd.Stdin = bytes.NewReader(png)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoTesseract, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			msg := strings.TrimSpace(errOut.String())
			if strings.Contains(msg, "Failed loading language") || strings.Contains(msg, "Error opening data file") {
				return nil, fmt.Errorf("%w: the language data for %q is not installed", ErrNoTesseract, lang)
			}
			return nil, fmt.Errorf("tesseract failed: %v %s", err, msg)
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("tesseract took longer than %s", timeout)
	}
	return ParseTSV(out.String()), nil
}

// ParseTSV turns Tesseract's tsv output into lines of words.
func ParseTSV(tsv string) []Line {
	type key struct{ block, par, line int }
	var order []key
	lines := map[key]*Line{}
	for i, row := range strings.Split(strings.ReplaceAll(tsv, "\r", ""), "\n") {
		cols := strings.Split(row, "\t")
		if i == 0 || len(cols) < 12 || cols[0] != "5" {
			continue
		}
		text := strings.TrimSpace(cols[11])
		if text == "" {
			continue
		}
		n := make([]int, 10)
		for j := 0; j < 10; j++ {
			n[j], _ = strconv.Atoi(cols[j])
		}
		conf, _ := strconv.ParseFloat(cols[10], 64)
		w := Word{Text: text, Bounds: [4]int{n[6], n[7], n[6] + n[8], n[7] + n[9]}, Conf: conf}
		k := key{n[2], n[3], n[4]}
		l := lines[k]
		if l == nil {
			l = &Line{Bounds: w.Bounds}
			lines[k] = l
			order = append(order, k)
		}
		l.Words = append(l.Words, w)
		l.Bounds = [4]int{min(l.Bounds[0], w.Bounds[0]), min(l.Bounds[1], w.Bounds[1]), max(l.Bounds[2], w.Bounds[2]), max(l.Bounds[3], w.Bounds[3])}
	}
	out := make([]Line, 0, len(order))
	for _, k := range order {
		l := lines[k]
		var parts []string
		var conf float64
		for _, w := range l.Words {
			parts = append(parts, w.Text)
			conf += w.Conf
		}
		l.Text = strings.Join(parts, " ")
		l.Conf = conf / float64(len(l.Words))
		out = append(out, *l)
	}
	return out
}

// FindText returns the bounds of text: a word equal to it first, else a line that contains it.
func FindText(lines []Line, text string) (Line, bool) {
	for _, l := range lines {
		for _, w := range l.Words {
			if w.Text == text {
				return Line{Text: w.Text, Bounds: w.Bounds, Conf: w.Conf}, true
			}
		}
	}
	for _, l := range lines {
		if strings.Contains(l.Text, text) {
			return l, true
		}
	}
	return Line{}, false
}
