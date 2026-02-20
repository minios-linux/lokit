package i18next

import (
	"path/filepath"
	"testing"
)

func TestRecipeTranslation_ReadWriteAndState(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "fr", "recipe.json")

	rt := &RecipeTranslation{
		Name:            "Pain",
		Description:     "Description francaise",
		LongDescription: "Longue description",
	}

	if err := rt.WriteRecipeFile(path); err != nil {
		t.Fatalf("WriteRecipeFile error: %v", err)
	}

	got, err := ParseRecipeFile(path)
	if err != nil {
		t.Fatalf("ParseRecipeFile error: %v", err)
	}

	if got.Name != rt.Name || got.Description != rt.Description || got.LongDescription != rt.LongDescription {
		t.Fatalf("parsed recipe mismatch: got=%+v want=%+v", got, rt)
	}

	if !got.IsTranslated() {
		t.Fatal("expected IsTranslated=true")
	}
	if !got.IsFullyTranslated() {
		t.Fatal("expected IsFullyTranslated=true")
	}

	got.LongDescription = ""
	if !got.IsTranslated() {
		t.Fatal("expected IsTranslated=true when long description is empty")
	}
	if got.IsFullyTranslated() {
		t.Fatal("expected IsFullyTranslated=false when long description is empty")
	}
}
