package branding

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/HugoSmits86/nativewebp"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"

	"github.com/boson-chat/admin/internal/apperr"
)

// Limits from the branding contract.
const (
	MaxImageBytes = 2 << 20
	MaxImageSide  = 4096
	// maxFrames bounds an animated GIF.
	maxFrames = 200
	// maxPixels bounds all frames of an animation together (decoded RGBA).
	maxPixels = 64 << 20
)

// Processed is an image ready to be written to config/branding/.
type Processed struct {
	Data        []byte
	Ext         string // webp | svg
	ContentType string
	// Source is the sniffed input type (png, jpeg, webp, gif, svg).
	Source        string
	Width, Height int
}

func badImage(msg string) error {
	return apperr.New(http.StatusUnprocessableEntity, "unsupported_image", msg)
}

var errTooLarge = apperr.New(http.StatusRequestEntityTooLarge, "image_too_large", "the image is too large (2 MB at most)")

// Sniff names the image type from its first bytes: png, jpeg, webp, gif,
// svg, or "" for anything else. The Content-Type the browser sent is never
// trusted.
func Sniff(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\x89PNG\r\n\x1a\n")):
		return "png"
	case bytes.HasPrefix(b, []byte{0xff, 0xd8, 0xff}):
		return "jpeg"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "gif"
	}
	if looksSVG(b) {
		return "svg"
	}
	return ""
}

var svgStart = regexp.MustCompile(`(?is)^\s*(<\?xml[^>]*>\s*)?(<!--.*?-->\s*)*<svg[\s>]`)

func looksSVG(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(b) > 4096 {
		b = b[:4096]
	}
	return svgStart.Match(b)
}

// Process checks an upload and turns it into what's stored: rasters
// re-encoded to WebP (dropping metadata and anything hiding in the file),
// SVG passed through once it holds nothing that could run.
func Process(data []byte) (*Processed, error) {
	if len(data) > MaxImageBytes {
		return nil, errTooLarge
	}
	if len(data) == 0 {
		return nil, badImage("send the image as the request body")
	}
	kind := Sniff(data)
	switch kind {
	case "":
		return nil, badImage("that isn't a PNG, JPEG, WebP, GIF or SVG image")
	case "svg":
		w, h, err := CheckSVG(data)
		if err != nil {
			return nil, err
		}
		return &Processed{Data: data, Ext: "svg", ContentType: "image/svg+xml", Source: kind, Width: w, Height: h}, nil
	}
	cfg, err := decodeConfig(kind, data)
	if err != nil {
		return nil, badImage("the " + strings.ToUpper(kind) + " file is damaged or incomplete")
	}
	if cfg.Width < 1 || cfg.Height < 1 {
		return nil, badImage("the image is empty")
	}
	if cfg.Width > MaxImageSide || cfg.Height > MaxImageSide {
		return nil, badImage(fmt.Sprintf("the image is %d×%d; at most %d pixels on a side", cfg.Width, cfg.Height, MaxImageSide))
	}
	out, err := reencode(kind, data, cfg)
	if err != nil {
		return nil, err
	}
	return &Processed{Data: out, Ext: "webp", ContentType: "image/webp", Source: kind, Width: cfg.Width, Height: cfg.Height}, nil
}

func decodeConfig(kind string, data []byte) (image.Config, error) {
	r := bytes.NewReader(data)
	switch kind {
	case "png":
		return png.DecodeConfig(r)
	case "jpeg":
		return jpeg.DecodeConfig(r)
	case "gif":
		return gif.DecodeConfig(r)
	case "webp":
		return webp.DecodeConfig(r)
	}
	return image.Config{}, errors.New("unknown")
}

func reencode(kind string, data []byte, cfg image.Config) ([]byte, error) {
	damaged := badImage("the " + strings.ToUpper(kind) + " file is damaged or incomplete")
	if kind == "gif" {
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, damaged
		}
		if len(g.Image) > 1 {
			return encodeAnimation(g, cfg)
		}
	}
	var img image.Image
	var err error
	switch kind {
	case "png":
		img, err = png.Decode(bytes.NewReader(data))
	case "jpeg":
		img, err = jpeg.Decode(bytes.NewReader(data))
	case "gif":
		img, err = gif.Decode(bytes.NewReader(data))
	case "webp":
		img, err = webp.Decode(bytes.NewReader(data))
	}
	if err != nil {
		if kind == "webp" && bytes.Contains(data[:min(len(data), 256)], []byte("ANIM")) {
			return nil, badImage("animated WebP isn't supported; upload the animation as a GIF")
		}
		return nil, damaged
	}
	return encodeStill(img)
}

// encodeStill writes img as lossless WebP. A photo can grow past the size
// limit losslessly; it's scaled down in steps until it fits (a login
// background doesn't need 4096 px).
func encodeStill(img image.Image) ([]byte, error) {
	src := toNRGBA(img)
	for range 6 {
		var buf bytes.Buffer
		if err := nativewebp.Encode(&buf, src, nil); err != nil {
			return nil, fmt.Errorf("encoding WebP: %w", err)
		}
		if buf.Len() <= MaxImageBytes {
			return buf.Bytes(), nil
		}
		b := src.Bounds()
		w, h := b.Dx()*3/4, b.Dy()*3/4
		if w < 256 || h < 256 {
			break
		}
		next := image.NewNRGBA(image.Rect(0, 0, w, h))
		xdraw.CatmullRom.Scale(next, next.Bounds(), src, b, draw.Src, nil)
		src = next
	}
	return nil, errTooLarge
}

func toNRGBA(img image.Image) *image.NRGBA {
	if n, ok := img.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out
}

// encodeAnimation composites the GIF's frames (they may be partial, with
// disposal rules) into full frames and writes an animated WebP.
func encodeAnimation(g *gif.GIF, cfg image.Config) ([]byte, error) {
	if len(g.Image) > maxFrames {
		return nil, badImage(fmt.Sprintf("the animation has %d frames; at most %d", len(g.Image), maxFrames))
	}
	if cfg.Width*cfg.Height*len(g.Image) > maxPixels {
		return nil, badImage("the animation is too large; use fewer frames or a smaller size")
	}
	bounds := image.Rect(0, 0, cfg.Width, cfg.Height)
	canvas := image.NewNRGBA(bounds)
	ani := &nativewebp.Animation{LoopCount: uint16(max(g.LoopCount, 0))}
	for i, fr := range g.Image {
		var prev *image.NRGBA
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			prev = image.NewNRGBA(bounds)
			copy(prev.Pix, canvas.Pix)
		}
		draw.Draw(canvas, fr.Bounds().Intersect(bounds), fr, fr.Bounds().Min, draw.Over)
		frame := image.NewNRGBA(bounds)
		copy(frame.Pix, canvas.Pix)
		ani.Images = append(ani.Images, frame)
		delay := 100
		if i < len(g.Delay) && g.Delay[i] > 0 {
			delay = g.Delay[i] * 10
		}
		ani.Durations = append(ani.Durations, uint(delay))
		ani.Disposals = append(ani.Disposals, 0)
		switch disposal {
		case gif.DisposalBackground:
			draw.Draw(canvas, fr.Bounds().Intersect(bounds), image.NewUniform(color.Transparent), image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = prev
		}
	}
	var buf bytes.Buffer
	if err := nativewebp.EncodeAll(&buf, ani, nil); err != nil {
		return nil, fmt.Errorf("encoding WebP: %w", err)
	}
	if buf.Len() > MaxImageBytes {
		return nil, errTooLarge
	}
	return buf.Bytes(), nil
}

// ---- SVG ----

// CheckSVG accepts an SVG only when it can't run anything: no script
// elements, foreignObject (HTML inside), event handler attributes,
// javascript: or external links, entities or XML stylesheets. Clients only
// ever render it as an image, where scripts don't run anyway; this keeps it
// safe if someone opens the file directly. It returns the declared size
// (0 when it only has a viewBox in relative units).
func CheckSVG(data []byte) (int, int, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	dec.Entity = map[string]string{}
	depth, w, h := 0, 0, 0
	sawRoot := false
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, 0, badImage("the SVG isn't well-formed XML: " + err.Error())
		}
		switch t := tok.(type) {
		case xml.Directive:
			// <!DOCTYPE …> can declare entities (billion laughs, external files).
			return 0, 0, badImage("the SVG has a DOCTYPE or entity declaration; remove it")
		case xml.ProcInst:
			if strings.EqualFold(t.Target, "xml-stylesheet") {
				return 0, 0, badImage("the SVG links a stylesheet; inline the styles")
			}
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if depth == 0 {
				if name != "svg" {
					return 0, 0, badImage("that isn't an SVG image")
				}
				sawRoot = true
				var err error
				if w, h, err = svgSize(t.Attr); err != nil {
					return 0, 0, err
				}
			}
			switch name {
			case "script":
				return 0, 0, badImage("the SVG has a <script>; images can't contain scripts")
			case "foreignobject":
				return 0, 0, badImage("the SVG has a <foreignObject>; remove the embedded HTML")
			case "iframe", "embed", "object", "handler", "listener":
				return 0, 0, badImage("the SVG embeds <" + t.Name.Local + ">; remove it")
			}
			for _, a := range t.Attr {
				if err := checkAttr(a); err != nil {
					return 0, 0, err
				}
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth > 0 && bytes.Contains(bytes.ToLower(t), []byte("javascript:")) {
				return 0, 0, badImage("the SVG contains a javascript: URL")
			}
		}
	}
	if !sawRoot {
		return 0, 0, badImage("that isn't an SVG image")
	}
	return w, h, nil
}

func checkAttr(a xml.Attr) error {
	name := strings.ToLower(a.Name.Local)
	val := strings.ToLower(strings.Join(strings.Fields(a.Value), ""))
	if strings.HasPrefix(name, "on") {
		return badImage("the SVG has an event handler (" + a.Name.Local + "); remove it")
	}
	if strings.Contains(val, "javascript:") || strings.Contains(val, "vbscript:") {
		return badImage("the SVG contains a script URL")
	}
	if name == "href" { // href or xlink:href
		if strings.HasPrefix(val, "#") || strings.HasPrefix(val, "data:image/png") ||
			strings.HasPrefix(val, "data:image/jpeg") || strings.HasPrefix(val, "data:image/webp") || strings.HasPrefix(val, "data:image/gif") {
			return nil
		}
		return badImage("the SVG links to " + truncate(a.Value, 40) + "; only #fragments and embedded PNG/JPEG/WebP/GIF images are allowed")
	}
	if name == "style" && (strings.Contains(val, "url(") && !strings.Contains(val, "url(#")) {
		return badImage("the SVG loads something through a style url(); only url(#id) is allowed")
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var lengthRe = regexp.MustCompile(`^\s*([0-9]*\.?[0-9]+)\s*(px)?\s*$`)

// svgSize reads width/height (or the viewBox) and checks the 4096 px limit.
func svgSize(attrs []xml.Attr) (int, int, error) {
	get := func(n string) string {
		for _, a := range attrs {
			if a.Name.Local == n {
				return a.Value
			}
		}
		return ""
	}
	px := func(s string) int {
		m := lengthRe.FindStringSubmatch(s)
		if m == nil {
			return 0
		}
		f, _ := strconv.ParseFloat(m[1], 64)
		return int(f + 0.5)
	}
	w, h := px(get("width")), px(get("height"))
	if w == 0 || h == 0 {
		f := strings.FieldsFunc(get("viewBox"), func(r rune) bool { return r == ' ' || r == ',' })
		if len(f) == 4 {
			vw, _ := strconv.ParseFloat(f[2], 64)
			vh, _ := strconv.ParseFloat(f[3], 64)
			w, h = int(vw+0.5), int(vh+0.5)
		}
	}
	if w > MaxImageSide || h > MaxImageSide {
		return 0, 0, badImage(fmt.Sprintf("the SVG is %d×%d; at most %d pixels on a side", w, h, MaxImageSide))
	}
	return w, h, nil
}
