package configfile

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Split turns "auth.smtp.host" into its path.
func Split(key string) []string { return strings.Split(key, ".") }

func find(m *yaml.Node, key string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// Lookup returns the key node and value node at path, or nils.
func Lookup(root *yaml.Node, path []string) (k, v *yaml.Node) {
	n := root
	for _, p := range path {
		i := find(n, p)
		if i < 0 {
			return nil, nil
		}
		k, v = n.Content[i], n.Content[i+1]
		n = v
	}
	return k, v
}

// Set puts value at path, creating mappings on the way. A replaced value
// keeps its comments and, for lists and mappings, its flow or block style.
func Set(root *yaml.Node, path []string, value any) error {
	var nv yaml.Node
	if err := nv.Encode(value); err != nil {
		return err
	}
	return SetNode(root, path, &nv)
}

// SetNode is Set with a ready node (copied in).
func SetNode(root *yaml.Node, path []string, value *yaml.Node) error {
	if root == nil || root.Kind != yaml.MappingNode {
		return fmt.Errorf("the config file's top level isn't a mapping")
	}
	n := root
	for i, p := range path {
		j := find(n, p)
		last := i == len(path)-1
		if last {
			nv := *value
			if j < 0 {
				n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p}, &nv)
				return nil
			}
			old := n.Content[j+1]
			nv.HeadComment, nv.LineComment, nv.FootComment = old.HeadComment, old.LineComment, old.FootComment
			if old.Kind == nv.Kind && (nv.Kind == yaml.SequenceNode || nv.Kind == yaml.MappingNode) {
				nv.Style = old.Style
			}
			if old.Kind == yaml.ScalarNode && nv.Kind == yaml.ScalarNode && nv.Tag == "!!str" &&
				(old.Style == yaml.DoubleQuotedStyle || old.Style == yaml.SingleQuotedStyle) {
				nv.Style = old.Style
			}
			n.Content[j+1] = &nv
			return nil
		}
		if j < 0 {
			child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p}, child)
			n = child
			continue
		}
		child := n.Content[j+1]
		if child.Kind != yaml.MappingNode {
			if child.Kind == yaml.ScalarNode && (child.Tag == "!!null" || child.Value == "") {
				// "auth:" with nothing under it.
				repl := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: child.HeadComment, LineComment: child.LineComment, FootComment: child.FootComment}
				n.Content[j+1] = repl
				child = repl
			} else {
				return fmt.Errorf("%s isn't a mapping in the config file (line %d)", strings.Join(path[:i+1], "."), child.Line)
			}
		}
		n = child
	}
	return nil
}

// Unset removes the key at path. Mappings it leaves empty go too, unless
// a comment hangs on them.
func Unset(root *yaml.Node, path []string) {
	unset(root, path)
}

func unset(n *yaml.Node, path []string) (empty bool) {
	j := find(n, path[0])
	if j < 0 {
		return false
	}
	if len(path) > 1 {
		child := n.Content[j+1]
		if !unset(child, path[1:]) {
			return false
		}
		k := n.Content[j]
		if k.HeadComment != "" || k.LineComment != "" || child.HeadComment != "" || child.LineComment != "" || child.FootComment != "" {
			return false
		}
	}
	k := n.Content[j]
	// A comment above the removed key (its documentation, often) stays:
	// on the next key of the mapping, else under the previous one.
	if k.HeadComment != "" {
		switch {
		case j+2 < len(n.Content):
			next := n.Content[j+2]
			next.HeadComment = strings.TrimSpace(k.HeadComment + "\n" + next.HeadComment)
		case j >= 2:
			prev := n.Content[j-2]
			prev.FootComment = strings.TrimSpace(prev.FootComment + "\n" + k.HeadComment)
		}
	}
	n.Content = append(n.Content[:j], n.Content[j+2:]...)
	return len(n.Content) == 0
}
