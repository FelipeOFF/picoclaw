package channels

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mymmrac/telego"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/session"
)

// Command pattern for Telegram (a-z, 0-9, underscore, max 32 chars)
var telegramCommandPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// NativeCommand represents a built-in Telegram command
type NativeCommand struct {
	Name        string
	Description string
	Handler     func(ctx context.Context, msg telego.Message, args string) error
	RequireAuth bool // Whether command requires user authorization
}

// CustomCommand represents a user-defined command from config
type CustomCommand struct {
	Command     string
	Description string
	Response    string // Static response for simple commands
}

// CommandRegistry holds all available commands
type CommandRegistry struct {
	native  map[string]NativeCommand
	custom  map[string]CustomCommand
	bot     *telego.Bot
	config  *config.Config
	workspace string
	sessionManager *session.SessionManager
}

// NewCommandRegistry creates a new command registry
func NewCommandRegistry(bot *telego.Bot, cfg *config.Config, workspace string) *CommandRegistry {
	cr := &CommandRegistry{
		native:    make(map[string]NativeCommand),
		custom:    make(map[string]CustomCommand),
		bot:       bot,
		config:    cfg,
		workspace: workspace,
	}
	cr.registerNativeCommands()
	return cr
}

// SetSessionManager sets the session manager for session-related commands
func (cr *CommandRegistry) SetSessionManager(sm *session.SessionManager) {
	cr.sessionManager = sm
}

// RegisterNativeCommand registers a native command
func (cr *CommandRegistry) RegisterNativeCommand(cmd NativeCommand) {
	name := strings.ToLower(cmd.Name)
	if !telegramCommandPattern.MatchString(name) {
		logger.WarnCF("telegram", "Invalid native command name", map[string]interface{}{
			"command": name,
		})
		return
	}
	cr.native[name] = cmd
}

// RegisterCustomCommand registers a custom command from config
func (cr *CommandRegistry) RegisterCustomCommand(cmd CustomCommand) error {
	name := normalizeCommandName(cmd.Command)
	if name == "" {
		return fmt.Errorf("custom command name is empty")
	}
	if !telegramCommandPattern.MatchString(name) {
		return fmt.Errorf("invalid command name: %s (use a-z, 0-9, underscore; max 32 chars)", name)
	}
	if _, exists := cr.native[name]; exists {
		return fmt.Errorf("command /%s conflicts with native command", name)
	}
	if _, exists := cr.custom[name]; exists {
		return fmt.Errorf("command /%s is duplicated", name)
	}
	if strings.TrimSpace(cmd.Description) == "" {
		return fmt.Errorf("command /%s is missing description", name)
	}
	
	cr.custom[name] = CustomCommand{
		Command:     name,
		Description: strings.TrimSpace(cmd.Description),
		Response:    cmd.Response,
	}
	return nil
}

// GetCommand returns a command by name (checks native first, then custom)
func (cr *CommandRegistry) GetCommand(name string) (interface{}, bool) {
	name = normalizeCommandName(name)
	if cmd, ok := cr.native[name]; ok {
		return cmd, true
	}
	if cmd, ok := cr.custom[name]; ok {
		return cmd, true
	}
	return nil, false
}

// Execute runs a command
func (cr *CommandRegistry) Execute(ctx context.Context, msg telego.Message) error {
	text := msg.Text
	if text == "" {
		return nil
	}
	
	// Parse command and args
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return nil
	}
	
	cmdName := normalizeCommandName(parts[0])
	var args string
	if len(parts) > 1 {
		args = strings.Join(parts[1:], " ")
	}
	
	// Try native command first
	if nativeCmd, ok := cr.native[cmdName]; ok {
		// Check authorization if required
		if nativeCmd.RequireAuth && !cr.isAuthorized(msg) {
			return cr.sendMessage(ctx, msg.Chat.ID, "You are not authorized to use this command.")
		}
		return nativeCmd.Handler(ctx, msg, args)
	}
	
	// Try custom command
	if customCmd, ok := cr.custom[cmdName]; ok {
		if !cr.isAuthorized(msg) {
			return cr.sendMessage(ctx, msg.Chat.ID, "You are not authorized to use this command.")
		}
		return cr.sendMessage(ctx, msg.Chat.ID, customCmd.Response)
	}
	
	return fmt.Errorf("unknown command: %s", cmdName)
}

// GetAllCommands returns all commands for menu registration
func (cr *CommandRegistry) GetAllCommands() []telego.BotCommand {
	var commands []telego.BotCommand
	
	// Add native commands
	for _, cmd := range cr.native {
		commands = append(commands, telego.BotCommand{
			Command:     cmd.Name,
			Description: cmd.Description,
		})
	}
	
	// Add custom commands
	for _, cmd := range cr.custom {
		commands = append(commands, telego.BotCommand{
			Command:     cmd.Command,
			Description: cmd.Description,
		})
	}
	
	return commands
}

// SyncMenuCommands updates the Telegram bot menu commands
func (cr *CommandRegistry) SyncMenuCommands(ctx context.Context) error {
	commands := cr.GetAllCommands()
	if len(commands) == 0 {
		return nil
	}
	
	// Telegram limits to 100 commands
	if len(commands) > 100 {
		logger.WarnCF("telegram", "Too many commands for menu, truncating to 100", map[string]interface{}{
			"total": len(commands),
		})
		commands = commands[:100]
	}
	
	params := &telego.SetMyCommandsParams{
		Commands: commands,
	}
	
	return cr.bot.SetMyCommands(ctx, params)
}

// isAuthorized checks if the user is authorized to use commands
func (cr *CommandRegistry) isAuthorized(msg telego.Message) bool {
	// Get allowed users from config
	allowed := cr.config.Channels.Telegram.AllowFrom
	if len(allowed) == 0 {
		return true // No restrictions
	}
	
	user := msg.From
	if user == nil {
		return false
	}
	
	userID := fmt.Sprintf("%d", user.ID)
	
	for _, allowedID := range allowed {
		// Check exact match
		if allowedID == userID {
			return true
		}
		// Check username match (if allowedID starts with @)
		if strings.HasPrefix(allowedID, "@") && user.Username != "" {
			if strings.EqualFold(allowedID[1:], user.Username) {
				return true
			}
		}
		// Check ID|username format
		if strings.Contains(allowedID, "|") {
			parts := strings.SplitN(allowedID, "|", 2)
			if parts[0] == userID {
				return true
			}
		}
	}
	
	return false
}

// sendMessage is a helper to send a message
func (cr *CommandRegistry) sendMessage(ctx context.Context, chatID int64, text string) error {
	_, err := cr.bot.SendMessage(ctx, &telego.SendMessageParams{
		ChatID: telego.ChatID{ID: chatID},
		Text:   text,
	})
	return err
}

// registerNativeCommands registers all built-in commands
func (cr *CommandRegistry) registerNativeCommands() {
	// Help command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "help",
		Description: "Show available commands",
		Handler:     cr.handleHelp,
		RequireAuth: false,
	})
	
	// Start command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "start",
		Description: "Start the bot",
		Handler:     cr.handleStart,
		RequireAuth: false,
	})
	
	// Reset command - clears session
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "reset",
		Description: "Clear conversation history",
		Handler:     cr.handleReset,
		RequireAuth: true,
	})
	
	// Session command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "session",
		Description: "Show or manage sessions",
		Handler:     cr.handleSession,
		RequireAuth: true,
	})
	
	// Model command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "model",
		Description: "Show or switch model",
		Handler:     cr.handleModel,
		RequireAuth: true,
	})
	
	// Status command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "status",
		Description: "Show bot status",
		Handler:     cr.handleStatus,
		RequireAuth: true,
	})
	
	// Show command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "show",
		Description: "Show configuration or memory",
		Handler:     cr.handleShow,
		RequireAuth: true,
	})
	
	// List command
	cr.RegisterNativeCommand(NativeCommand{
		Name:        "list",
		Description: "List models or channels",
		Handler:     cr.handleList,
		RequireAuth: true,
	})
}

// Command handlers

func (cr *CommandRegistry) handleHelp(ctx context.Context, msg telego.Message, args string) error {
	var sb strings.Builder
	sb.WriteString("PicoClaw Commands\n\n")
	
	sb.WriteString("Session Management:\n")
	sb.WriteString("/reset - Clear current conversation\n")
	sb.WriteString("/session - Show current session info\n")
	sb.WriteString("/session list - List active sessions\n\n")
	
	sb.WriteString("Model Control:\n")
	sb.WriteString("/model - Show current model\n")
	sb.WriteString("/model list - List available models\n\n")
	
	sb.WriteString("System:\n")
	sb.WriteString("/status - Show bot status\n")
	sb.WriteString("/show config - Show configuration\n")
	sb.WriteString("/show memory - Show memory usage\n")
	sb.WriteString("/list channels - List enabled channels\n\n")
	
	sb.WriteString("General:\n")
	sb.WriteString("/start - Start the bot\n")
	sb.WriteString("/help - Show this help\n")
	
	// Add custom commands to help
	if len(cr.custom) > 0 {
		sb.WriteString("\nCustom Commands:\n")
		for name, cmd := range cr.custom {
			sb.WriteString(fmt.Sprintf("/%s - %s\n", name, cmd.Description))
		}
	}
	
	return cr.sendMessage(ctx, msg.Chat.ID, sb.String())
}

func (cr *CommandRegistry) handleStart(ctx context.Context, msg telego.Message, args string) error {
	welcome := fmt.Sprintf("Hello! I'm PicoClaw!\n\n"+
		"Your AI assistant with multi-session support.\n\n"+
		"Current Model: %s\n"+
		"Workspace: %s\n\n"+
		"Use /help to see available commands.",
		cr.config.Agents.Defaults.Model,
		cr.config.Agents.Defaults.Workspace)
	
	return cr.sendMessage(ctx, msg.Chat.ID, welcome)
}

func (cr *CommandRegistry) handleReset(ctx context.Context, msg telego.Message, args string) error {
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	sessionKey := "telegram:" + chatID
	
	// Clear session
	if cr.sessionManager != nil {
		cr.sessionManager.SetHistory(sessionKey, nil)
		cr.sessionManager.SetSummary(sessionKey, "")
	}
	
	// Delete session file
	sessionsDir := filepath.Join(cr.workspace, "sessions")
	sessionFile := filepath.Join(sessionsDir, "telegram_"+chatID+".json")
	os.Remove(sessionFile)
	
	logger.InfoCF("telegram", "Session reset via command", map[string]interface{}{
		"session_key": sessionKey,
		"user_id":     msg.From.ID,
	})
	
	return cr.sendMessage(ctx, msg.Chat.ID, "Session Reset! Conversation history cleared. Starting fresh!")
}

func (cr *CommandRegistry) handleSession(ctx context.Context, msg telego.Message, args string) error {
	chatID := fmt.Sprintf("%d", msg.Chat.ID)
	sessionKey := "telegram:" + chatID
	
	if strings.TrimSpace(args) == "" {
		// Show current session
		var historyLen int
		if cr.sessionManager != nil {
			history := cr.sessionManager.GetHistory(sessionKey)
			historyLen = len(history)
		}
		
		text := fmt.Sprintf("Current Session:\n\n"+
			"ID: telegram:%s\n"+
			"Messages: %d\n\n"+
			"Use /session list to see all sessions or /reset to clear this one.",
			chatID, historyLen)
		
		return cr.sendMessage(ctx, msg.Chat.ID, text)
	}
	
	parts := strings.Fields(args)
	switch parts[0] {
	case "list":
		text := "Active Sessions:\n\n"
		text += fmt.Sprintf("> telegram:%s (current)\n\n", chatID)
		text += "Sessions are created per chat automatically."
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	default:
		return cr.sendMessage(ctx, msg.Chat.ID, "Unknown subcommand. Use: /session or /session list")
	}
}

func (cr *CommandRegistry) handleModel(ctx context.Context, msg telego.Message, args string) error {
	if strings.TrimSpace(args) == "" {
		// Show current model
		provider := cr.config.Agents.Defaults.Provider
		if provider == "" {
			provider = "kimi-cli"
		}
		
		text := fmt.Sprintf("Current Model:\n\n"+
			"Model: %s\n"+
			"Provider: %s\n"+
			"Max Tokens: %d\n\n"+
			"Use /model list to see available models.",
			cr.config.Agents.Defaults.Model,
			provider,
			cr.config.Agents.Defaults.MaxTokens)
		
		return cr.sendMessage(ctx, msg.Chat.ID, text)
	}
	
	parts := strings.Fields(args)
	switch parts[0] {
	case "list":
		models := []string{
			"kimi-cli", "kimi-k2.5", "kimi-k1.5",
			"claude-3-5-sonnet", "claude-3-opus",
			"gpt-4o", "gpt-4-turbo",
			"glm-4.7",
		}
		
		text := "Available Models:\n\n"
		for _, m := range models {
			if m == cr.config.Agents.Defaults.Model {
				text += fmt.Sprintf("> %s (current)\n", m)
			} else {
				text += fmt.Sprintf("  %s\n", m)
			}
		}
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	case "switch", "set":
		if len(parts) < 2 {
			return cr.sendMessage(ctx, msg.Chat.ID, "Usage: /model switch <model-name>")
		}
		
		newModel := parts[1]
		text := fmt.Sprintf("To change model to %s, update your config file at ~/.picoclaw/config.json and restart the gateway.", newModel)
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	default:
		return cr.sendMessage(ctx, msg.Chat.ID, "Unknown subcommand. Use: /model, /model list, or /model switch <name>")
	}
}

func (cr *CommandRegistry) handleStatus(ctx context.Context, msg telego.Message, args string) error {
	text := fmt.Sprintf("PicoClaw Status:\n\n"+
		"Model: %s\n"+
		"Workspace: %s\n"+
		"Max Tokens: %d\n\n"+
		"Channels:\n"+
		"  Telegram: Enabled\n",
		cr.config.Agents.Defaults.Model,
		cr.config.Agents.Defaults.Workspace,
		cr.config.Agents.Defaults.MaxTokens)
	
	return cr.sendMessage(ctx, msg.Chat.ID, text)
}

func (cr *CommandRegistry) handleShow(ctx context.Context, msg telego.Message, args string) error {
	if strings.TrimSpace(args) == "" {
		return cr.sendMessage(ctx, msg.Chat.ID, "Usage: /show [config|memory]")
	}
	
	switch strings.TrimSpace(args) {
	case "config":
		provider := cr.config.Agents.Defaults.Provider
		if provider == "" {
			provider = "kimi-cli"
		}
		
		text := fmt.Sprintf("Configuration:\n\n"+
			"Model: %s\n"+
			"Provider: %s\n"+
			"Max Tokens: %d\n"+
			"Temperature: %.1f\n"+
			"Max Iterations: %d\n"+
			"Workspace: %s",
			cr.config.Agents.Defaults.Model,
			provider,
			cr.config.Agents.Defaults.MaxTokens,
			cr.config.Agents.Defaults.Temperature,
			cr.config.Agents.Defaults.MaxToolIterations,
			cr.config.Agents.Defaults.Workspace)
		
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	case "memory":
		sessionsDir := filepath.Join(cr.workspace, "sessions")
		var sessionCount int
		if entries, err := os.ReadDir(sessionsDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
					sessionCount++
				}
			}
		}
		
		text := fmt.Sprintf("Memory Status:\n\n"+
			"Stored Sessions: %d\n"+
			"Workspace: %s\n\n"+
			"Sessions are stored in sessions/ directory.",
			sessionCount, cr.workspace)
		
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	default:
		return cr.sendMessage(ctx, msg.Chat.ID, "Unknown parameter: "+args+". Try config or memory.")
	}
}

func (cr *CommandRegistry) handleList(ctx context.Context, msg telego.Message, args string) error {
	if strings.TrimSpace(args) == "" {
		return cr.sendMessage(ctx, msg.Chat.ID, "Usage: /list [models|channels]")
	}
	
	switch strings.TrimSpace(args) {
	case "models":
		models := []string{
			"kimi-cli", "kimi-k2.5", "kimi-k1.5",
			"claude-3-5-sonnet", "claude-3-opus",
			"gpt-4o", "gpt-4-turbo",
			"glm-4.7",
		}
		
		text := "Available Models:\n\n"
		for _, m := range models {
			if m == cr.config.Agents.Defaults.Model {
				text += fmt.Sprintf("> %s (current)\n", m)
			} else {
				text += fmt.Sprintf("  %s\n", m)
			}
		}
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	case "channels":
		var enabled []string
		if cr.config.Channels.Telegram.Enabled {
			enabled = append(enabled, "Telegram")
		}
		if cr.config.Channels.WhatsApp.Enabled {
			enabled = append(enabled, "WhatsApp")
		}
		if cr.config.Channels.Feishu.Enabled {
			enabled = append(enabled, "Feishu")
		}
		if cr.config.Channels.Discord.Enabled {
			enabled = append(enabled, "Discord")
		}
		if cr.config.Channels.Slack.Enabled {
			enabled = append(enabled, "Slack")
		}
		
		text := "Enabled Channels:\n\n"
		if len(enabled) == 0 {
			text += "No channels enabled"
		} else {
			for _, ch := range enabled {
				text += "  " + ch + "\n"
			}
		}
		return cr.sendMessage(ctx, msg.Chat.ID, text)
		
	default:
		return cr.sendMessage(ctx, msg.Chat.ID, "Unknown parameter: "+args+". Try models or channels.")
	}
}

// Helper functions

func normalizeCommandName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	// Remove leading slash if present
	if strings.HasPrefix(trimmed, "/") {
		trimmed = trimmed[1:]
	}
	return strings.ToLower(strings.TrimSpace(trimmed))
}

// parseCommandArgs extracts arguments from a command message
func (cr *CommandRegistry) parseCommandArgs(text string) string {
	parts := strings.SplitN(text, " ", 2)
	if len(parts) < 2 {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

// TelegramCommander is the interface for the old implementation
type TelegramCommander interface {
	Help(ctx context.Context, message telego.Message) error
	Start(ctx context.Context, message telego.Message) error
	Show(ctx context.Context, message telego.Message) error
	List(ctx context.Context, message telego.Message) error
	Reset(ctx context.Context, message telego.Message) error
	Model(ctx context.Context, message telego.Message) error
	Session(ctx context.Context, message telego.Message) error
	Status(ctx context.Context, message telego.Message) error
}

// cmdAdapter adapts CommandRegistry to the old TelegramCommander interface
type cmdAdapter struct {
	registry *CommandRegistry
}

func (a *cmdAdapter) Help(ctx context.Context, message telego.Message) error {
	return a.registry.handleHelp(ctx, message, "")
}

func (a *cmdAdapter) Start(ctx context.Context, message telego.Message) error {
	return a.registry.handleStart(ctx, message, "")
}

func (a *cmdAdapter) Show(ctx context.Context, message telego.Message) error {
	args := a.registry.parseCommandArgs(message.Text)
	return a.registry.handleShow(ctx, message, args)
}

func (a *cmdAdapter) List(ctx context.Context, message telego.Message) error {
	args := a.registry.parseCommandArgs(message.Text)
	return a.registry.handleList(ctx, message, args)
}

func (a *cmdAdapter) Reset(ctx context.Context, message telego.Message) error {
	return a.registry.handleReset(ctx, message, "")
}

func (a *cmdAdapter) Model(ctx context.Context, message telego.Message) error {
	args := a.registry.parseCommandArgs(message.Text)
	return a.registry.handleModel(ctx, message, args)
}

func (a *cmdAdapter) Session(ctx context.Context, message telego.Message) error {
	args := a.registry.parseCommandArgs(message.Text)
	return a.registry.handleSession(ctx, message, args)
}

func (a *cmdAdapter) Status(ctx context.Context, message telego.Message) error {
	return a.registry.handleStatus(ctx, message, "")
}

// NewTelegramCommands creates a new TelegramCommander (backward compatible)
func NewTelegramCommands(bot *telego.Bot, cfg *config.Config, workspace string) TelegramCommander {
	registry := NewCommandRegistry(bot, cfg, workspace)
	return &cmdAdapter{registry: registry}
}
