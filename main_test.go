package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProgressBar(t *testing.T) {
	tests := []struct {
		name    string
		percent int
		width   int
		want    string
	}{
		{
			name:    "clamps below zero",
			percent: -10,
			width:   4,
			want:    colorRed + "░░░░" + colorReset + "   0%",
		},
		{
			name:    "mid range uses yellow",
			percent: 50,
			width:   4,
			want:    colorYellow + "██░░" + colorReset + "  50%",
		},
		{
			name:    "clamps above hundred",
			percent: 120,
			width:   4,
			want:    colorGreen + "████" + colorReset + " 100%",
		},
	}

	for _, tc := range tests {
		if got := progressBar(tc.percent, tc.width); got != tc.want {
			t.Fatalf("%s: progressBar() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestFlagFromRegion(t *testing.T) {
	if got := flagFromRegion("us"); got != "🇺🇸" {
		t.Fatalf("flagFromRegion(us) = %q, want %q", got, "🇺🇸")
	}
	if got := flagFromRegion("USA"); got != "" {
		t.Fatalf("flagFromRegion(USA) = %q, want empty", got)
	}
	if got := flagFromRegion("1A"); got != "" {
		t.Fatalf("flagFromRegion(1A) = %q, want empty", got)
	}
}

func TestLangHelpers(t *testing.T) {
	if got := langFlag("zz-BR"); got != "🇧🇷" {
		t.Fatalf("langFlag(zz-BR) = %q, want %q", got, "🇧🇷")
	}
	if got := langFlag("invalid"); got != "" {
		t.Fatalf("langFlag(invalid) = %q, want empty", got)
	}

	langs := []string{"en", "pt-BR", "zh-Hant"}
	if got := langColumnWidth(langs); got != len("zh-Hant") {
		t.Fatalf("langColumnWidth() = %d, want %d", got, len("zh-Hant"))
	}

	cell := langCell("zz-BR", 6)
	if !strings.Contains(cell, "🇧🇷") || !strings.Contains(cell, "zz-BR") {
		t.Fatalf("langCell() = %q, want flag and language code", cell)
	}
}

func TestIntersectLanguages(t *testing.T) {
	available := []string{"en", "fr", "de", "es"}
	filter := []string{" fr ", "es", "it"}
	want := []string{"fr", "es"}

	if got := intersectLanguages(available, filter); !reflect.DeepEqual(got, want) {
		t.Fatalf("intersectLanguages() = %#v, want %#v", got, want)
	}
}

func TestFilterOutLang(t *testing.T) {
	langs := []string{"en", "fr", "en", "de"}
	want := []string{"fr", "de"}

	if got := filterOutLang(langs, "en"); !reflect.DeepEqual(got, want) {
		t.Fatalf("filterOutLang() = %#v, want %#v", got, want)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("ok"), 0644); err != nil {
		t.Fatalf("os.WriteFile() error: %v", err)
	}

	if !fileExists(filePath) {
		t.Fatalf("fileExists(file) = false, want true")
	}
	if fileExists(dir) {
		t.Fatalf("fileExists(directory) = true, want false")
	}
	if fileExists(filepath.Join(dir, "missing.txt")) {
		t.Fatalf("fileExists(missing) = true, want false")
	}
}
