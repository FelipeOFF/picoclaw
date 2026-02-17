package channels

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	th "github.com/mymmrac/telego/telegohandler"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegohandler"
	tu "github.com/mymmrac/telego/telegoutil"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/utils"
	"github.com/sipeed/picoclaw/pkg/voice"
)

type TelegramChannel struct {
	*BaseChannel
	bot             *telego.Bot
	commands        TelegramCommander
	cmdRegistry     *CommandRegistry
	streamingSender *StreamingSender
	config          *config.Config
	chatIDs         map[string]int64
	transcriber     *voice.GroqTranscriber
	placeholders    sync.Map // chatID -> messageID
	stopThinking    sync.Map // chatID -> thinkingCancel
}

type thinkingCancel struct {
	fn context.CancelFunc
}

func (c *thinkingCancel) Cancel() {
	if c != nil && c.fn != nil {
		c.fn()
	}
}

func NewTelegramChannel(cfg *config.Config, bus *bus.MessageBus, workspace string) (*TelegramChannel, error) {
	var opts []telego.BotOption
	telegramCfg := cfg.Channels.Telegram

	if telegramCfg.Proxy != "" {
		proxyURL, parseErr := url.Parse(telegramCfg.Proxy)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", telegramCfg.Proxy, parseErr)
		}
		opts = append(opts, telego.WithHTTPClient(&http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}))
	}

	bot, err := telego.NewBot(telegramCfg.Token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create telegram bot: %w", err)
	}

	base := NewBaseChannel("telegram", telegramCfg, bus, telegramCfg.AllowFrom)
	
	// Create command registry
	cmdRegistry := NewCommandRegistry(bot, cfg, workspace)
	
	// Create streaming sender optimized for multi-core systems
	streamingConfig := DefaultStreamingConfig()
	streamingConfig.ParallelWorkers = 4 // Use 4 workers for your 6 cores
	streamingSender := NewStreamingSender(bot, streamingConfig)

	return &TelegramChannel{
		BaseChannel:     base,
		commands:        &cmdAdapter{registry: cmdRegistry},
		cmdRegistry:     cmdRegistry,
		streamingSender: streamingSender,
		bot:             bot,
		config:          cfg,
		chatIDs:         make(map[string]int64),
		transcriber:     nil,
		placeholders:    sync.Map{},
		stopThinking:    sync.Map{},
	}, nil
}

func (c *TelegramChannel) SetTranscriber(transcriber *voice.GroqTranscriber) {
	c.transcriber = transcriber
}

// startThinking starts the typing indicator
// Similar to OpenClaw's behavior - shows "bot is typing..." in Telegram
// No placeholder message is sent, just the native typing indicator
func (c *TelegramChannel) startThinking(ctx context.Context, chatID int64, chatIDStr string) {
	// Stop any previous thinking animation
	if prevStop, ok := c.stopThinking.Load(chatIDStr); ok {
		if cf, ok := prevStop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
	}

	// Create cancel function for thinking state (5 minute timeout)
	_, thinkCancel := context.WithTimeout(ctx, 5*time.Minute)
	c.stopThinking.Store(chatIDStr, &thinkingCancel{fn: thinkCancel})

	// Send typing action (shows "typing..." in chat header)
	// This is the native Telegram typing indicator
	err := c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping))
	if err != nil {
		logger.DebugCF("telegram", "Failed to send typing action", map[string]interface{}{
			"error": err.Error(),
		})
	}

	// Keep sending typing action periodically while processing
	// Telegram typing indicator lasts ~5 seconds, so we refresh every 4 seconds
	go func() {
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				// Check if still thinking
				if _, ok := c.stopThinking.Load(chatIDStr); ok {
					_ = c.bot.SendChatAction(ctx, tu.ChatAction(tu.ID(chatID), telego.ChatActionTyping))
				} else {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
}

// loadCustomCommands loads custom commands from config
func (c *TelegramChannel) loadCustomCommands() {
	if c.cmdRegistry == nil {
		return
	}
	
	// Custom commands can be added here from config
	// For now, we'll just log that the registry is ready
	logger.DebugCF("telegram", "Command registry ready", map[string]interface{}{
		"native_commands": len(c.cmdRegistry.native),
	})
}

// SetSessionManager sets the session manager for the command registry
func (c *TelegramChannel) SetSessionManager(sm *session.SessionManager) {
	if c.cmdRegistry != nil {
		c.cmdRegistry.SetSessionManager(sm)
	}
}

func (c *TelegramChannel) Start(ctx context.Context) error {
	logger.InfoC("telegram", "Starting Telegram bot (polling mode)...")

	// Load custom commands from config
	c.loadCustomCommands()

	// Sync command menu with Telegram
	if c.cmdRegistry != nil {
		if err := c.cmdRegistry.SyncMenuCommands(ctx); err != nil {
			logger.WarnCF("telegram", "Failed to sync menu commands", map[string]interface{}{
				"error": err.Error(),
			})
		} else {
			logger.InfoC("telegram", "Menu commands synced successfully")
		}
	}

	updates, err := c.bot.UpdatesViaLongPolling(ctx, &telego.GetUpdatesParams{
		Timeout: 30,
	})
	if err != nil {
		return fmt.Errorf("failed to start long polling: %w", err)
	}

	bh, err := telegohandler.NewBotHandler(c.bot, updates)
	if err != nil {
		return fmt.Errorf("failed to create bot handler: %w", err)
	}

	// Basic commands
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		c.commands.Help(ctx, message)
		return nil
	}, th.CommandEqual("help"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Start(ctx, message)
	}, th.CommandEqual("start"))

	// Config commands
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Show(ctx, message)
	}, th.CommandEqual("show"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.List(ctx, message)
	}, th.CommandEqual("list"))

	// Session management
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Reset(ctx, message)
	}, th.CommandEqual("reset"))
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Session(ctx, message)
	}, th.CommandEqual("session"))

	// Model commands
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Model(ctx, message)
	}, th.CommandEqual("model"))

	// Status command
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.commands.Status(ctx, message)
	}, th.CommandEqual("status"))

	// Handle regular messages
	bh.HandleMessage(func(ctx *th.Context, message telego.Message) error {
		return c.handleMessage(ctx, &message)
	}, th.AnyMessage())

	c.setRunning(true)
	logger.InfoCF("telegram", "Telegram bot connected", map[string]interface{}{
		"username": c.bot.Username(),
	})

	go bh.Start()

	go func() {
		<-ctx.Done()
		bh.Stop()
	}()

	return nil
}
func (c *TelegramChannel) Stop(ctx context.Context) error {
	logger.InfoC("telegram", "Stopping Telegram bot...")
	c.setRunning(false)
	return nil
}

// Telegram message limits
const (
	telegramMaxMessageLength      = 4096
	telegramMaxMessageLengthSafe  = 4000  // Leave some margin for HTML formatting
	telegramMaxTotalContentLength = 50000 // Absolute max before heavy truncation (50KB)
)

func (c *TelegramChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	if !c.IsRunning() {
		return fmt.Errorf("telegram bot not running")
	}

	chatID, err := parseChatID(msg.ChatID)
	if err != nil {
		return fmt.Errorf("invalid chat ID: %w", err)
	}

	// Stop thinking animation
	if stop, ok := c.stopThinking.Load(msg.ChatID); ok {
		if cf, ok := stop.(*thinkingCancel); ok && cf != nil {
			cf.Cancel()
		}
		c.stopThinking.Delete(msg.ChatID)
	}

	content := msg.Content
	
	// Clean up the content - remove markdown/html artifacts for plain text
	content = cleanTelegramText(content)
	
	// Use streaming sender for large messages
	if c.streamingSender != nil && len(content) > telegramMaxMessageLengthSafe {
		logger.InfoCF("telegram", "Using streaming sender for large message", map[string]interface{}{
			"content_length": len(content),
			"chat_id":        msg.ChatID,
		})
		return c.streamingSender.SendLargeMessageParallel(ctx, chatID, content)
	}

	// Try to edit placeholder first
	if pID, ok := c.placeholders.Load(msg.ChatID); ok {
		c.placeholders.Delete(msg.ChatID)
		editMsg := tu.EditMessageText(tu.ID(chatID), pID.(int), content)
		// No ParseMode = plain text
		if _, err = c.bot.EditMessageText(ctx, editMsg); err == nil {
			return nil
		}
		// Fallback to new message if edit fails
	}

	// Send as plain text (no HTML/Markdown parsing)
	tgMsg := tu.Message(tu.ID(chatID), content)
	// ParseMode empty = plain text
	_, err = c.bot.SendMessage(ctx, tgMsg)
	return err
}

// sendSplitMessages splits a long message into multiple Telegram messages
func (c *TelegramChannel) sendSplitMessages(ctx context.Context, chatID int64, content string) error {
	// Split by paragraphs first to keep context
	paragraphs := strings.Split(content, "\n\n")
	
	var currentChunk strings.Builder
	chunkCount := 0
	
	for _, paragraph := range paragraphs {
		// If a single paragraph is too long, split it further
		if len(paragraph) > telegramMaxMessageLengthSafe {
			// Send current chunk if not empty
			if currentChunk.Len() > 0 {
				if err := c.sendMessageChunk(ctx, chatID, currentChunk.String()); err != nil {
					return err
				}
				chunkCount++
				currentChunk.Reset()
			}
			
			// Split long paragraph by lines
			lines := strings.Split(paragraph, "\n")
			for _, line := range lines {
				if currentChunk.Len()+len(line)+1 > telegramMaxMessageLengthSafe {
					if currentChunk.Len() > 0 {
						if err := c.sendMessageChunk(ctx, chatID, currentChunk.String()); err != nil {
							return err
						}
						chunkCount++
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
		
		// Check if adding this paragraph would exceed the limit
		if currentChunk.Len()+len(paragraph)+2 > telegramMaxMessageLengthSafe {
			if currentChunk.Len() > 0 {
				if err := c.sendMessageChunk(ctx, chatID, currentChunk.String()); err != nil {
					return err
				}
				chunkCount++
				currentChunk.Reset()
			}
		}
		
		if currentChunk.Len() > 0 {
			currentChunk.WriteString("\n\n")
		}
		currentChunk.WriteString(paragraph)
	}
	
	// Send final chunk
	if currentChunk.Len() > 0 {
		if err := c.sendMessageChunk(ctx, chatID, currentChunk.String()); err != nil {
			return err
		}
		chunkCount++
	}
	
	logger.InfoCF("telegram", "Message split and sent", map[string]interface{}{
		"chunks": chunkCount,
	})
	
	return nil
}

// sendMessageChunk sends a single message chunk
func (c *TelegramChannel) sendMessageChunk(ctx context.Context, chatID int64, content string) error {
	htmlContent := markdownToTelegramHTML(content)
	
	tgMsg := tu.Message(tu.ID(chatID), htmlContent)
	tgMsg.ParseMode = telego.ModeHTML
	
	if _, err := c.bot.SendMessage(ctx, tgMsg); err != nil {
		// Try without HTML parsing
		tgMsg.ParseMode = ""
		tgMsg.Text = content
		_, err = c.bot.SendMessage(ctx, tgMsg)
		return err
	}
	
	// Small delay to avoid rate limiting
	time.Sleep(100 * time.Millisecond)
	return nil
}

func (c *TelegramChannel) handleMessage(ctx context.Context, message *telego.Message) error {
	if message == nil {
		return fmt.Errorf("message is nil")
	}

	user := message.From
	if user == nil {
		return fmt.Errorf("message sender (user) is nil")
	}

	senderID := fmt.Sprintf("%d", user.ID)
	if user.Username != "" {
		senderID = fmt.Sprintf("%d|%s", user.ID, user.Username)
	}

	// 检查白名单，避免为被拒绝的用户下载附件
	if !c.IsAllowed(senderID) {
		logger.DebugCF("telegram", "Message rejected by allowlist", map[string]interface{}{
			"user_id": senderID,
		})
		return nil
	}

	chatID := message.Chat.ID
	c.chatIDs[senderID] = chatID

	content := ""
	mediaPaths := []string{}
	localFiles := []string{} // 跟踪需要清理的本地文件

	// 确保临时文件在函数返回时被清理
	defer func() {
		for _, file := range localFiles {
			if err := os.Remove(file); err != nil {
				logger.DebugCF("telegram", "Failed to cleanup temp file", map[string]interface{}{
					"file":  file,
					"error": err.Error(),
				})
			}
		}
	}()

	if message.Text != "" {
		content += message.Text
	}

	if message.Caption != "" {
		if content != "" {
			content += "\n"
		}
		content += message.Caption
	}

	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		photoPath := c.downloadPhoto(ctx, photo.FileID)
		if photoPath != "" {
			localFiles = append(localFiles, photoPath)
			mediaPaths = append(mediaPaths, photoPath)
			if content != "" {
				content += "\n"
			}
			content += "[image: photo]"
		}
	}

	if message.Voice != nil {
		voicePath := c.downloadFile(ctx, message.Voice.FileID, ".ogg")
		if voicePath != "" {
			localFiles = append(localFiles, voicePath)
			mediaPaths = append(mediaPaths, voicePath)

			transcribedText := ""
			if c.transcriber != nil && c.transcriber.IsAvailable() {
				ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
				defer cancel()

				result, err := c.transcriber.Transcribe(ctx, voicePath)
				if err != nil {
					logger.ErrorCF("telegram", "Voice transcription failed", map[string]interface{}{
						"error": err.Error(),
						"path":  voicePath,
					})
					transcribedText = "[voice (transcription failed)]"
				} else {
					transcribedText = fmt.Sprintf("[voice transcription: %s]", result.Text)
					logger.InfoCF("telegram", "Voice transcribed successfully", map[string]interface{}{
						"text": result.Text,
					})
				}
			} else {
				transcribedText = "[voice]"
			}

			if content != "" {
				content += "\n"
			}
			content += transcribedText
		}
	}

	if message.Audio != nil {
		audioPath := c.downloadFile(ctx, message.Audio.FileID, ".mp3")
		if audioPath != "" {
			localFiles = append(localFiles, audioPath)
			mediaPaths = append(mediaPaths, audioPath)
			if content != "" {
				content += "\n"
			}
			content += "[audio]"
		}
	}

	if message.Document != nil {
		docPath := c.downloadFile(ctx, message.Document.FileID, "")
		if docPath != "" {
			localFiles = append(localFiles, docPath)
			mediaPaths = append(mediaPaths, docPath)
			if content != "" {
				content += "\n"
			}
			content += "[file]"
		}
	}

	if content == "" {
		content = "[empty message]"
	}

	logger.DebugCF("telegram", "Received message", map[string]interface{}{
		"sender_id": senderID,
		"chat_id":   fmt.Sprintf("%d", chatID),
		"preview":   utils.Truncate(content, 50),
	})

	// Start thinking indicator (typing animation + placeholder message)
	// This mimics OpenClaw's behavior - shows the bot is "typing"
	chatIDStr := fmt.Sprintf("%d", chatID)
	c.startThinking(ctx, chatID, chatIDStr)

	metadata := map[string]string{
		"message_id": fmt.Sprintf("%d", message.MessageID),
		"user_id":    fmt.Sprintf("%d", user.ID),
		"username":   user.Username,
		"first_name": user.FirstName,
		"is_group":   fmt.Sprintf("%t", message.Chat.Type != "private"),
	}

	c.HandleMessage(fmt.Sprintf("%d", user.ID), fmt.Sprintf("%d", chatID), content, mediaPaths, metadata)
	return nil
}

func (c *TelegramChannel) downloadPhoto(ctx context.Context, fileID string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get photo file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ".jpg")
}

func (c *TelegramChannel) downloadFileWithInfo(file *telego.File, ext string) string {
	if file.FilePath == "" {
		return ""
	}

	url := c.bot.FileDownloadURL(file.FilePath)
	logger.DebugCF("telegram", "File URL", map[string]interface{}{"url": url})

	// Use FilePath as filename for better identification
	filename := file.FilePath + ext
	return utils.DownloadFile(url, filename, utils.DownloadOptions{
		LoggerPrefix: "telegram",
	})
}

func (c *TelegramChannel) downloadFile(ctx context.Context, fileID, ext string) string {
	file, err := c.bot.GetFile(ctx, &telego.GetFileParams{FileID: fileID})
	if err != nil {
		logger.ErrorCF("telegram", "Failed to get file", map[string]interface{}{
			"error": err.Error(),
		})
		return ""
	}

	return c.downloadFileWithInfo(file, ext)
}

func parseChatID(chatIDStr string) (int64, error) {
	var id int64
	_, err := fmt.Sscanf(chatIDStr, "%d", &id)
	return id, err
}

// cleanTelegramText removes markdown/html artifacts for plain text output
// Similar to OpenClaw's approach - clean, readable text without formatting
func cleanTelegramText(text string) string {
	if text == "" {
		return ""
	}

	// Remove markdown headers
	text = regexp.MustCompile(`(?m)^#{1,6}\s*`).ReplaceAllString(text, "")
	
	// Remove markdown bold/italic markers but keep content
	text = regexp.MustCompile(`\*\*([^*]+)\*\*`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`__([^_]+)__`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`_([^_]+)_`).ReplaceAllString(text, "$1")
	text = regexp.MustCompile(`\*([^*]+)\*`).ReplaceAllString(text, "$1")
	
	// Remove strikethrough
	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "$1")
	
	// Convert markdown links to plain text (just the text, not URL)
	text = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`).ReplaceAllString(text, "$1")
	
	// Remove code block markers but keep content
	text = regexp.MustCompile("```\\w*\\n?").ReplaceAllString(text, "")
	text = regexp.MustCompile("```").ReplaceAllString(text, "")
	text = regexp.MustCompile("`([^`]+)`").ReplaceAllString(text, "$1")
	
	// Remove HTML tags
	text = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(text, "")
	
	// Remove blockquote markers
	text = regexp.MustCompile(`(?m)^>\s*`).ReplaceAllString(text, "")
	
	// Clean up multiple newlines
	text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
	
	// Trim whitespace
	text = strings.TrimSpace(text)
	
	return text
}

func markdownToTelegramHTML(text string) string {
	if text == "" {
		return ""
	}

	codeBlocks := extractCodeBlocks(text)
	text = codeBlocks.text

	inlineCodes := extractInlineCodes(text)
	text = inlineCodes.text

	text = regexp.MustCompile(`^#{1,6}\s+(.+)$`).ReplaceAllString(text, "$1")

	text = regexp.MustCompile(`^>\s*(.*)$`).ReplaceAllString(text, "$1")

	text = escapeHTML(text)

	text = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(text, `<a href="$2">$1</a>`)

	text = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(text, "<b>$1</b>")

	text = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(text, "<b>$1</b>")

	reItalic := regexp.MustCompile(`_([^_]+)_`)
	text = reItalic.ReplaceAllStringFunc(text, func(s string) string {
		match := reItalic.FindStringSubmatch(s)
		if len(match) < 2 {
			return s
		}
		return "<i>" + match[1] + "</i>"
	})

	text = regexp.MustCompile(`~~(.+?)~~`).ReplaceAllString(text, "<s>$1</s>")

	text = regexp.MustCompile(`^[-*]\s+`).ReplaceAllString(text, "• ")

	for i, code := range inlineCodes.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00IC%d\x00", i), fmt.Sprintf("<code>%s</code>", escaped))
	}

	for i, code := range codeBlocks.codes {
		escaped := escapeHTML(code)
		text = strings.ReplaceAll(text, fmt.Sprintf("\x00CB%d\x00", i), fmt.Sprintf("<pre><code>%s</code></pre>", escaped))
	}

	return text
}

type codeBlockMatch struct {
	text  string
	codes []string
}

func extractCodeBlocks(text string) codeBlockMatch {
	re := regexp.MustCompile("```[\\w]*\\n?([\\s\\S]*?)```")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00CB%d\x00", i)
		i++
		return placeholder
	})

	return codeBlockMatch{text: text, codes: codes}
}

type inlineCodeMatch struct {
	text  string
	codes []string
}

func extractInlineCodes(text string) inlineCodeMatch {
	re := regexp.MustCompile("`([^`]+)`")
	matches := re.FindAllStringSubmatch(text, -1)

	codes := make([]string, 0, len(matches))
	for _, match := range matches {
		codes = append(codes, match[1])
	}

	i := 0
	text = re.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("\x00IC%d\x00", i)
		i++
		return placeholder
	})

	return inlineCodeMatch{text: text, codes: codes}
}

func escapeHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")
	return text
}
