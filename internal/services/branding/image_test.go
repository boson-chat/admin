package branding

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/HugoSmits86/nativewebp"
	"golang.org/x/image/webp"

	"github.com/boson-chat/admin/internal/apperr"
)

func pngOf(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegOf(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = uint8(i)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func animatedGIF(t testing.TB) []byte {
	t.Helper()
	pal := color.Palette{color.Transparent, color.Black, color.White, color.NRGBA{255, 0, 0, 255}}
	g := &gif.GIF{LoopCount: 0}
	for i := range 3 {
		fr := image.NewPaletted(image.Rect(i*2, i*2, i*2+8, i*2+8), pal)
		for j := range fr.Pix {
			fr.Pix[j] = uint8(1 + (i+j)%3)
		}
		g.Image = append(g.Image, fr)
		g.Delay = append(g.Delay, 5)
		g.Disposal = append(g.Disposal, []byte{gif.DisposalNone, gif.DisposalBackground, gif.DisposalPrevious}[i])
	}
	g.Config = image.Config{Width: 16, Height: 16, ColorModel: pal}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func webpOf(t testing.TB, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := nativewebp.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const okSVG = `<?xml version="1.0"?>
<!-- a logo -->
<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="64" height="64" viewBox="0 0 64 64">
  <defs><linearGradient id="g"><stop offset="0" stop-color="#7c3aed"/></linearGradient></defs>
  <style>.a { fill: url(#g); }</style>
  <rect class="a" width="64" height="64" rx="8" style="fill:url(#g)"/>
  <use xlink:href="#g"/>
</svg>`

func TestSniff(t *testing.T) {
	cases := map[string][]byte{
		"png":  pngOf(t, 2, 2),
		"jpeg": jpegOf(t, 2, 2),
		"gif":  animatedGIF(t),
		"webp": webpOf(t, 2, 2),
		"svg":  []byte(okSVG),
		"":     []byte("<html><body>hi</body></html>"),
	}
	for want, b := range cases {
		if got := Sniff(b); got != want {
			t.Errorf("Sniff(%s) = %q", want, got)
		}
	}
	if Sniff([]byte("\xef\xbb\xbf  <svg viewBox='0 0 1 1'/>")) != "svg" {
		t.Error("BOM + bare svg")
	}
}

func TestProcessReencodesRastersToWebP(t *testing.T) {
	for name, in := range map[string][]byte{
		"png":  pngOf(t, 40, 30),
		"jpeg": jpegOf(t, 40, 30),
		"webp": webpOf(t, 40, 30),
	} {
		p, err := Process(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.Ext != "webp" || p.ContentType != "image/webp" || p.Source != name || p.Width != 40 || p.Height != 30 {
			t.Fatalf("%s: %+v", name, p)
		}
		cfg, err := webp.DecodeConfig(bytes.NewReader(p.Data))
		if err != nil || cfg.Width != 40 || cfg.Height != 30 {
			t.Fatalf("%s: output isn't a 40×30 webp: %v %+v", name, err, cfg)
		}
	}
}

func TestProcessAnimatedGIF(t *testing.T) {
	p, err := Process(animatedGIF(t))
	if err != nil {
		t.Fatal(err)
	}
	if p.Ext != "webp" || !bytes.Contains(p.Data[:64], []byte("VP8X")) || !bytes.Contains(p.Data, []byte("ANIM")) || bytes.Count(p.Data, []byte("ANMF")) != 3 {
		t.Fatalf("not an animated webp with 3 frames: % x", p.Data[:32])
	}
}

func TestProcessLimits(t *testing.T) {
	status := func(err error) int {
		e, ok := apperr.As(err)
		if !ok {
			return 0
		}
		return e.Status
	}
	if _, err := Process(pngOf(t, MaxImageSide+1, 1)); status(err) != 422 || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("too wide: %v", err)
	}
	if _, err := Process(pngOf(t, 1, MaxImageSide)); err != nil {
		t.Fatalf("4096 tall is fine: %v", err)
	}
	if _, err := Process(make([]byte, MaxImageBytes+1)); status(err) != 413 {
		t.Fatalf("too big: %v", err)
	}
	if _, err := Process([]byte("GIF89a truncated")); status(err) != 422 {
		t.Fatalf("damaged: %v", err)
	}
	if _, err := Process([]byte("%PDF-1.7")); status(err) != 422 || !strings.Contains(err.Error(), "PNG, JPEG") {
		t.Fatalf("not an image: %v", err)
	}
	if _, err := Process(nil); status(err) != 422 {
		t.Fatalf("empty: %v", err)
	}
}

// A noisy photo is bigger than 2 MB losslessly: it's scaled down to fit.
func TestProcessShrinksHugeLosslessOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	img := image.NewNRGBA(image.Rect(0, 0, 1200, 1200))
	r := rand.New(rand.NewPCG(1, 2))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.IntN(256))
	}
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 85}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() > MaxImageBytes {
		t.Skipf("fixture too big: %d", buf.Len())
	}
	p, err := Process(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := webp.DecodeConfig(bytes.NewReader(p.Data))
	if len(p.Data) > MaxImageBytes || cfg.Width >= 1200 {
		t.Fatalf("%d bytes, %d px", len(p.Data), cfg.Width)
	}
}

func TestSVGPassesThrough(t *testing.T) {
	p, err := Process([]byte(okSVG))
	if err != nil {
		t.Fatal(err)
	}
	if p.Ext != "svg" || p.ContentType != "image/svg+xml" || string(p.Data) != okSVG || p.Width != 64 {
		t.Fatalf("%+v", p)
	}
	if _, err := Process([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><image href="data:image/png;base64,iVBOR"/></svg>`)); err != nil {
		t.Fatalf("embedded png: %v", err)
	}
}

func TestSVGRejected(t *testing.T) {
	cases := map[string]string{
		"script":        `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
		"script ns":     `<svg xmlns="http://www.w3.org/2000/svg" xmlns:s="http://www.w3.org/2000/svg"><s:SCRIPT href="x.js"/></svg>`,
		"foreignObject": `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><div xmlns="http://www.w3.org/1999/xhtml">x</div></foreignObject></svg>`,
		"onload":        `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`,
		"onclick child": `<svg xmlns="http://www.w3.org/2000/svg"><rect onClick="x()"/></svg>`,
		"javascript":    `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink"><a xlink:href="java&#x09;script:alert(1)"><rect/></a></svg>`,
		"external use":  `<svg xmlns="http://www.w3.org/2000/svg"><use href="https://evil.example/x.svg#a"/></svg>`,
		"svg data":      `<svg xmlns="http://www.w3.org/2000/svg"><image href="data:image/svg+xml;base64,PHN2Zz4="/></svg>`,
		"css url":       `<svg xmlns="http://www.w3.org/2000/svg"><rect style="fill:url(https://evil.example/a)"/></svg>`,
		"doctype":       `<?xml version="1.0"?><!DOCTYPE svg [<!ENTITY a "aaaa">]><svg xmlns="http://www.w3.org/2000/svg">&a;</svg>`,
		"stylesheet":    `<?xml-stylesheet href="https://evil.example/a.css"?><svg xmlns="http://www.w3.org/2000/svg"/>`,
		"iframe":        `<svg xmlns="http://www.w3.org/2000/svg"><iframe src="x"/></svg>`,
		"huge":          `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 5000 10"/>`,
		"broken":        `<svg xmlns="http://www.w3.org/2000/svg"><rect></svg>`,
	}
	for name, svg := range cases {
		if _, err := Process([]byte(svg)); err == nil {
			t.Errorf("%s: accepted", name)
		} else if e, ok := apperr.As(err); !ok || e.Status != 422 {
			t.Errorf("%s: %v", name, err)
		}
	}
}
