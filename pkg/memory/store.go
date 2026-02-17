// PicoClaw - Memory Store with Vector Search
// Adapted from OpenClaw memory-lancedb-local plugin
// Uses SQLite with vector extension for local embeddings storage

package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// MemoryEntry represents a stored memory
type MemoryEntry struct {
	ID         string    `json:"id"`
	Text       string    `json:"text"`
	Vector     []float32 `json:"vector"`
	Importance float32   `json:"importance"`
	Category   string    `json:"category"`
	SessionKey string    `json:"session_key"`
	CreatedAt  time.Time `json:"created_at"`
}

// MemorySearchResult represents a search result
type MemorySearchResult struct {
	Entry MemoryEntry `json:"entry"`
	Score float32     `json:"score"`
}

// EmbeddingProvider interface for different embedding sources
type EmbeddingProvider interface {
	Embed(text string) ([]float32, error)
	Dimensions() int
}

// StoreConfig configuration for memory store
type StoreConfig struct {
	DbPath            string  `json:"db_path"`
	EmbeddingProvider string  `json:"embedding_provider"` // "openai" or "local"
	EmbeddingModel    string  `json:"embedding_model"`
	OpenAIAPIKey      string  `json:"openai_api_key,omitempty"`
	LocalModelPath    string  `json:"local_model_path,omitempty"`
	MinScore          float32 `json:"min_score"`    // Minimum similarity score (0-1)
	MaxResults        int     `json:"max_results"`  // Max results per search
	AutoCapture       bool    `json:"auto_capture"` // Enable auto-capture
}

// DefaultConfig returns default configuration
func DefaultConfig(workspace string) StoreConfig {
	return StoreConfig{
		DbPath:            filepath.Join(workspace, "memory", "vector.db"),
		EmbeddingProvider: "openai",
		EmbeddingModel:    "text-embedding-3-small",
		MinScore:          0.5,
		MaxResults:        5,
		AutoCapture:       true,
	}
}

// MemoryStore manages vector-based memory
type MemoryStore struct {
	db         *sql.DB
	embedder   EmbeddingProvider
	config     StoreConfig
	categories []string
}

// Embedder returns the embedding provider
func (s *MemoryStore) Embedder() EmbeddingProvider {
	return s.embedder
}

// Memory categories
var MemoryCategories = []string{
	"preference", // User preferences (likes, dislikes)
	"decision",   // Decisions made
	"entity",     // Named entities (people, places, things)
	"fact",       // General facts
	"other",      // Uncategorized
}

// NewMemoryStore creates a new memory store
func NewMemoryStore(config StoreConfig) (*MemoryStore, error) {
	// Ensure directory exists
	dir := filepath.Dir(config.DbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create memory directory: %w", err)
	}

	// Open SQLite database
	db, err := sql.Open("sqlite3", config.DbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("failed to open memory database: %w", err)
	}

	store := &MemoryStore{
		db:         db,
		config:     config,
		categories: MemoryCategories,
	}

	// Initialize schema
	if err := store.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	// Initialize embedding provider
	if err := store.initEmbedder(); err != nil {
		db.Close()
		return nil, err
	}

	logger.InfoCF("memory", "Memory store initialized", map[string]interface{}{
		"db_path":   config.DbPath,
		"provider":  config.EmbeddingProvider,
		"model":     config.EmbeddingModel,
		"dimensions": store.embedder.Dimensions(),
	})

	return store, nil
}

// initSchema creates database tables
func (s *MemoryStore) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS memories (
		id TEXT PRIMARY KEY,
		text TEXT NOT NULL,
		vector BLOB NOT NULL,
		importance REAL DEFAULT 0.5,
		category TEXT DEFAULT 'other',
		session_key TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_memories_category ON memories(category);
	CREATE INDEX IF NOT EXISTS idx_memories_session ON memories(session_key);
	CREATE INDEX IF NOT EXISTS idx_memories_created ON memories(created_at);
	`

	_, err := s.db.Exec(schema)
	return err
}

// initEmbedder initializes the embedding provider
func (s *MemoryStore) initEmbedder() error {
	switch s.config.EmbeddingProvider {
	case "openai":
		embedder, err := NewOpenAIEmbedder(s.config.OpenAIAPIKey, s.config.EmbeddingModel)
		if err != nil {
			return err
		}
		s.embedder = embedder

	case "local":
		embedder, err := NewLocalEmbedder(s.config.LocalModelPath)
		if err != nil {
			return err
		}
		s.embedder = embedder

	default:
		return fmt.Errorf("unknown embedding provider: %s", s.config.EmbeddingProvider)
	}

	return nil
}

// Store saves a new memory
func (s *MemoryStore) Store(text string, importance float32, category string, sessionKey string) (*MemoryEntry, error) {
	// Generate embedding
	vector, err := s.embedder.Embed(text)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Validate category
	if !s.isValidCategory(category) {
		category = "other"
	}

	entry := &MemoryEntry{
		ID:         uuid.New().String(),
		Text:       text,
		Vector:     vector,
		Importance: importance,
		Category:   category,
		SessionKey: sessionKey,
		CreatedAt:  time.Now(),
	}

	// Serialize vector
	vectorJSON, err := json.Marshal(vector)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize vector: %w", err)
	}

	// Insert into database
	_, err = s.db.Exec(
		`INSERT INTO memories (id, text, vector, importance, category, session_key, created_at) 
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.Text, vectorJSON, entry.Importance, entry.Category, entry.SessionKey, entry.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to store memory: %w", err)
	}

	logger.DebugCF("memory", "Memory stored", map[string]interface{}{
		"id":       entry.ID,
		"category": entry.Category,
		"text_len": len(entry.Text),
	})

	return entry, nil
}

// Search finds similar memories using cosine similarity
func (s *MemoryStore) Search(query string, limit int, minScore float32) ([]MemorySearchResult, error) {
	if limit <= 0 {
		limit = s.config.MaxResults
	}
	if minScore <= 0 {
		minScore = s.config.MinScore
	}

	// Generate query embedding
	queryVector, err := s.embedder.Embed(query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate query embedding: %w", err)
	}

	// Load all memories and compute similarity
	// Note: For production, use a proper vector database like Milvus or Weaviate
	rows, err := s.db.Query(`
		SELECT id, text, vector, importance, category, session_key, created_at 
		FROM memories 
		ORDER BY created_at DESC
		LIMIT 1000
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to query memories: %w", err)
	}
	defer rows.Close()

	var results []MemorySearchResult
	for rows.Next() {
		var entry MemoryEntry
		var vectorJSON []byte

		err := rows.Scan(&entry.ID, &entry.Text, &vectorJSON, &entry.Importance, 
			&entry.Category, &entry.SessionKey, &entry.CreatedAt)
		if err != nil {
			continue
		}

		// Deserialize vector
		if err := json.Unmarshal(vectorJSON, &entry.Vector); err != nil {
			continue
		}

		// Compute cosine similarity
		score := cosineSimilarity(queryVector, entry.Vector)
		if score >= minScore {
			results = append(results, MemorySearchResult{
				Entry: entry,
				Score: score,
			})
		}
	}

	// Sort by score (descending)
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Score > results[i].Score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	logger.DebugCF("memory", "Memory search completed", map[string]interface{}{
		"query":      query,
		"results":    len(results),
		"min_score":  minScore,
	})

	return results, nil
}

// Delete removes a memory by ID
func (s *MemoryStore) Delete(id string) error {
	// Validate UUID format
	uuidRegex := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	if !uuidRegex.MatchString(strings.ToLower(id)) {
		return fmt.Errorf("invalid memory ID format: %s", id)
	}

	result, err := s.db.Exec("DELETE FROM memories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete memory: %w", err)
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return fmt.Errorf("memory not found: %s", id)
	}

	return nil
}

// Count returns the total number of memories
func (s *MemoryStore) Count() (int, error) {
	var count int
	err := s.db.QueryRow("SELECT COUNT(*) FROM memories").Scan(&count)
	return count, err
}

// Close closes the database connection
func (s *MemoryStore) Close() error {
	return s.db.Close()
}

// isValidCategory checks if category is valid
func (s *MemoryStore) isValidCategory(category string) bool {
	for _, c := range s.categories {
		if c == category {
			return true
		}
	}
	return false
}

// cosineSimilarity computes cosine similarity between two vectors
func cosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float32
	for i := 0; i < len(a); i++ {
		dotProduct += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

// normalizeVector L2 normalizes a vector
func normalizeVector(v []float32) []float32 {
	var sum float32
	for _, x := range v {
		sum += x * x
	}
	norm := float32(math.Sqrt(float64(sum)))
	if norm == 0 {
		return v
	}

	result := make([]float32, len(v))
	for i, x := range v {
		result[i] = x / norm
	}
	return result
}
