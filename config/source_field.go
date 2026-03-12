package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// SourceField supports two YAML forms for target source:
//
//  1. String path template:
//     source: "{lang}.json"
//
//  2. Index source configuration:
//     source:
//     index: "recipes.json"
//     records_path: "$"
//     key_field: "id"
//     fields: ["name", "description"]
type SourceField struct {
	Path        string
	Index       string   `yaml:"index,omitempty"`
	RecordsPath string   `yaml:"records_path,omitempty"`
	KeyField    string   `yaml:"key_field,omitempty"`
	Fields      []string `yaml:"fields,omitempty"`
}

func (s *SourceField) UnmarshalYAML(value *yaml.Node) error {
	if s == nil {
		return nil
	}

	s.Path = ""
	s.Index = ""
	s.RecordsPath = ""
	s.KeyField = ""
	s.Fields = nil

	switch value.Kind {
	case yaml.ScalarNode:
		var path string
		if err := value.Decode(&path); err != nil {
			return err
		}
		s.Path = path
		return nil
	case yaml.MappingNode:
		type sourceObject struct {
			Index       string   `yaml:"index,omitempty"`
			RecordsPath string   `yaml:"records_path,omitempty"`
			KeyField    string   `yaml:"key_field,omitempty"`
			Fields      []string `yaml:"fields,omitempty"`
		}
		var obj sourceObject
		if err := value.Decode(&obj); err != nil {
			return err
		}
		s.Index = obj.Index
		s.RecordsPath = obj.RecordsPath
		s.KeyField = obj.KeyField
		s.Fields = obj.Fields
		return nil
	default:
		return fmt.Errorf("source must be either a string or an object")
	}
}

func (s *SourceField) IsPath() bool {
	return s != nil && s.Path != ""
}

func (s *SourceField) IsIndex() bool {
	return s != nil && s.Index != ""
}
