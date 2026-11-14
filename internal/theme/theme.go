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
	if c, ok := m["colors"]; ok && c != nil {
		cm, ok := c.(map[string]any)
		if !ok {
			return t, fmt.Errorf("colors must be an object of token: #rrggbb")
		}
		for _, tok := range Tokens {
			raw, ok := cm[tok]
			if !ok || raw == nil || raw == "" {
				continue
			}
			s, _ := raw.(string)
			c, err := Color(s)
			if err != nil {
				return t, fmt.Errorf("colors.%s: %v", tok, err)
			}
			if t.Colors == nil {
				t.Colors = map[string]string{}
			}
			t.Colors[tok] = c
		}
	}
	if n, ok := m["nick"]; ok && n != nil {
		nm, ok := n.(map[string]any)
		if !ok {
			return t, fmt.Errorf("nick must be {saturation, lightness}")
		}
		sat, ok1 := num(nm["saturation"])
		lig, ok2 := num(nm["lightness"])
		if !ok1 || !ok2 || sat < 0 || sat > 100 || lig < 0 || lig > 100 {
			return t, fmt.Errorf("nick saturation and lightness must be whole numbers from 0 to 100")
		}
		t.Nick = &Nick{Saturation: sat, Lightness: lig}
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
	if p, ok := m["panes"]; ok && p != nil {
		pm, ok := p.(map[string]any)
		if !ok {
			return t, fmt.Errorf("panes must be an object")
		}
		for _, name := range Panes {
			raw, ok := pm[name]
			if !ok || raw == nil {
				continue
			}
			pane, err := cleanPane(raw)
			if err != nil {
				return t, fmt.Errorf("panes.%s: %v", name, err)
			}
			if pane.empty() {
				continue
			}
			if t.Panes == nil {
				t.Panes = map[string]Pane{}
			}
			t.Panes[name] = pane
		}
	}
	return t, nil
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

// Slots are the images a theme can have, as branding.images keys.
var Slots = []string{"logo", "logo_wide", "login_background",
	"panes.servers.image", "panes.channels.image", "panes.messages.image", "panes.members.image"}

// ValidSlot reports whether s is a branding.images key.
func ValidSlot(s string) bool { return slices.Contains(Slots, s) }

// FileStem is the slot's file name prefix in config/branding/
// (panes.messages.image → pane-messages).
func FileStem(slot string) string {
	if p, ok := strings.CutPrefix(slot, "panes."); ok {
		return "pane-" + strings.TrimSuffix(p, ".image")
	}
	return slot
}

// FileRe matches the files the panel writes: <stem>-<hash>.<ext>.
var FileRe = regexp.MustCompile(`^[a-z_]+(-[a-z]+)?-[0-9a-f]{12,64}\.(webp|svg)$`)

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
