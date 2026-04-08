// Package chunk splits markdown content into overlapping text chunks for indexing.
package chunk

import (
	"strings"
	"unicode"
)

const (
	TokenTarget  = 512
	TokenOverlap = 64
)

// Chunk is one piece of a document ready for indexing/embedding.
type Chunk struct {
	Path       string
	ChunkIndex int
	Content    string
	TokenCount int
}

// Split splits content into overlapping chunks of ~512 tokens.
// Algorithm: split by double-newline (paragraphs); if a paragraph exceeds
// TokenTarget, split by sentence boundary; then apply sliding window
// with TokenOverlap.
func Split(content, path string) []Chunk {
	if strings.TrimSpace(content) == "" {
		return nil
	}

	paragraphs := strings.Split(content, "\n\n")
	var sentences []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if EstimateTokens(p) <= TokenTarget {
			sentences = append(sentences, p)
		} else {
			sentences = append(sentences, splitBySentence(p)...)
		}
	}

	if len(sentences) == 0 {
		return nil
	}

	var chunks []Chunk
	idx := 0
	start := 0
	for start < len(sentences) {
		var buf strings.Builder
		i := start
		for i < len(sentences) {
			next := sentences[i]
			candidate := buf.String()
			if candidate != "" {
				candidate += " " + next
			} else {
				candidate = next
			}
			if EstimateTokens(candidate) > TokenTarget && buf.Len() > 0 {
				break
			}
			if buf.Len() > 0 {
				buf.WriteString(" ")
			}
			buf.WriteString(next)
			i++
		}
		text := strings.TrimSpace(buf.String())
		chunks = append(chunks, Chunk{
			Path:       path,
			ChunkIndex: idx,
			Content:    text,
			TokenCount: EstimateTokens(text),
		})
		idx++

		// Overlap: backtrack ~TokenOverlap tokens
		overlapTokens := 0
		newStart := i
		for newStart > start && overlapTokens < TokenOverlap {
			newStart--
			overlapTokens += EstimateTokens(sentences[newStart])
		}
		if newStart <= start {
			newStart = i
		}
		start = newStart
	}
	return chunks
}

// EstimateTokens approximates the token count of text (1 token ≈ 4 chars).
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// splitBySentence splits text at sentence boundaries ('. ', '! ', '? ').
func splitBySentence(text string) []string {
	var sentences []string
	var buf strings.Builder
	runes := []rune(text)
	for i, r := range runes {
		buf.WriteRune(r)
		if (r == '.' || r == '!' || r == '?') && i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
			s := strings.TrimSpace(buf.String())
			if s != "" {
				sentences = append(sentences, s)
			}
			buf.Reset()
		}
	}
	if buf.Len() > 0 {
		s := strings.TrimSpace(buf.String())
		if s != "" {
			sentences = append(sentences, s)
		}
	}
	if len(sentences) == 0 {
		return []string{text}
	}
	return sentences
}
