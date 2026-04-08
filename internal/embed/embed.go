// Package embed provides vector embedding for semantic search.
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Embedder generates vector embeddings for a batch of texts.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Dimensions() int
	ModelName() string
}

// EncodeEmbedding serialises a float32 slice to JSON bytes for SQLite storage.
func EncodeEmbedding(v []float32) ([]byte, error) {
	return json.Marshal(v)
}

// DecodeEmbedding deserialises a JSON blob back to float32 slice.
func DecodeEmbedding(b []byte) ([]float32, error) {
	var v []float32
	return v, json.Unmarshal(b, &v)
}

// --- OpenAI ---

type openAIEmbedder struct {
	apiKey  string
	baseURL string
}

// NewOpenAI creates an embedder backed by OpenAI text-embedding-3-small.
// baseURL defaults to https://api.openai.com if empty.
func NewOpenAI(apiKey, baseURL string) Embedder {
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	return &openAIEmbedder{apiKey: apiKey, baseURL: baseURL}
}

func (e *openAIEmbedder) Dimensions() int   { return 1536 }
func (e *openAIEmbedder) ModelName() string { return "text-embedding-3-small" }

func (e *openAIEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{
		"input": texts,
		"model": "text-embedding-3-small",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embeddings: status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("openai embeddings decode: %w", err)
	}

	out := make([][]float32, len(result.Data))
	for i, d := range result.Data {
		v := make([]float32, len(d.Embedding))
		for j, f := range d.Embedding {
			v[j] = float32(f)
		}
		out[i] = v
	}
	return out, nil
}

// --- Google ---

type googleEmbedder struct {
	apiKey  string
	baseURL string
}

// NewGoogle creates an embedder backed by Google text-embedding-004.
// baseURL defaults to https://generativelanguage.googleapis.com if empty.
func NewGoogle(apiKey, baseURL string) Embedder {
	if baseURL == "" {
		baseURL = "https://generativelanguage.googleapis.com"
	}
	return &googleEmbedder{apiKey: apiKey, baseURL: baseURL}
}

func (e *googleEmbedder) Dimensions() int   { return 768 }
func (e *googleEmbedder) ModelName() string { return "text-embedding-004" }

func (e *googleEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Parts []part `json:"parts"`
	}
	type embedReq struct {
		Model   string  `json:"model"`
		Content content `json:"content"`
	}
	requests := make([]embedReq, len(texts))
	for i, t := range texts {
		requests[i] = embedReq{
			Model:   "models/text-embedding-004",
			Content: content{Parts: []part{{Text: t}}},
		}
	}
	body, err := json.Marshal(map[string]any{"requests": requests})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/text-embedding-004:batchEmbedContents", e.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", e.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google embeddings: status %d", resp.StatusCode)
	}

	var result struct {
		Embeddings []struct {
			Values []float64 `json:"values"`
		} `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("google embeddings decode: %w", err)
	}

	out := make([][]float32, len(result.Embeddings))
	for i, d := range result.Embeddings {
		v := make([]float32, len(d.Values))
		for j, f := range d.Values {
			v[j] = float32(f)
		}
		out[i] = v
	}
	return out, nil
}
