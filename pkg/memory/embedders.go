package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// OpenAIEmbedder uses OpenAI API for embeddings
type OpenAIEmbedder struct {
	apiKey string
	model  string
	dims   int
}

// NewOpenAIEmbedder creates a new OpenAI embedder
func NewOpenAIEmbedder(apiKey, model string) (*OpenAIEmbedder, error) {
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key not provided")
	}

	// Default model
	if model == "" {
		model = "text-embedding-3-small"
	}

	// Dimensions for different models
	dims := 1536 // Default for text-embedding-3-small
	if model == "text-embedding-3-large" {
		dims = 3072
	} else if strings.Contains(model, "ada") {
		dims = 1536
	}

	return &OpenAIEmbedder{
		apiKey: apiKey,
		model:  model,
		dims:   dims,
	}, nil
}

// Embed generates embedding for text
func (e *OpenAIEmbedder) Embed(text string) ([]float32, error) {
	payload := map[string]interface{}{
		"model": e.model,
		"input": text,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/embeddings", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API error: %s", resp.Status)
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding returned")
	}

	return normalizeVector(result.Data[0].Embedding), nil
}

// Dimensions returns embedding dimensions
func (e *OpenAIEmbedder) Dimensions() int {
	return e.dims
}

// LocalEmbedder uses local model (via llama.cpp or similar)
type LocalEmbedder struct {
	modelPath string
	dims      int
}

// NewLocalEmbedder creates a new local embedder
func NewLocalEmbedder(modelPath string) (*LocalEmbedder, error) {
	if modelPath == "" {
		// Try to find default model
		home, _ := os.UserHomeDir()
		candidates := []string{
			filepath.Join(home, ".picoclaw", "models", "embeddings", "all-MiniLM-L6-v2.gguf"),
			filepath.Join(home, ".picoclaw", "models", "embeddings", "model.gguf"),
		}
		for _, c := range candidates {
			if _, err := os.Stat(c); err == nil {
				modelPath = c
				break
			}
		}
	}

	if modelPath == "" {
		return nil, fmt.Errorf("local model path not provided and no default found")
	}

	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("model file not found: %s", modelPath)
	}

	// Default dimensions for common models
	dims := 384 // all-MiniLM-L6-v2
	if strings.Contains(modelPath, "large") {
		dims = 768
	}

	return &LocalEmbedder{
		modelPath: modelPath,
		dims:      dims,
	}, nil
}

// Embed generates embedding using local model
// This is a simplified version - in production, use a proper Go binding for llama.cpp
func (e *LocalEmbedder) Embed(text string) ([]float32, error) {
	// Try to use a Python script with llama-cpp-python or similar
	// For now, return a simple hash-based embedding as fallback
	
	// Check if we have a Python embedding script
	scriptPath := filepath.Join(filepath.Dir(e.modelPath), "embed.py")
	if _, err := os.Stat(scriptPath); err == nil {
		return e.embedWithPython(scriptPath, text)
	}

	// Fallback: simple character n-gram embedding
	// This is not semantically meaningful but provides consistent vectors
	return e.fallbackEmbedding(text), nil
}

// embedWithPython calls Python script for embedding
func (e *LocalEmbedder) embedWithPython(scriptPath, text string) ([]float32, error) {
	cmd := exec.Command("python3", scriptPath, e.modelPath, text)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("embedding script failed: %w", err)
	}

	var embedding []float32
	if err := json.Unmarshal(output, &embedding); err != nil {
		return nil, err
	}

	return normalizeVector(embedding), nil
}

// fallbackEmbedding creates a simple hash-based embedding
// Not semantically meaningful, but consistent
func (e *LocalEmbedder) fallbackEmbedding(text string) []float32 {
	// Simple n-gram based embedding
	vector := make([]float32, e.dims)
	ngrams := e.extractNgrams(text, 3)
	
	for _, ngram := range ngrams {
		hash := e.hashString(ngram)
		idx := int(hash % uint64(e.dims))
		vector[idx] += 1.0
	}

	return normalizeVector(vector)
}

// extractNgrams extracts character n-grams from text
func (e *LocalEmbedder) extractNgrams(text string, n int) []string {
	text = strings.ToLower(text)
	var ngrams []string
	for i := 0; i <= len(text)-n; i++ {
		ngrams = append(ngrams, text[i:i+n])
	}
	return ngrams
}

// hashString creates a simple hash of a string
func (e *LocalEmbedder) hashString(s string) uint64 {
	var hash uint64 = 5381
	for _, c := range s {
		hash = ((hash << 5) + hash) + uint64(c)
	}
	return hash
}

// Dimensions returns embedding dimensions
func (e *LocalEmbedder) Dimensions() int {
	return e.dims
}

// SimpleEmbedder is a lightweight embedder that doesn't require external services
// Uses TF-IDF like approach with a vocabulary
type SimpleEmbedder struct {
	vocab  map[string]int
	dims   int
}

// NewSimpleEmbedder creates a simple embedder with built-in vocabulary
func NewSimpleEmbedder() *SimpleEmbedder {
	// Common English words as vocabulary
	commonWords := []string{
		"the", "be", "to", "of", "and", "a", "in", "that", "have", "i",
		"it", "for", "not", "on", "with", "he", "as", "you", "do", "at",
		"this", "but", "his", "by", "from", "they", "we", "say", "her", "she",
		"or", "an", "will", "my", "one", "all", "would", "there", "their", "what",
		"so", "up", "out", "if", "about", "who", "get", "which", "go", "me",
		"when", "make", "can", "like", "time", "no", "just", "him", "know", "take",
		"people", "into", "year", "your", "good", "some", "could", "them", "see", "other",
		"than", "then", "now", "look", "only", "come", "its", "over", "think", "also",
		"back", "after", "use", "two", "how", "our", "work", "first", "well", "way",
		"even", "new", "want", "because", "any", "these", "give", "day", "most", "us",
		"is", "was", "are", "were", "been", "has", "had", "did", "does", "doing",
		"code", "function", "class", "method", "variable", "program", "software", "computer",
		"data", "file", "project", "build", "test", "run", "debug", "error", "fix",
		"create", "add", "remove", "delete", "update", "change", "modify", "edit",
		"install", "configure", "setup", "deploy", "server", "client", "api", "web",
		"database", "query", "table", "column", "row", "sql", "nosql", "json", "xml",
		"python", "javascript", "typescript", "go", "golang", "rust", "java", "cpp", "c++",
		"react", "vue", "angular", "node", "express", "django", "flask", "fastapi",
		"docker", "kubernetes", "container", "cloud", "aws", "azure", "gcp",
		"git", "github", "commit", "branch", "merge", "pull", "push", "repository",
		"memory", "remember", "recall", "search", "find", "store", "save", "load",
		"user", "preference", "like", "dislike", "want", "need", "important", "always",
		"never", "usually", "sometimes", "often", "rarely", "name", "email", "phone",
		"address", "location", "place", "city", "country", "company", "work", "job",
	}

	vocab := make(map[string]int)
	for i, word := range commonWords {
		vocab[word] = i
	}

	return &SimpleEmbedder{
		vocab: vocab,
		dims:  len(commonWords),
	}
}

// Embed generates embedding using TF-IDF like approach
func (e *SimpleEmbedder) Embed(text string) ([]float32, error) {
	vector := make([]float32, e.dims)
	
	// Tokenize
	words := strings.Fields(strings.ToLower(text))
	
	// Count word frequencies
	wordCount := make(map[string]int)
	for _, word := range words {
		// Remove punctuation
		word = strings.TrimFunc(word, func(r rune) bool {
			return r < 'a' || r > 'z'
		})
		if word != "" {
			wordCount[word]++
		}
	}
	
	// Create vector
	for word, count := range wordCount {
		if idx, ok := e.vocab[word]; ok {
			// TF (term frequency)
			tf := float32(count) / float32(len(words))
			vector[idx] = tf
		}
	}

	return normalizeVector(vector), nil
}

// Dimensions returns embedding dimensions
func (e *SimpleEmbedder) Dimensions() int {
	return e.dims
}
