// Package mdfile tests.
package mdfile

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Parse tests
// ---------------------------------------------------------------------------

func TestParse_PlainBody(t *testing.T) {
	data := []byte("Hello world\n\nThis is a paragraph.\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse error: %v", err)
	}
	keys := f.Keys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 segment, got %d: %v", len(keys), keys)
	}
	val, _ := f.Get("sec:0")
	if val == "" {
		t.Error("expected non-empty sec:0")
	}
}

func TestParse_WithHeadings(t *testing.T) {
	data := []byte(`# Title

Intro paragraph.

## Section A

Content A.

## Section B

Content B.
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	keys := f.Keys()
	// Expect 3 sections: "# Title\n\nIntro", "## Section A\n\nContent A", "## Section B\n\nContent B"
	if len(keys) != 3 {
		t.Fatalf("expected 3 segments, got %d: %v", len(keys), keys)
	}
	sec0, _ := f.Get("sec:0")
	if !strings.Contains(sec0, "# Title") {
		t.Errorf("sec:0 should contain '# Title', got: %q", sec0)
	}
}

func TestParse_FrontmatterOnly(t *testing.T) {
	data := []byte(`---
title: Hello World
description: A sample post
---

# Body
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if !f.hasFrontmatter {
		t.Error("expected hasFrontmatter=true")
	}
	title, ok := f.Get("fm:title")
	if !ok || title != "Hello World" {
		t.Errorf("fm:title: want 'Hello World', got %q (ok=%v)", title, ok)
	}
	desc, ok := f.Get("fm:description")
	if !ok || desc != "A sample post" {
		t.Errorf("fm:description: want 'A sample post', got %q", desc)
	}
}

func TestParse_FrontmatterAndBody(t *testing.T) {
	data := []byte(`---
title: My Post
---

# Introduction

Some intro text.
`)
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	keys := f.Keys()
	// Expect: fm:title + sec:0 (# Introduction + text)
	if len(keys) != 2 {
		t.Fatalf("expected 2 segments, got %d: %v", len(keys), keys)
	}
}

func TestParse_EmptyFile(t *testing.T) {
	f, err := Parse([]byte(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Keys()) != 0 {
		t.Errorf("expected 0 segments, got %d", len(f.Keys()))
	}
}

func TestParse_CodeBlockNotSplit(t *testing.T) {
	// Code blocks should not be treated as section separators.
	data := []byte("# Title\n\nSome text.\n\n```bash\n# this is a comment\necho hello\n```\n\nMore text.\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	// Should be 1 section: the heading + everything after it.
	keys := f.Keys()
	if len(keys) != 1 {
		t.Fatalf("expected 1 section (code block not split), got %d: %v", len(keys), keys)
	}
	sec0, _ := f.Get("sec:0")
	if !strings.Contains(sec0, "```bash") {
		t.Errorf("sec:0 should contain the code block, got: %q", sec0)
	}
}

// ---------------------------------------------------------------------------
// Stats / UntranslatedKeys
// ---------------------------------------------------------------------------

func TestStats_Empty(t *testing.T) {
	f, _ := Parse([]byte("# Title\n"))
	// After parse the section has the value "# Title", so it's "translated" (non-empty).
	total, _, _ := f.Stats()
	if total != 1 {
		t.Errorf("expected total=1, got %d", total)
	}
}

func TestUntranslatedKeys_AfterNewTranslationFile(t *testing.T) {
	src, _ := Parse([]byte("---\ntitle: Hello\n---\n\n# Body\n\nText here.\n"))
	target := NewTranslationFile(src)
	untr := target.UntranslatedKeys()
	if len(untr) != len(src.Keys()) {
		t.Errorf("expected all %d keys untranslated, got %d: %v", len(src.Keys()), len(untr), untr)
	}
}

// ---------------------------------------------------------------------------
// Set / Marshal round-trip
// ---------------------------------------------------------------------------

func TestSet_AndMarshal(t *testing.T) {
	src := []byte("# Title\n\nSome text.\n")
	f, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}

	f.Set("sec:0", "# Заголовок\n\nНемного текста.")

	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Заголовок") {
		t.Errorf("output should contain translated heading, got: %q", string(out))
	}
}

func TestMarshal_FrontmatterPreserved(t *testing.T) {
	data := []byte("---\ntitle: Hello\n---\n\n# Body\n\nText.\n")
	f, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	f.Set("fm:title", "Привет")

	out, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	outStr := string(out)
	if !strings.Contains(outStr, "---") {
		t.Error("output should have front matter delimiters")
	}
	if !strings.Contains(outStr, "Привет") {
		t.Errorf("output should contain translated title, got: %q", outStr)
	}
}

// ---------------------------------------------------------------------------
// NewTranslationFile / SyncKeys
// ---------------------------------------------------------------------------

func TestNewTranslationFile_ClearsValues(t *testing.T) {
	src, _ := Parse([]byte("# Hello\n\nWorld.\n"))
	target := NewTranslationFile(src)
	for _, key := range target.Keys() {
		val, _ := target.Get(key)
		if val != "" {
			t.Errorf("key %q should be empty after NewTranslationFile, got %q", key, val)
		}
	}
}

func TestSyncKeys_AddsNew(t *testing.T) {
	src, _ := Parse([]byte("# Section 1\n\nText 1.\n\n## Section 2\n\nText 2.\n"))
	target, _ := Parse([]byte("# Section 1\n\nTranslated 1.\n"))
	// target has only 1 section; src has 2.
	SyncKeys(src, target)
	if len(target.Keys()) != len(src.Keys()) {
		t.Errorf("after SyncKeys, expected %d keys, got %d", len(src.Keys()), len(target.Keys()))
	}
}
