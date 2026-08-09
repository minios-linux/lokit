package config

import (
	"fmt"
	"path/filepath"

	"github.com/minios-linux/lokit/terminology"
	"gopkg.in/yaml.v3"
)

func loadTerminology(data []byte, configPath, configDir string) (*terminology.Catalog, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, nil // The regular config decoder reports syntax errors.
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return nil, nil
	}

	root := document.Content[0]
	var terminologyNode *yaml.Node
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value == "terminology" {
			terminologyNode = root.Content[i+1]
			break
		}
	}
	if terminologyNode == nil {
		return nil, nil
	}
	if terminologyNode.Kind != yaml.MappingNode {
		return nil, configNodeError(configPath, terminologyNode, "terminology must be an object")
	}

	var fromNode *yaml.Node
	seen := make(map[string]struct{})
	for i := 0; i < len(terminologyNode.Content); i += 2 {
		key, value := terminologyNode.Content[i], terminologyNode.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return nil, configNodeError(configPath, key, "terminology field names must be strings")
		}
		if _, exists := seen[key.Value]; exists {
			return nil, configNodeError(configPath, key, "duplicate terminology field %q", key.Value)
		}
		seen[key.Value] = struct{}{}
		if key.Value != "from" {
			return nil, configNodeError(configPath, key, "unknown terminology field %q", key.Value)
		}
		fromNode = value
	}
	if fromNode == nil {
		return nil, configNodeError(configPath, terminologyNode, "terminology.from is required")
	}
	if fromNode.Kind != yaml.SequenceNode || len(fromNode.Content) == 0 {
		return nil, configNodeError(configPath, fromNode, "terminology.from must be a non-empty string list")
	}

	paths := make([]string, 0, len(fromNode.Content))
	for _, item := range fromNode.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" || item.Value == "" {
			return nil, configNodeError(configPath, item, "terminology.from entries must be non-empty strings")
		}
		path := filepath.FromSlash(item.Value)
		if !filepath.IsAbs(path) {
			path = filepath.Join(configDir, path)
		}
		paths = append(paths, filepath.Clean(path))
	}
	catalog, err := terminology.LoadFiles(paths)
	if err != nil {
		return nil, fmt.Errorf("%s: loading terminology: %w", configPath, err)
	}
	return catalog, nil
}

func configNodeError(path string, node *yaml.Node, format string, args ...any) error {
	line, column := 1, 1
	if node != nil && node.Line > 0 {
		line, column = node.Line, node.Column
	}
	return fmt.Errorf("%s:%d:%d: %s", path, line, column, fmt.Sprintf(format, args...))
}
