package agent

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	openai "github.com/sashabaranov/go-openai"
	"golang.org/x/exp/slices"
)

// ToolCallValidator is a function that validates and parses the tool call arguments.
// It should return (parsedResult, nil) on success, or (nil, error) on failure.
type ToolCallValidator func(toolCall openai.ToolCall) (any, error)

// ShouldStopFunc determines if the tool call is a terminal/final action.
type ShouldStopFunc func(toolCall openai.ToolCall) bool

// Agent encapsulates OpenAI tool-calling logic.
type Agent struct {
	logger      *log.Logger
	client      *openai.Client
	model       string
	maxAttempts int
}

// NewAgent creates a new Agent for tool-calling.
func NewAgent(logger *log.Logger, client *openai.Client, model string, maxAttempts int) *Agent {
	return &Agent{
		logger:      logger,
		client:      client,
		model:       model,
		maxAttempts: maxAttempts,
	}
}

// NewOpenRouterAgent creates an Agent configured for OpenRouter's OpenAI-compatible API.
// apiKey: your OpenRouter API key
// model: the model name to use (e.g., "google/gemini-2.5-flash-preview")
// maxAttempts: number of tool-calling retry attempts
func NewOpenRouterAgent(logger *log.Logger, apiKey, model string, maxAttempts int) *Agent {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "https://openrouter.ai/api/v1"
	client := openai.NewClientWithConfig(cfg)
	return NewAgent(logger, client, model, maxAttempts)
}

// RunLoop performs iterative tool-calling with error handling and a max loop count.
// Returns the parsed result from the validator, or an error if all attempts fail.
func (a *Agent) RunLoop(
	ctx context.Context,
	initialMessages []openai.ChatCompletionMessage,
	tools []openai.Tool,
	validator ToolCallValidator,
	shouldStop ShouldStopFunc,
	maxLoop int,
) (any, error) {
	var (
		lastToolCall string
		lastError    error
		chatMessages = slices.Clone(initialMessages)
	)

	for loop := 1; loop <= maxLoop; loop++ {
		a.logger.Debug("Running agent loop", "loop", loop)

		resp, err := a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:      a.model,
			Messages:   chatMessages,
			Tools:      tools,
			ToolChoice: "auto",
		})
		if err != nil {
			lastError = err
			continue
		}

		if len(resp.Choices) == 0 {
			lastError = fmt.Errorf("no choices in response")
			continue
		}

		message := resp.Choices[0].Message
		if len(message.ToolCalls) == 0 {
			lastError = fmt.Errorf("no tool calls in response")
			continue
		}

		toolCall := message.ToolCalls[0]
		lastToolCall = toolCall.Function.Arguments

		parsed, err := validator(toolCall)
		if err == nil {
			a.logger.Debug("Tool call validated successfully", "toolCall", toolCall)
			if shouldStop == nil || shouldStop(toolCall) {
				return parsed, nil
			}
			// Add the tool result as a new message and continue the loop
			chatMessages = append(chatMessages, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleTool,
				Content: fmt.Sprintf("Tool result: %v", parsed),
				Name:    toolCall.Function.Name,
			})
			continue
		}
		a.logger.Debug("Tool call validation failed", "toolCall", toolCall, "error", err)
		lastError = err

		// On error, add the previous tool call and error as a new user message
		msg := ""
		if lastToolCall != "" {
			msg += "Previous tool call arguments:\n" + lastToolCall + "\n"
		}
		msg += "Error: " + lastError.Error() + "\n"
		msg += "Please correct your response using only allowed values."
		chatMessages = append(chatMessages, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleUser,
			Content: msg,
		})
	}

	return nil, fmt.Errorf("failed to get valid tool call after %d attempts: %w", maxLoop, lastError)
}
