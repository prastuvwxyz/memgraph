package parse

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test-file.md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestH1TitleExtraction(t *testing.T) {
	content := `# My Great Title

Some body text here.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if pf.Title != "My Great Title" {
		t.Errorf("expected title %q, got %q", "My Great Title", pf.Title)
	}
}

func TestH1TitleExtractionWithFrontmatter(t *testing.T) {
	content := `---
tags: [go, test]
---

# Frontmatter Title

Body text.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if pf.Title != "Frontmatter Title" {
		t.Errorf("expected title %q, got %q", "Frontmatter Title", pf.Title)
	}
}

func TestFilenameFallbackTitle(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "openclaw-setup-complete.md")
	if err := os.WriteFile(path, []byte("No heading here.\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	want := "Openclaw Setup Complete"
	if pf.Title != want {
		t.Errorf("expected fallback title %q, got %q", want, pf.Title)
	}
}

func TestFilenameFallbackTitleUnderscore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "my_knowledge_base.md")
	if err := os.WriteFile(path, []byte("just text\n"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	want := "My Knowledge Base"
	if pf.Title != want {
		t.Errorf("expected fallback title %q, got %q", want, pf.Title)
	}
}

func TestYAMLTagsListFormat(t *testing.T) {
	content := `---
title: Something
tags: [go, memory, graph]
---

Body here.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	want := []string{"go", "memory", "graph"}
	if len(pf.Tags) != len(want) {
		t.Fatalf("expected %d tags, got %d: %v", len(want), len(pf.Tags), pf.Tags)
	}
	for i, tag := range want {
		if pf.Tags[i] != tag {
			t.Errorf("tag[%d]: expected %q, got %q", i, tag, pf.Tags[i])
		}
	}
}

func TestYAMLTagsBlockFormat(t *testing.T) {
	content := `---
tags:
  - alpha
  - beta
  - gamma
---

Body content.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if len(pf.Tags) != len(want) {
		t.Fatalf("expected %d tags, got %d: %v", len(want), len(pf.Tags), pf.Tags)
	}
	for i, tag := range want {
		if pf.Tags[i] != tag {
			t.Errorf("tag[%d]: expected %q, got %q", i, tag, pf.Tags[i])
		}
	}
}

func TestNoFrontmatter(t *testing.T) {
	content := `# No Frontmatter

Just a plain markdown file.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(pf.Tags) != 0 {
		t.Errorf("expected no tags, got %v", pf.Tags)
	}
	if pf.Title != "No Frontmatter" {
		t.Errorf("expected title %q, got %q", "No Frontmatter", pf.Title)
	}
}

func TestWikilinkExtraction(t *testing.T) {
	content := `# Notes

See [[Project Alpha]] and [[Beta Notes|Beta]] for details.
Also refer to [[gamma-doc]].
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	wantLinks := map[string]bool{
		"Project Alpha": true,
		"Beta Notes":    true,
		"gamma-doc":     true,
	}
	for _, link := range pf.Links {
		if !wantLinks[link] {
			t.Errorf("unexpected link: %q", link)
		}
		delete(wantLinks, link)
	}
	for missing := range wantLinks {
		t.Errorf("missing expected link: %q", missing)
	}
}

func TestRelativeMarkdownLinkExtraction(t *testing.T) {
	content := `# Links

Check [this file](./notes/something.md) and [another](../other.md).
Skip [external](https://example.com) and [http link](http://foo.bar/page).
Also [no-prefix](relative/path.md).
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}

	wantLinks := map[string]bool{
		"./notes/something.md": true,
		"../other.md":          true,
		"relative/path.md":     true,
	}
	for _, link := range pf.Links {
		if link == "https://example.com" || link == "http://foo.bar/page" {
			t.Errorf("should not include absolute URL: %q", link)
		}
		delete(wantLinks, link)
	}
	for missing := range wantLinks {
		t.Errorf("missing expected link: %q", missing)
	}
}

func TestBodyStripping(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "headings removed",
			input: "# Title\n## Subtitle\nBody text.",
			want:  "Title\nSubtitle\nBody text.",
		},
		{
			name:  "bold stripped",
			input: "This is **bold** and __also bold__.",
			want:  "This is bold and also bold.",
		},
		{
			name:  "italic stripped",
			input: "This is *italic* and _also italic_.",
			want:  "This is italic and also italic.",
		},
		{
			name:  "inline code stripped",
			input: "Use `fmt.Println` to print.",
			want:  "Use  to print.",
		},
		{
			name:  "link text kept",
			input: "See [the docs](https://example.com) for info.",
			want:  "See the docs for info.",
		},
		{
			name:  "html comment removed",
			input: "Before <!-- a comment --> after.",
			want:  "Before  after.",
		},
		{
			name:  "wikilink replaced with target",
			input: "See [[My Note]] here.",
			want:  "See My Note here.",
		},
		{
			name:  "wikilink alias used",
			input: "See [[My Note|the note]] here.",
			want:  "See the note here.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StripMarkdown(tt.input)
			if got != tt.want {
				t.Errorf("StripMarkdown(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBodyMaxLength(t *testing.T) {
	// Create content with more than 2000 chars
	long := ""
	for i := 0; i < 100; i++ {
		long += "This is a line of text that is moderately long. "
	}
	content := "# Title\n\n" + long
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if len(pf.Body) > 2000 {
		t.Errorf("body exceeds 2000 chars: got %d", len(pf.Body))
	}
}

func TestChecksumConsistency(t *testing.T) {
	content := `# Test File

Some content here.
`
	path := writeTempFile(t, content)

	pf1, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile (1): %v", err)
	}
	pf2, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile (2): %v", err)
	}
	if pf1.Checksum != pf2.Checksum {
		t.Errorf("checksums differ on identical file: %q vs %q", pf1.Checksum, pf2.Checksum)
	}

	// Verify against manually computed sha256
	raw, _ := os.ReadFile(path)
	expected := fmt.Sprintf("%x", sha256.Sum256(raw))
	if pf1.Checksum != expected {
		t.Errorf("checksum mismatch: got %q, want %q", pf1.Checksum, expected)
	}
}

func TestChecksumChangesWithContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "changing.md")

	if err := os.WriteFile(path, []byte("version 1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pf1, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("version 2\n"), 0644); err != nil {
		t.Fatal(err)
	}
	pf2, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if pf1.Checksum == pf2.Checksum {
		t.Error("checksums should differ when file content changes")
	}
}

func TestLastVerified(t *testing.T) {
	content := `# My Doc

<!-- last-verified: 2026-03-15 -->

Some content.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if pf.LastVerified != "2026-03-15" {
		t.Errorf("expected LastVerified %q, got %q", "2026-03-15", pf.LastVerified)
	}
}

func TestBodyExcludesFrontmatter(t *testing.T) {
	content := `---
tags: [a, b]
---

# Title

Actual body content.
`
	path := writeTempFile(t, content)
	pf, err := ParseFile(path)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if contains(pf.Body, "tags:") {
		t.Errorf("body should not contain frontmatter, got: %q", pf.Body)
	}
	if !contains(pf.Body, "Actual body content") {
		t.Errorf("body should contain actual content, got: %q", pf.Body)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
