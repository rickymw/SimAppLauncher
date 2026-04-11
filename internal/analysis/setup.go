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
	idx := strings.Index(yaml, "\nCarSetup:\n")
	if idx < 0 {
		if strings.HasPrefix(yaml, "CarSetup:\n") {
			idx = -1 // so idx+1 == 0
		} else {
			return nil
		}
	}
	// Extract lines belonging to the CarSetup block.
	block := yaml[idx+1:]
	lines := strings.Split(block, "\n")

	// Skip the "CarSetup:" header line.
	lines = lines[1:]

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
func FindChild(nodes []SetupNode, key string) *SetupNode {
	for i := range nodes {
		if nodes[i].Key == key {
			return &nodes[i]
		}
	}
	return nil
}
