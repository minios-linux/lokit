package i18next

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RecipeTranslation represents a per-recipe translation file.
type RecipeTranslation struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	LongDescription string `json:"longDescription"`
}

// ParseRecipeFile reads a recipe translation JSON file.
func ParseRecipeFile(path string) (*RecipeTranslation, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var rt RecipeTranslation
	if err := json.Unmarshal(data, &rt); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	return &rt, nil
}

// WriteRecipeFile writes a recipe translation JSON file.
func (rt *RecipeTranslation) WriteRecipeFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	data, err := json.MarshalIndent(rt, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

// IsTranslated returns true if at least name and description are non-empty.
func (rt *RecipeTranslation) IsTranslated() bool {
	return rt.Name != "" && rt.Description != ""
}

// IsFullyTranslated returns true if all three fields are non-empty.
func (rt *RecipeTranslation) IsFullyTranslated() bool {
	return rt.Name != "" && rt.Description != "" && rt.LongDescription != ""
}
