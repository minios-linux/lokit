package i18next

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseBlogPostData_ParsesFrontmatterAndBody(t *testing.T) {
	data := []byte(`---
title: Test title
excerpt: >-
  First paragraph

  Second paragraph
author: alice
publishedAt: '2026-01-01'
updatedAt: '2026-01-02'
tags:
  - one
  - two
featuredImage: '/img/test.png'
published: false
order: 7
telegramDiscussion: 'https://example.test/discuss'
telegramPostId: 42
---

# Heading

Body text.
`)

	bp, err := ParseBlogPostData(data)
	if err != nil {
		t.Fatalf("ParseBlogPostData error: %v", err)
	}

	if bp.Title != "Test title" {
		t.Fatalf("unexpected title: %q", bp.Title)
	}
	if !strings.Contains(bp.Excerpt, "First paragraph") || !strings.Contains(bp.Excerpt, "Second paragraph") {
		t.Fatalf("unexpected excerpt: %q", bp.Excerpt)
	}
	if bp.Author != "alice" || bp.PublishedAt != "2026-01-01" || bp.UpdatedAt != "2026-01-02" {
		t.Fatalf("unexpected metadata fields: %+v", bp)
	}
	if len(bp.Tags) != 2 || bp.Tags[0] != "one" || bp.Tags[1] != "two" {
		t.Fatalf("unexpected tags: %v", bp.Tags)
	}
	if bp.Published {
		t.Fatal("expected published=false")
	}
	if bp.Order != 7 || bp.TelegramPostId != 42 {
		t.Fatalf("unexpected numeric fields: order=%d telegramPostId=%d", bp.Order, bp.TelegramPostId)
	}
	if !strings.Contains(bp.Content, "# Heading") {
		t.Fatalf("unexpected content: %q", bp.Content)
	}
}

func TestBlogHelpers_SlugsLangsAndPath(t *testing.T) {
	tmp := t.TempDir()
	postsDir := filepath.Join(tmp, "posts")
	translationsDir := filepath.Join(postsDir, "translations")

	if err := os.MkdirAll(translationsDir, 0755); err != nil {
		t.Fatalf("mkdir error: %v", err)
	}

	mustWrite := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	mustWrite(filepath.Join(postsDir, "alpha.md"), "# alpha\n")
	mustWrite(filepath.Join(postsDir, "beta.md"), "# beta\n")
	mustWrite(filepath.Join(postsDir, "ignore.txt"), "x")
	mustWrite(filepath.Join(translationsDir, "alpha.ru.md"), "# ru\n")
	mustWrite(filepath.Join(translationsDir, "alpha.fr.md"), "# fr\n")
	mustWrite(filepath.Join(translationsDir, "beta.ru.md"), "# ru\n")

	slugs, err := BlogPostSlugs(postsDir)
	if err != nil {
		t.Fatalf("BlogPostSlugs error: %v", err)
	}
	if len(slugs) != 2 || slugs[0] != "alpha" || slugs[1] != "beta" {
		t.Fatalf("unexpected slugs: %v", slugs)
	}

	langs := BlogTranslationLangs(postsDir, "alpha")
	if len(langs) != 2 || langs[0] != "fr" || langs[1] != "ru" {
		t.Fatalf("unexpected langs for alpha: %v", langs)
	}

	path := BlogTranslationPath(postsDir, "alpha", "de")
	if !strings.HasSuffix(path, filepath.Join("translations", "alpha.de.md")) {
		t.Fatalf("unexpected translation path: %s", path)
	}
}
