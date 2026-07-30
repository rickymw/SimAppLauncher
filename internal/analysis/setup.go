package analysis

import "strings"

// SetupNode represents a key in the CarSetup YAML tree.
// Leaf nodes have Value set; branch nodes have Children.
type SetupNode struct {
	Key      string
	Value    string      // non-empty for leaf nodes
	Children []SetupNode // non-empty for branch nodes
}

// ParseCarSetupTree parses the CarSetup YAML block into a tree of SetupNodes.
// Returns nil if no CarSetup section is found.
func ParseCarSetupTree(yaml string) []SetupNode {
	block := ExtractCarSetupBlock(yaml)
	if block == "" {
		return nil
	}
	lines := strings.Split(block, "\n")
	// Skip the "CarSetup:" header line.
	if len(lines) > 0 {
		lines = lines[1:]
	}
	// Collect only indented lines (stop at next top-level key).
	var indented []string
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
		indented = append(indented, line)
	}

	nodes, _ := parseNodes(indented, 0)
	return nodes
}

// ExtractCarSetupBlock returns the "CarSetup:" section of a session YAML
// document as a standalone string starting with the "CarSetup:" header line
// and ending just before the next top-level key. Returns "" if there is no
// CarSetup section. The returned block is a valid input for ParseCarSetupTree.
func ExtractCarSetupBlock(yaml string) string {
	idx := strings.Index(yaml, "\nCarSetup:\n")
	if idx < 0 {
		if !strings.HasPrefix(yaml, "CarSetup:\n") {
			return ""
		}
		idx = -1 // so idx+1 == 0
	}
	block := yaml[idx+1:]
	// Trim trailing top-level keys: keep the header plus only indented lines.
	lines := strings.Split(block, "\n")
	end := 1 // include the header
	for ; end < len(lines); end++ {
		line := lines[end]
		if len(line) == 0 {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			break
		}
	}
	return strings.Join(lines[:end], "\n")
}

// parseNodes recursively parses indented lines into SetupNodes.
// baseIndent is the minimum indentation for this level.
// Returns the parsed nodes and how many lines were consumed.
func parseNodes(lines []string, baseIndent int) ([]SetupNode, int) {
	var nodes []SetupNode
	i := 0
	for i < len(lines) {
		line := lines[i]
		indent := countIndent(line)
		if indent < baseIndent {
			break
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			i++
			continue
		}

		colonIdx := strings.Index(trimmed, ":")
		if colonIdx < 0 {
			i++
			continue
		}

		key := trimmed[:colonIdx]
		rest := strings.TrimSpace(trimmed[colonIdx+1:])

		if rest != "" {
			// Leaf node: "Key: Value"
			nodes = append(nodes, SetupNode{Key: key, Value: rest})
			i++
		} else {
			// Branch node: "Key:" followed by indented children.
			i++
			childIndent := indent + 1
			if i < len(lines) {
				childIndent = countIndent(lines[i])
				if childIndent <= indent {
					// No children — treat as empty branch.
					nodes = append(nodes, SetupNode{Key: key})
					continue
				}
			}
			children, consumed := parseNodes(lines[i:], childIndent)
			nodes = append(nodes, SetupNode{Key: key, Children: children})
			i += consumed
		}
	}
	return nodes, i
}

func countIndent(s string) int {
	n := 0
	for _, c := range s {
		if c == ' ' {
			n++
		} else if c == '\t' {
			n += 4
		} else {
			break
		}
	}
	return n
}

// FindChild returns the child node with the given key, or nil.
// SetupValue is one leaf of a setup tree, addressed by its full path from the
// root (e.g. "Chassis/LeftFront/Camber").
type SetupValue struct {
	Path  string
	Value string
}

// FlattenSetup walks a setup tree depth-first and returns every leaf in
// document order. Order is preserved rather than sorted so a diff reads in the
// same sequence as the garage screen.
func FlattenSetup(nodes []SetupNode) []SetupValue {
	var out []SetupValue
	var walk func(prefix string, ns []SetupNode)
	walk = func(prefix string, ns []SetupNode) {
		for _, n := range ns {
			path := n.Key
			if prefix != "" {
				path = prefix + "/" + n.Key
			}
			if len(n.Children) > 0 {
				walk(path, n.Children)
				continue
			}
			out = append(out, SetupValue{Path: path, Value: n.Value})
		}
	}
	walk("", nodes)
	return out
}

// sessionStateSetupKeys are leaf names inside iRacing's CarSetup block that
// record what happened during the session rather than what the driver set.
//
// iRacing writes end-of-session tyre readings back into the setup block, so
// they differ between any two sessions no matter what — a raw diff of two
// identical setups still reports every corner's pressure, temperature and
// tread. Filtering them is what makes the diff readable: the handful of genuine
// changes are otherwise buried under a dozen guaranteed ones.
var sessionStateSetupKeys = map[string]bool{
	"UpdateCount":     true,
	"LastHotPressure": true,
	"LastTemps":       true,
	"LastTempsOMI":    true,
	"LastTempsIMO":    true,
	"TreadRemaining":  true,
}

// IsSessionState reports whether a flattened setup path names a session-state
// reading rather than an adjustable setting.
func IsSessionState(path string) bool {
	leaf := path
	if i := strings.LastIndex(path, "/"); i >= 0 {
		leaf = path[i+1:]
	}
	return sessionStateSetupKeys[leaf]
}

// FilterSessionState splits a diff into adjustable settings and the count of
// session-state readings that were dropped. The count is returned rather than
// discarded so callers can say how much was hidden.
func FilterSessionState(diff []SetupDiffEntry) (kept []SetupDiffEntry, hidden int) {
	for _, d := range diff {
		if IsSessionState(d.Path) {
			hidden++
			continue
		}
		kept = append(kept, d)
	}
	return kept, hidden
}

// SetupDiffEntry is one difference between two setups. An empty Old means the
// field is only present in the new setup, and an empty New means it was dropped
// — which normally signals a different car rather than a changed setting.
type SetupDiffEntry struct {
	Path string
	Old  string
	New  string
}

// DiffSetups compares two flattened setups and returns the fields that differ,
// in the order they appear in newer.
//
// Fields present in only one side are reported with the missing side empty
// rather than skipped: a setup that has gained or lost a field is usually a
// different car or a changed iRacing build, and silently hiding that would make
// the diff look like a small tweak.
func DiffSetups(older, newer []SetupValue) []SetupDiffEntry {
	oldByPath := make(map[string]string, len(older))
	for _, v := range older {
		oldByPath[v.Path] = v.Value
	}
	seen := make(map[string]bool, len(newer))

	var out []SetupDiffEntry
	for _, v := range newer {
		seen[v.Path] = true
		prev, ok := oldByPath[v.Path]
		if ok && prev == v.Value {
			continue
		}
		out = append(out, SetupDiffEntry{Path: v.Path, Old: prev, New: v.Value})
	}
	for _, v := range older {
		if !seen[v.Path] {
			out = append(out, SetupDiffEntry{Path: v.Path, Old: v.Value})
		}
	}
	return out
}

func FindChild(nodes []SetupNode, key string) *SetupNode {
	for i := range nodes {
		if nodes[i].Key == key {
			return &nodes[i]
		}
	}
	return nil
}
