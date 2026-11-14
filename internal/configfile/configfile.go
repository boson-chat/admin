// Package configfile keeps the stack's shared config file (config/boson.yaml,
// BOSON_CONFIG): it loads and watches it, and edits it in place without
// losing what people wrote by hand.
//
// The file is shared by every service in the stack; each reads its own
// section. The admin panel is the only writer besides humans, so edits go
// through the yaml.v3 Node API: comments, key order, quoting and unknown
// keys survive a save. Writes are atomic (temp file, fsync, rename, fsync
// of the directory) so a reader never sees half a file, and serialised
// here; across panels and hand edits the last write wins, optionally
// guarded by If-Match on the file's ETag.
//
// A file that doesn't parse is never replaced: the last good snapshot stays
// in use, the problem (with its line) is reported, and saves are refused
// until someone fixes it.
package configfile

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"gopkg.in/yaml.v3"
)

// Version is the schema version this panel writes and understands.
const Version = 1

// FileMode is used for a new file: readable by every service's user.
// Secrets never go in it.
const FileMode fs.FileMode = 0o644

// Snapshot is one successfully parsed version of the file.
type Snapshot struct {
	// Exists is false when the file isn't there (everything is a default).
	Exists bool
	Raw    []byte
	// Doc is the parsed document; its root is a mapping. Treat as read-only.
	Doc      *yaml.Node
	ETag     string
	ModTime  time.Time
	LoadedAt time.Time
	// SavedBy / SavedAt come from the footer the panel writes on save.
	SavedBy string
	SavedAt time.Time
}

// Root is the top-level mapping (nil when the file is missing or empty).
func (s *Snapshot) Root() *yaml.Node {
	if s == nil || s.Doc == nil || len(s.Doc.Content) == 0 {
		return nil
	}
	return s.Doc.Content[0]
}

// Problem is why the file on disk isn't in use.
type Problem struct {
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

func (p *Problem) Error() string {
	switch {
	case p.Line > 0 && p.Column > 0:
		return fmt.Sprintf("line %d, column %d: %s", p.Line, p.Column, p.Message)
	case p.Line > 0:
		return fmt.Sprintf("line %d: %s", p.Line, p.Message)
	}
	return p.Message
}

var (
	// ErrConflict: If-Match didn't match the file on disk.
	ErrConflict = errors.New("the config file changed since it was read")
	// ErrNotConfigured: no BOSON_CONFIG path.
	ErrNotConfigured = errors.New("no config file is configured (BOSON_CONFIG)")
)

// InvalidError: the file on disk doesn't parse, so it can't be edited.
type InvalidError struct{ Problem *Problem }

func (e *InvalidError) Error() string { return "the config file has an error: " + e.Problem.Error() }

// Store holds the file's last good snapshot.
type Store struct {
	path   string
	header string
	now    func() time.Time

	wmu sync.Mutex // serialises Update and Reload

	mu       sync.RWMutex
	cur      *Snapshot
	problem  *Problem
	diskRaw  []byte // what's on disk, even when it doesn't parse
	diskETag string
	subs     []func(*Snapshot)
}

// Options for Open.
type Options struct {
	// Header is the comment written at the top of a file the panel creates.
	Header string
	Now    func() time.Time
}

// Open loads path. A missing or broken file isn't an error here: the store
// starts empty (env defaults) and reports the problem.
func Open(path string, o Options) *Store {
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Store{path: path, header: o.Header, now: o.Now, cur: &Snapshot{LoadedAt: o.Now()}}
	s.Reload()
	return s
}

// Path is the file's path inside the container.
func (s *Store) Path() string { return s.path }

// Current is the last good snapshot and the current problem, if any.
func (s *Store) Current() (*Snapshot, *Problem) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur, s.problem
}

// Disk is the file's bytes as last read (also when they don't parse).
func (s *Store) Disk() ([]byte, string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.diskRaw, s.diskETag
}

// OnChange registers fn, called (outside locks) with each new good snapshot.
func (s *Store) OnChange(fn func(*Snapshot)) {
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	s.mu.Unlock()
}

func etag(b []byte) string {
	h := sha256.Sum256(b)
	return `"` + hex.EncodeToString(h[:12]) + `"`
}

// read loads and parses the file from disk.
func (s *Store) read() (*Snapshot, []byte, *Problem) {
	raw, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Snapshot{LoadedAt: s.now(), ETag: etag(nil)}, nil, nil
	}
	if err != nil {
		return nil, nil, &Problem{Message: "can't read the file: " + err.Error()}
	}
	var mod time.Time
	if fi, err := os.Stat(s.path); err == nil {
		mod = fi.ModTime()
	}
	doc, p := Parse(raw)
	if p != nil {
		return nil, raw, p
	}
	snap := &Snapshot{Exists: true, Raw: raw, Doc: doc, ETag: etag(raw), ModTime: mod, LoadedAt: s.now()}
	snap.SavedBy, snap.SavedAt = parseFooter(raw)
	return snap, raw, nil
}

// Reload re-reads the file. It reports whether a new good snapshot is in use.
func (s *Store) Reload() bool {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.reload()
}

func (s *Store) reload() bool {
	snap, raw, p := s.read()
	s.mu.Lock()
	if snap == nil {
		s.diskRaw, s.diskETag = raw, etag(raw)
		s.problem = p
		s.mu.Unlock()
		return false
	}
	s.diskRaw, s.diskETag = snap.Raw, snap.ETag
	s.problem = nil
	if s.cur != nil && s.cur.ETag == snap.ETag && s.cur.Exists == snap.Exists && s.cur.ModTime.Equal(snap.ModTime) {
		s.mu.Unlock()
		return false
	}
	s.cur = snap
	subs := append([]func(*Snapshot){}, s.subs...)
	s.mu.Unlock()
	for _, fn := range subs {
		fn(snap)
	}
	return true
}

var lineRe = regexp.MustCompile(`^yaml: line (\d+): (.*)$`)

// Parse parses raw as a v1 config document. An empty file is an empty
// mapping.
func Parse(raw []byte) (*yaml.Node, *Problem) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	err := dec.Decode(&doc)
	if errors.Is(err, io.EOF) {
		return newDoc(""), nil
	}
	if err != nil {
		msg := err.Error()
		if m := lineRe.FindStringSubmatch(msg); m != nil {
			line, _ := strconv.Atoi(m[1])
			return nil, &Problem{Message: m[2], Line: line}
		}
		return nil, &Problem{Message: strings.TrimPrefix(msg, "yaml: ")}
	}
	var extra yaml.Node
	if dec.Decode(&extra) == nil {
		return nil, &Problem{Message: "the file holds more than one YAML document (---)", Line: extra.Line}
	}
	if len(doc.Content) == 0 {
		return newDoc(doc.HeadComment), nil
	}
	root := doc.Content[0]
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		d := newDoc(doc.HeadComment)
		d.Content[0].HeadComment, d.Content[0].FootComment = root.HeadComment, root.FootComment
		return d, nil
	}
	if root.Kind != yaml.MappingNode {
		return nil, &Problem{Message: "the top level must be a mapping (key: value lines)", Line: root.Line, Column: root.Column}
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if k.Value != "version" {
			continue
		}
		if n, err := strconv.Atoi(v.Value); err != nil || v.Kind != yaml.ScalarNode || n != Version {
			return nil, &Problem{Message: fmt.Sprintf("version must be %d, got %q", Version, v.Value), Line: v.Line, Column: v.Column}
		}
	}
	if k := duplicateKey(root, ""); k != nil {
		return nil, k
	}
	return &doc, nil
}

// duplicateKey: yaml.v3 decodes into nodes without complaining about
// repeated keys, but which one wins would then depend on the reader.
func duplicateKey(n *yaml.Node, prefix string) *Problem {
	switch n.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if seen[k.Value] {
				return &Problem{Message: fmt.Sprintf("%s%s is set twice", prefix, k.Value), Line: k.Line, Column: k.Column}
			}
			seen[k.Value] = true
			if p := duplicateKey(n.Content[i+1], prefix+k.Value+"."); p != nil {
				return p
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if p := duplicateKey(c, prefix); p != nil {
				return p
			}
		}
	}
	return nil
}

func newDoc(head string) *yaml.Node {
	return &yaml.Node{Kind: yaml.DocumentNode, HeadComment: head, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
}

// Edit changes the document in place. Set/Unset are on the root mapping.
type Edit func(root *yaml.Node) error

// Update applies edit to the file on disk (re-read first, so hand edits
// made since the last reload are kept) and writes it atomically. ifMatch,
// when not empty, must equal the file's current ETag. actor goes in the
// footer comment.
func (s *Store) Update(ifMatch, actor string, edit Edit) (*Snapshot, error) {
	if s == nil || s.path == "" {
		return nil, ErrNotConfigured
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()

	snap, _, p := s.read()
	if p != nil {
		s.reload() // record the problem for status
		return nil, &InvalidError{Problem: p}
	}
	if ifMatch != "" && ifMatch != "*" && ifMatch != snap.ETag {
		return nil, ErrConflict
	}
	doc := snap.Doc
	if doc == nil {
		doc = newDoc(s.header)
	} else if !snap.Exists || (doc.HeadComment == "" && len(doc.Content[0].Content) == 0 && doc.Content[0].HeadComment == "") {
		doc.HeadComment = s.header
	}
	root := doc.Content[0]
	if err := edit(root); err != nil {
		return nil, err
	}
	ensureVersion(root)
	setFooter(doc, actor, s.now())

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encoding the config file: %w", err)
	}
	_ = enc.Close()
	out := buf.Bytes()
	if _, p := Parse(out); p != nil { // never write something we can't read back
		return nil, fmt.Errorf("the edited config file doesn't parse: %s", p.Error())
	}
	if err := writeAtomic(s.path, out); err != nil {
		return nil, err
	}
	s.reload()
	cur, _ := s.Current()
	return cur, nil
}

func ensureVersion(root *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "version" {
			return
		}
	}
	k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "version"}
	v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(Version)}
	// A head comment on the first key belongs to the file, not the key:
	// keep it at the top.
	if len(root.Content) > 0 {
		k.HeadComment, root.Content[0].HeadComment = root.Content[0].HeadComment, ""
	}
	root.Content = append([]*yaml.Node{k, v}, root.Content...)
}

const footerPrefix = "last saved in the admin panel by "

var footerRe = regexp.MustCompile(`(?m)^version:.*# ` + regexp.QuoteMeta(footerPrefix) + `(.+?) at (\S+)\s*$`)

// setFooter notes who saved last, as a comment on the version line (the
// top of the file, where people look first).
func setFooter(doc *yaml.Node, actor string, at time.Time) {
	if actor == "" {
		actor = "an admin"
	}
	actor = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '#' {
			return -1
		}
		return r
	}, actor)
	_, v := Lookup(doc.Content[0], []string{"version"})
	if v != nil {
		v.LineComment = "# " + footerPrefix + actor + " at " + at.UTC().Format(time.RFC3339)
	}
}

func parseFooter(raw []byte) (string, time.Time) {
	m := footerRe.FindAllSubmatch(raw, -1)
	if len(m) == 0 {
		return "", time.Time{}
	}
	last := m[len(m)-1]
	t, _ := time.Parse(time.RFC3339, string(last[2]))
	return string(last[1]), t
}

// writeAtomic replaces path with data: temp file in the same directory,
// fsync, rename over the old file, fsync the directory. The old file's
// permissions are kept (FileMode for a new one).
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := FileMode
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("writing the config file: %w", err)
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // no-op after the rename
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the config file: %w", err)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the config file: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("writing the config file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("writing the config file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replacing the config file: %w", err)
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Watch reloads the file when it changes until ctx ends: fsnotify on the
// directory (an atomic save replaces the file's inode, so watching the file
// itself would go deaf), plus a poll every interval for mounts where
// events don't arrive (Docker Desktop, NFS).
func (s *Store) Watch(ctx context.Context, interval time.Duration, onErr func(error)) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	var events <-chan fsnotify.Event
	var errs <-chan error
	if w, err := fsnotify.NewWatcher(); err == nil {
		if err := w.Add(filepath.Dir(s.path)); err == nil {
			events, errs = w.Events, w.Errors
		} else if onErr != nil {
			onErr(fmt.Errorf("watching %s (polling instead): %w", filepath.Dir(s.path), err))
		}
		defer func() { _ = w.Close() }()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	base := filepath.Base(s.path)
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			if filepath.Base(ev.Name) == base {
				debounce = time.After(150 * time.Millisecond) // editors write in bursts
			}
		case err := <-errs:
			if onErr != nil && err != nil {
				onErr(err)
			}
		case <-debounce:
			debounce = nil
			s.Reload()
		case <-t.C:
			s.Reload()
		}
	}
}
