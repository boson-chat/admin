// Package theme is the boson-theme/1 document shared by the stack, the
// directory and the client (the branding contract): a network's colours,
// corner radius, font and per-pane backgrounds. The same rules apply
// everywhere it is written; the client checks again when it reads one.
//
// In config/boson.yaml it lives under branding.theme without image URLs:
// the images are files in config/branding/ named by branding.images.
package theme

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Version is the document version this panel writes.
const Version = 1

// MaxBytes bounds a whole document as JSON.
const MaxBytes = 16 << 10

// Tokens are the themeable colours, in the client's editor order
// (client/src/renderer/src/modules/ui/theme/theme.types.ts THEME_TOKENS).
var Tokens = []string{
	"bg", "surface", "surface-raised", "surface-hover", "border", "border-soft",
	"fg", "fg-2", "fg-3", "muted", "meta",
	"accent", "accent-on",
	"success", "warn", "danger", "info",
}

// Panes are the client's areas that take a background.
var Panes = []string{"servers", "channels", "messages", "members"}

// Positions are where a pane's image is anchored.
var Positions = []string{"center", "top", "bottom", "left", "right"}

const (
	MaxRadius = 12
	MaxBlur   = 24
)

// Nick tunes the per-nick colours (hue comes from the nick).
type Nick struct {
	Saturation int `json:"saturation" yaml:"saturation"`
	Lightness  int `json:"lightness" yaml:"lightness"`
}

// Pane is one area's background. Image is a URL in a public document and
// never stored in the config file (branding.images holds the file).
type Pane struct {
	BG       string   `json:"bg,omitempty" yaml:"bg,omitempty"`
	Image    string   `json:"image,omitempty" yaml:"-"`
	Dim      *float64 `json:"dim,omitempty" yaml:"dim,omitempty"`
	Blur     *int     `json:"blur,omitempty" yaml:"blur,omitempty"`
	Position string   `json:"position,omitempty" yaml:"position,omitempty"`
}

func (p Pane) empty() bool {
	return p.BG == "" && p.Image == "" && p.Dim == nil && p.Blur == nil && p.Position == ""
}

// Variant is the theme for the other base (variants.<base>): its own
// colours, nick tuning and pane overrides, laid over the primary's panes
// field by field. Radius, font and the logo / sign-in images are shared.
type Variant struct {
	Colors map[string]string `json:"colors,omitempty" yaml:"colors,omitempty"`
	Nick   *Nick             `json:"nick,omitempty" yaml:"nick,omitempty"`
	Panes  map[string]Pane   `json:"panes,omitempty" yaml:"panes,omitempty"`
}

// Theme is a boson-theme/1 document. Field order is the order written to
// the config file.
type Theme struct {
	Version int               `json:"version" yaml:"version"`
	Base    string            `json:"base" yaml:"base"`
	Colors  map[string]string `json:"colors,omitempty" yaml:"colors,omitempty"`
	Nick    *Nick             `json:"nick,omitempty" yaml:"nick,omitempty"`
	Radius  *int              `json:"radius,omitempty" yaml:"radius,omitempty"`
	Font    string            `json:"font,omitempty" yaml:"font,omitempty"`
	Panes   map[string]Pane   `json:"panes,omitempty" yaml:"panes,omitempty"`
	// Variants holds at most the base that isn't Base.
	Variants map[string]Variant `json:"variants,omitempty" yaml:"variants,omitempty"`
	// Image URLs: public documents only (the directory push and the auth
	// service's GET /v1/branding fill them).
	Logo            string `json:"logo,omitempty" yaml:"-"`
	LogoWide        string `json:"logo_wide,omitempty" yaml:"-"`
	LoginBackground string `json:"login_background,omitempty" yaml:"-"`
}

// Default is an empty dark theme (everything from Boson's built-in).
func Default() Theme { return Theme{Version: Version, Base: "dark"} }

var hexRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Color checks a #rrggbb colour and lower-cases it.
func Color(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !hexRe.MatchString(s) {
		return "", fmt.Errorf("%q must be a colour like #7c3aed", s)
	}
	return strings.ToLower(s), nil
}

// Clean validates a document decoded from JSON (map[string]any with
// float64 numbers) and returns the stored form: unknown keys are dropped,
// image URLs too (they live in branding.images), and anything out of range
// is an error naming the field.
func Clean(v any) (Theme, error) {
	t := Default()
	m, ok := v.(map[string]any)
	if !ok {
		return t, fmt.Errorf("the theme must be an object")
	}
	if b, err := json.Marshal(m); err != nil || len(b) > MaxBytes {
		return t, fmt.Errorf("the theme is too large (16 KB at most)")
	}
	if ver, ok := m["version"]; ok {
		if n, ok := num(ver); !ok || n != Version {
			return t, fmt.Errorf("version must be %d", Version)
		}
	}
	if b, ok := m["base"]; ok {
		s, _ := b.(string)
		if s != "dark" && s != "light" {
			return t, fmt.Errorf("base must be dark or light")
		}
		t.Base = s
	}
	var err error
	if t.Colors, t.Nick, err = cleanColorsNick(m); err != nil {
		return t, err
	}
	if r, ok := m["radius"]; ok && r != nil {
		n, ok := num(r)
		if !ok || n < 0 || n > MaxRadius {
			return t, fmt.Errorf("radius must be a whole number from 0 to %d", MaxRadius)
		}
		t.Radius = &n
	}
	if f, ok := m["font"]; ok && f != nil && f != "" {
		s, _ := f.(string)
		if s != "boson" && s != "system" {
			return t, fmt.Errorf("font must be boson or system")
		}
		t.Font = s
	}
	if t.Panes, err = cleanPanes(m); err != nil {
		return t, err
	}
	if v, ok := m["variants"]; ok && v != nil {
		vm, ok := v.(map[string]any)
		if !ok {
			return t, fmt.Errorf("variants must be an object of dark / light")
		}
		for _, base := range []string{"dark", "light"} {
			raw, ok := vm[base]
			// Only the other base: a variant for the theme's own is dropped.
			if !ok || raw == nil || base == t.Base {
				continue
			}
			vv, ok := raw.(map[string]any)
			if !ok {
				return t, fmt.Errorf("variants.%s must be an object", base)
			}
			var variant Variant
			if variant.Colors, variant.Nick, err = cleanColorsNick(vv); err != nil {
				return t, fmt.Errorf("variants.%s.%v", base, err)
			}
			if variant.Panes, err = cleanPanes(vv); err != nil {
				return t, fmt.Errorf("variants.%s.%v", base, err)
			}
			if t.Variants == nil {
				t.Variants = map[string]Variant{}
			}
			t.Variants[base] = variant
		}
	}
	return t, nil
}

// cleanColorsNick checks the colors and nick of the primary or a variant.
func cleanColorsNick(m map[string]any) (map[string]string, *Nick, error) {
	var colors map[string]string
	var nick *Nick
	if c, ok := m["colors"]; ok && c != nil {
		cm, ok := c.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("colors must be an object of token: #rrggbb")
		}
		for _, tok := range Tokens {
			raw, ok := cm[tok]
			if !ok || raw == nil || raw == "" {
				continue
			}
			s, _ := raw.(string)
			c, err := Color(s)
			if err != nil {
				return nil, nil, fmt.Errorf("colors.%s: %v", tok, err)
			}
			if colors == nil {
				colors = map[string]string{}
			}
			colors[tok] = c
		}
	}
	if n, ok := m["nick"]; ok && n != nil {
		nm, ok := n.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("nick must be {saturation, lightness}")
		}
		sat, ok1 := num(nm["saturation"])
		lig, ok2 := num(nm["lightness"])
		if !ok1 || !ok2 || sat < 0 || sat > 100 || lig < 0 || lig > 100 {
			return nil, nil, fmt.Errorf("nick saturation and lightness must be whole numbers from 0 to 100")
		}
		nick = &Nick{Saturation: sat, Lightness: lig}
	}
	return colors, nick, nil
}

// cleanPanes checks the panes of the primary or a variant; empty panes are
// dropped.
func cleanPanes(m map[string]any) (map[string]Pane, error) {
	p, ok := m["panes"]
	if !ok || p == nil {
		return nil, nil
	}
	pm, ok := p.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("panes must be an object")
	}
	var out map[string]Pane
	for _, name := range Panes {
		raw, ok := pm[name]
		if !ok || raw == nil {
			continue
		}
		pane, err := cleanPane(raw)
		if err != nil {
			return nil, fmt.Errorf("panes.%s: %v", name, err)
		}
		if pane.empty() {
			continue
		}
		if out == nil {
			out = map[string]Pane{}
		}
		out[name] = pane
	}
	return out, nil
}

func cleanPane(v any) (Pane, error) {
	var p Pane
	m, ok := v.(map[string]any)
	if !ok {
		return p, fmt.Errorf("must be an object")
	}
	if b, ok := m["bg"]; ok && b != nil && b != "" {
		s, _ := b.(string)
		c, err := Color(s)
		if err != nil {
			return p, fmt.Errorf("bg: %v", err)
		}
		p.BG = c
	}
	if d, ok := m["dim"]; ok && d != nil {
		f, ok := d.(float64)
		if !ok {
			if n, isInt := d.(int); isInt {
				f, ok = float64(n), true
			}
		}
		if !ok || f < 0 || f > 1 {
			return p, fmt.Errorf("dim must be from 0 to 1")
		}
		f = float64(int(f*100+0.5)) / 100
		p.Dim = &f
	}
	if b, ok := m["blur"]; ok && b != nil {
		n, ok := num(b)
		if !ok || n < 0 || n > MaxBlur {
			return p, fmt.Errorf("blur must be a whole number of pixels from 0 to %d", MaxBlur)
		}
		p.Blur = &n
	}
	if pos, ok := m["position"]; ok && pos != nil && pos != "" {
		s, _ := pos.(string)
		if !slices.Contains(Positions, s) {
			return p, fmt.Errorf("position must be one of %s", strings.Join(Positions, ", "))
		}
		p.Position = s
	}
	return p, nil
}

// num reads a whole number from JSON (float64) or YAML (int).
func num(v any) (int, bool) {
	switch x := v.(type) {
	case float64:
		if x != float64(int(x)) {
			return 0, false
		}
		return int(x), true
	case int:
		return x, true
	}
	return 0, false
}

// ---- images ----

// Slots are the images a theme can have, as branding.images keys: the
// shared logo and sign-in images, each pane's image, and each pane's image
// in a variant (variants.<base>.panes.<pane>.image; without one the
// variant uses the primary pane's).
var Slots = func() []string {
	out := []string{"logo", "logo_wide", "login_background"}
	for _, prefix := range []string{"", "variants.dark.", "variants.light."} {
		for _, p := range Panes {
			out = append(out, prefix+"panes."+p+".image")
		}
	}
	return out
}()

// SlotVariant is the variant a slot belongs to ("" for the primary's).
func SlotVariant(slot string) string {
	for _, b := range []string{"dark", "light"} {
		if strings.HasPrefix(slot, "variants."+b+".") {
			return b
		}
	}
	return ""
}

// ValidSlot reports whether s is a branding.images key.
func ValidSlot(s string) bool { return slices.Contains(Slots, s) }

// FileStem is the slot's file name prefix in config/branding/
// (panes.messages.image → pane-messages, variants.light.panes.messages.image
// → pane-messages-light).
func FileStem(slot string) string {
	if v := SlotVariant(slot); v != "" {
		return FileStem(strings.TrimPrefix(slot, "variants."+v+".")) + "-" + v
	}
	if p, ok := strings.CutPrefix(slot, "panes."); ok {
		return "pane-" + strings.TrimSuffix(p, ".image")
	}
	return slot
}

// FileRe matches the files the panel writes: <stem>-<hash>.<ext>.
var FileRe = regexp.MustCompile(`^[a-z_]+(-[a-z]+){0,2}-[0-9a-f]{12,64}\.(webp|svg)$`)

// WithImages fills t's image URLs from images (slot → URL).
func (t Theme) WithImages(images map[string]string) Theme {
	out := t
	out.Logo, out.LogoWide, out.LoginBackground = images["logo"], images["logo_wide"], images["login_background"]
	if len(t.Panes) > 0 || hasPaneImage(images) {
		out.Panes = map[string]Pane{}
		for k, v := range t.Panes {
			out.Panes[k] = v
		}
		for _, name := range Panes {
			if u := images["panes."+name+".image"]; u != "" {
				p := out.Panes[name]
				p.Image = u
				out.Panes[name] = p
			}
		}
	}
	return out
}

func hasPaneImage(images map[string]string) bool {
	for k := range images {
		if strings.HasPrefix(k, "panes.") {
			return true
		}
	}
	return false
}

// CleanImages validates branding.images (slot → file name).
func CleanImages(v any) (map[string]string, error) {
	out := map[string]string{}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be a mapping of slot: file")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !ValidSlot(k) {
			return nil, fmt.Errorf("%s isn't an image slot (%s)", k, strings.Join(Slots, ", "))
		}
		s, _ := m[k].(string)
		if s == "" || strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".") || len(s) > 128 {
			return nil, fmt.Errorf("%s: %q must be a file name in config/branding/", k, s)
		}
		out[k] = s
	}
	return out, nil
}

// ForDirectory prepares auth's public document (GET /v1/branding) for the
// directory's PUT /v1/networks/{id}/theme: an image the network doesn't
// have is sent as "" (remove), since an omitted one keeps the directory's.
func ForDirectory(doc map[string]any) map[string]any {
	out := make(map[string]any, len(doc)+3)
	for k, v := range doc {
		out[k] = v
	}
	for _, k := range []string{"logo", "logo_wide", "login_background"} {
		if s, _ := out[k].(string); s == "" {
			out[k] = ""
		}
	}
	panes := map[string]any{}
	if p, ok := out["panes"].(map[string]any); ok {
		for k, v := range p {
			panes[k] = v
		}
	}
	for _, name := range Panes {
		pane := map[string]any{}
		if p, ok := panes[name].(map[string]any); ok {
			for k, v := range p {
				pane[k] = v
			}
		}
		if s, _ := pane["image"].(string); s == "" {
			pane["image"] = ""
		}
		panes[name] = pane
	}
	out["panes"] = panes
	return out
}
