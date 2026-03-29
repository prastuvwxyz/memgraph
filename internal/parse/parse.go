package parse

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// ParsedFile holds structured data extracted from a markdown file.
type ParsedFile struct {
	Path         string   // relative path from root
	Title        string   // H1 heading or filename without ext
	Tags         []string // from YAML frontmatter "tags:" field
	Body         string   // markdown body stripped to plain text (max 2000 chars)
	Links        []string // outbound links: [[wikilinks]] + [text](path) targets
	Checksum     string   // sha256 hex of raw file content
	LastVerified string   // from <!-- last-verified: YYYY-MM-DD --> comment
}

var (
	reH1          = regexp.MustCompile(`(?m)^#\s+(.+)$`)
	reWikilink    = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	reMdLink      = regexp.MustCompile(`\[(?:[^\]]*)\]\(([^)]+)\)`)
	reLastVerified = regexp.MustCompile(`<!--\s*last-verified:\s*(\d{4}-\d{2}-\d{2})\s*-->`)
)

// ParseFile reads a markdown file and returns structured ParsedFile data.
func ParseFile(path string) (*ParsedFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	content := string(raw)

	checksum := fmt.Sprintf("%x", sha256.Sum256(raw))

	tags, bodyContent := extractFrontmatter(content)

	title := extractTitle(bodyContent)
	if title == "" {
		title = filenameToTitle(filepath.Base(path))
	}

	lastVerified := extractLastVerified(content)

	body := StripMarkdown(bodyContent)
	if len(body) > 2000 {
		body = body[:2000]
	}

	links := extractLinks(content)

	return &ParsedFile{
		Path:         path,
		Title:        title,
		Tags:         tags,
		Body:         body,
		Links:        links,
		Checksum:     checksum,
		LastVerified: lastVerified,
	}, nil
}

// extractFrontmatter parses YAML frontmatter between --- delimiters at top of file.
// Returns tags and the body content (after frontmatter).
func extractFrontmatter(content string) (tags []string, body string) {
	if !strings.HasPrefix(content, "---") {
		return nil, content
	}

	// Find closing ---
	rest := content[3:]
	// skip optional newline after opening ---
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	}

	end := strings.Index(rest, "\n---")
	if end == -1 {
		return nil, content
	}

	frontmatter := rest[:end]
	body = rest[end+4:] // skip \n---
	// skip optional newline after closing ---
	if strings.HasPrefix(body, "\n") {
		body = body[1:]
	} else if strings.HasPrefix(body, "\r\n") {
		body = body[2:]
	}

	tags = parseTags(frontmatter)
	return tags, body
}

// parseTags extracts tags from a YAML frontmatter string.
// Supports both:
//   tags: [a, b, c]
//   tags:
//     - a
//     - b
func parseTags(fm string) []string {
	lines := strings.Split(fm, "\n")

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "tags:") {
			continue
		}

		value := strings.TrimSpace(strings.TrimPrefix(trimmed, "tags:"))

		// Inline list format: tags: [a, b, c]
		if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
			inner := value[1 : len(value)-1]
			return splitTags(inner)
		}

		// Inline single value: tags: sometag
		if value != "" {
			return []string{strings.TrimSpace(value)}
		}

		// Block format: next lines are "  - tag"
		var tags []string
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			stripped := strings.TrimSpace(l)
			if strings.HasPrefix(stripped, "- ") {
				tag := strings.TrimSpace(strings.TrimPrefix(stripped, "- "))
				if tag != "" {
					tags = append(tags, tag)
				}
			} else if stripped == "" {
				continue
			} else {
				break
			}
		}
		return tags
	}

	return nil
}

func splitTags(s string) []string {
	parts := strings.Split(s, ",")
	var tags []string
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// extractTitle returns the first H1 heading text from markdown content.
func extractTitle(content string) string {
	m := reH1.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// filenameToTitle converts a filename (without extension) to a title.
// e.g. "openclaw-setup-complete" → "OpenClaw Setup Complete"
func filenameToTitle(filename string) string {
	name := strings.TrimSuffix(filename, filepath.Ext(filename))
	// Replace - and _ with spaces
	name = strings.NewReplacer("-", " ", "_", " ").Replace(name)
	// Title case
	return titleCase(name)
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) == 0 {
			continue
		}
		runes := []rune(w)
		runes[0] = unicode.ToUpper(runes[0])
		words[i] = string(runes)
	}
	return strings.Join(words, " ")
}

// extractLastVerified finds <!-- last-verified: YYYY-MM-DD --> in content.
func extractLastVerified(content string) string {
	m := reLastVerified.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	return m[1]
}

// StripMarkdown removes common markdown syntax and returns plain text.
func StripMarkdown(s string) string {
	// Remove HTML comments
	reHTMLComment := regexp.MustCompile(`<!--.*?-->`)
	s = reHTMLComment.ReplaceAllString(s, "")

	// Remove fenced code blocks (``` or ~~~)
	reCodeFence := regexp.MustCompile("(?ms)^```.*?^```\\s*$")
	s = reCodeFence.ReplaceAllString(s, "")
	reCodeFence2 := regexp.MustCompile("(?ms)^~~~.*?^~~~\\s*$")
	s = reCodeFence2.ReplaceAllString(s, "")

	// Remove inline code
	reInlineCode := regexp.MustCompile("`[^`]*`")
	s = reInlineCode.ReplaceAllString(s, "")

	// Remove headings markers (keep text)
	reHeading := regexp.MustCompile(`(?m)^#{1,6}\s+`)
	s = reHeading.ReplaceAllString(s, "")

	// Remove bold/italic: **text** __text__ *text* _text_
	reBoldItalic := regexp.MustCompile(`\*{1,3}([^*]+)\*{1,3}`)
	s = reBoldItalic.ReplaceAllString(s, "$1")
	reUnderline := regexp.MustCompile(`_{1,3}([^_]+)_{1,3}`)
	s = reUnderline.ReplaceAllString(s, "$1")

	// Replace markdown links [text](url) with text
	reMdLinkStrip := regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	s = reMdLinkStrip.ReplaceAllString(s, "$1")

	// Replace wikilinks [[target|text]] or [[target]] with text/target
	reWikilinkStrip := regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]*))?\]\]`)
	s = reWikilinkStrip.ReplaceAllStringFunc(s, func(match string) string {
		m := reWikilinkStrip.FindStringSubmatch(match)
		if len(m) >= 3 && m[2] != "" {
			return m[2]
		}
		if len(m) >= 2 {
			return m[1]
		}
		return match
	})

	// Remove image syntax ![alt](url)
	reImage := regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	s = reImage.ReplaceAllString(s, "")

	// Remove blockquote markers
	reBlockquote := regexp.MustCompile(`(?m)^>\s?`)
	s = reBlockquote.ReplaceAllString(s, "")

	// Remove horizontal rules
	reHR := regexp.MustCompile(`(?m)^[-*_]{3,}\s*$`)
	s = reHR.ReplaceAllString(s, "")

	// Collapse multiple blank lines
	reMultiBlank := regexp.MustCompile(`\n{3,}`)
	s = reMultiBlank.ReplaceAllString(s, "\n\n")

	return strings.TrimSpace(s)
}

// reCodeFenceStrip matches fenced code blocks (``` or ~~~), including indented ones.
var reCodeFenceStrip = regexp.MustCompile("(?ms)^\\s*```.*?^\\s*```\\s*$|(?ms)^\\s*~~~.*?^\\s*~~~\\s*$")

// reInlineCodeStrip matches inline code spans.
var reInlineCodeStrip = regexp.MustCompile("`[^`\n]+`")

// stripCodeForLinks removes fenced and inline code so link regexes
// don't match template placeholders inside code blocks.
func stripCodeForLinks(content string) string {
	s := reCodeFenceStrip.ReplaceAllString(content, "")
	return reInlineCodeStrip.ReplaceAllString(s, "")
}

// looksLikeFilePath returns true if target looks like a relative file path
// rather than a bare word placeholder (e.g. "url", "ctx", "fake-url").
// A valid relative link must contain a slash, start with ./ or ../, or carry a file extension.
func looksLikeFilePath(target string) bool {
	return strings.Contains(target, "/") ||
		strings.HasPrefix(target, "./") ||
		strings.HasPrefix(target, "../") ||
		strings.Contains(target, ".")
}

// extractLinks returns all outbound links from markdown content.
// Includes [[wikilinks]] and [text](relative/path).
// Skips http/https URLs, absolute paths (/...), and links inside code blocks.
func extractLinks(content string) []string {
	// Strip code blocks/spans first to avoid capturing template placeholders.
	stripped := stripCodeForLinks(content)

	seen := make(map[string]bool)
	var links []string

	// Wikilinks: [[target]] or [[target|alias]]
	for _, m := range reWikilink.FindAllStringSubmatch(stripped, -1) {
		raw := m[1]
		// strip alias part after |
		if idx := strings.Index(raw, "|"); idx != -1 {
			raw = raw[:idx]
		}
		target := strings.TrimSpace(raw)
		if target != "" && !seen[target] {
			seen[target] = true
			links = append(links, target)
		}
	}

	// Markdown links: [text](path)
	for _, m := range reMdLink.FindAllStringSubmatch(stripped, -1) {
		target := strings.TrimSpace(m[1])
		// Skip absolute URLs and absolute paths
		if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "/") {
			continue
		}
		// Strip anchor fragment
		if idx := strings.Index(target, "#"); idx != -1 {
			target = target[:idx]
		}
		// Only keep targets that look like relative file paths:
		// must contain a slash, start with ./ or ../, or end in a known extension.
		if !looksLikeFilePath(target) {
			continue
		}
		if target != "" && !seen[target] {
			seen[target] = true
			links = append(links, target)
		}
	}

	return links
}
