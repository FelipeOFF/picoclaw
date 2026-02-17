package channels

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	tu "github.com/mymmrac/telego/telegoutil"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// StreamingConfig configures how streaming responses work
type StreamingConfig struct {
	Enabled           bool
	ChunkSize         int           // Size of each chunk
	ChunkDelay        time.Duration // Delay between chunks
	MaxChunks         int           // Maximum number of chunks
	ParallelWorkers   int           // Number of parallel workers for processing
}

// DefaultStreamingConfig returns default streaming configuration
// Optimized for multi-core systems
func DefaultStreamingConfig() StreamingConfig {
	return StreamingConfig{
		Enabled:         true,
		ChunkSize:       3500,             // Safe size for Telegram (under 4096)
		ChunkDelay:      100 * time.Millisecond,
		MaxChunks:       50,               // ~175KB total
		ParallelWorkers: 4,                // Use 4 cores for processing
	}
}

// StreamingSender handles sending large messages in streaming fashion
type StreamingSender struct {
	bot    *telego.Bot
	config StreamingConfig
}

// NewStreamingSender creates a new streaming sender
func NewStreamingSender(bot *telego.Bot, config StreamingConfig) *StreamingSender {
	return &StreamingSender{
		bot:    bot,
		config: config,
	}
}

// SendLargeMessage sends a large message using streaming/chunking
// This method uses parallel processing for better performance on multi-core systems
func (s *StreamingSender) SendLargeMessage(ctx context.Context, chatID int64, content string) error {
	if !s.config.Enabled {
		return s.sendSimple(ctx, chatID, content)
	}

	// If content is small enough, send normally
	if len(content) <= s.config.ChunkSize {
		return s.sendSimple(ctx, chatID, content)
	}

	// Split content into chunks
	chunks := s.splitIntoChunks(content)
	
	if len(chunks) > s.config.MaxChunks {
		logger.WarnCF("telegram", "Too many chunks, truncating", map[string]interface{}{
			"total_chunks": len(chunks),
			"max_chunks":   s.config.MaxChunks,
		})
		chunks = chunks[:s.config.MaxChunks]
		// Add truncation notice to last chunk
		chunks[len(chunks)-1] += "\n\n[Message truncated due to length]"
	}

	logger.InfoCF("telegram", "Streaming message", map[string]interface{}{
		"chunks":       len(chunks),
		"total_length": len(content),
		"chat_id":      chatID,
	})

	// Send chunks with rate limiting
	for i, chunk := range chunks {
		if err := s.sendChunk(ctx, chatID, chunk, i+1, len(chunks)); err != nil {
			return fmt.Errorf("failed to send chunk %d/%d: %w", i+1, len(chunks), err)
		}
		
		// Delay between chunks (except for the last one)
		if i < len(chunks)-1 {
			time.Sleep(s.config.ChunkDelay)
		}
	}

	return nil
}

// SendLargeMessageParallel sends chunks in parallel for better performance
// Uses worker pool pattern for multi-core systems
func (s *StreamingSender) SendLargeMessageParallel(ctx context.Context, chatID int64, content string) error {
	if !s.config.Enabled || len(content) <= s.config.ChunkSize {
		return s.sendSimple(ctx, chatID, content)
	}

	chunks := s.splitIntoChunks(content)
	
	if len(chunks) > s.config.MaxChunks {
		chunks = chunks[:s.config.MaxChunks]
		chunks[len(chunks)-1] += "\n\n[Message truncated due to length]"
	}

	// For small number of chunks, sequential is faster
	if len(chunks) <= 3 {
		return s.SendLargeMessage(ctx, chatID, content)
	}

	logger.InfoCF("telegram", "Streaming message (parallel)", map[string]interface{}{
		"chunks":         len(chunks),
		"workers":        s.config.ParallelWorkers,
		"total_length":   len(content),
	})

	// Use worker pool for parallel processing
	type chunkResult struct {
		index int
		err   error
	}

	resultChan := make(chan chunkResult, len(chunks))
	chunkChan := make(chan struct {
		index int
		data  string
	}, len(chunks))

	// Fill chunk channel
	for i, chunk := range chunks {
		chunkChan <- struct {
			index int
			data  string
		}{index: i, data: chunk}
	}
	close(chunkChan)

	// Start workers
	var wg sync.WaitGroup
	for w := 0; w < s.config.ParallelWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for chunk := range chunkChan {
				err := s.sendChunk(ctx, chatID, chunk.data, chunk.index+1, len(chunks))
				resultChan <- chunkResult{index: chunk.index, err: err}
			}
		}()
	}

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	var firstError error
	for result := range resultChan {
		if result.err != nil && firstError == nil {
			firstError = result.err
		}
	}

	return firstError
}

// splitIntoChunks splits content into Telegram-safe chunks
// Tries to preserve paragraph boundaries
func (s *StreamingSender) splitIntoChunks(content string) []string {
	var chunks []string
	
	// First try to split by paragraphs
	paragraphs := strings.Split(content, "\n\n")
	
	var currentChunk strings.Builder
	for _, paragraph := range paragraphs {
		// If paragraph itself is too long, split by lines
		if len(paragraph) > s.config.ChunkSize {
			// Send current chunk if not empty
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
			}
			
			// Split long paragraph by lines
			lines := strings.Split(paragraph, "\n")
			for _, line := range lines {
				if currentChunk.Len()+len(line)+1 > s.config.ChunkSize {
					if currentChunk.Len() > 0 {
						chunks = append(chunks, currentChunk.String())
						currentChunk.Reset()
					}
				}
				if currentChunk.Len() > 0 {
					currentChunk.WriteString("\n")
				}
				currentChunk.WriteString(line)
			}
			continue
		}
		
		// Check if adding this paragraph would exceed chunk size
		if currentChunk.Len()+len(paragraph)+2 > s.config.ChunkSize {
			if currentChunk.Len() > 0 {
				chunks = append(chunks, currentChunk.String())
				currentChunk.Reset()
			}
		}
		
		if currentChunk.Len() > 0 {
			currentChunk.WriteString("\n\n")
		}
		currentChunk.WriteString(paragraph)
	}
	
	// Don't forget the last chunk
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}
	
	return chunks
}

// sendChunk sends a single chunk with progress indicator
func (s *StreamingSender) sendChunk(ctx context.Context, chatID int64, chunk string, current, total int) error {
	// Add progress indicator for multi-chunk messages
	var text string
	if total > 1 {
		text = fmt.Sprintf("(%d/%d)\n%s", current, total, chunk)
	} else {
		text = chunk
	}
	
	msg := tu.Message(tu.ID(chatID), text)
	_, err := s.bot.SendMessage(ctx, msg)
	return err
}

// sendSimple sends a simple message without streaming
func (s *StreamingSender) sendSimple(ctx context.Context, chatID int64, content string) error {
	msg := tu.Message(tu.ID(chatID), content)
	_, err := s.bot.SendMessage(ctx, msg)
	return err
}

// ProcessLargeContent processes large content in parallel chunks
// Returns processed chunks ready for sending
func (s *StreamingSender) ProcessLargeContent(content string, processor func(string) string) []string {
	chunks := s.splitIntoChunks(content)
	
	// Process chunks in parallel
	resultChan := make(chan struct {
		index int
		data  string
	}, len(chunks))
	
	var wg sync.WaitGroup
	for i, chunk := range chunks {
		wg.Add(1)
		go func(idx int, data string) {
			defer wg.Done()
			processed := processor(data)
			resultChan <- struct {
				index int
				data  string
			}{index: idx, data: processed}
		}(i, chunk)
	}
	
	go func() {
		wg.Wait()
		close(resultChan)
	}()
	
	// Collect results in order
	results := make([]string, len(chunks))
	for result := range resultChan {
		results[result.index] = result.data
	}
	
	return results
}
