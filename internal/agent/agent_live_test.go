//go:build live
// +build live

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	openai "github.com/sashabaranov/go-openai"
)

func newOpenRouterClient() *openai.Client {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		panic("OPENROUTER_API_KEY not set")
	}
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = "https://openrouter.ai/api/v1"
	return openai.NewClientWithConfig(cfg)
}

func TestAgent_RunLoop_Live(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set; skipping live test")
	}
	maskedKey := "[set]..." + apiKey[len(apiKey)-4:]
	fmt.Printf("[Test Debug] Using OPENROUTER_API_KEY: %s\n", maskedKey)

	client := newOpenRouterClient()
	model := "openai/gpt-4o-mini"
	agent := NewAgent(client, model, 3)

	echoFunc := openai.FunctionDefinition{
		Name:        "echo",
		Description: "Echoes the input string",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to echo",
				},
			},
			"required": []string{"message"},
		},
	}
	echoTool := openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &echoFunc,
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: "You are an echo bot. Only call the echo function with the user's message.",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello, world!",
		},
	}

	validator := func(toolCall openai.ToolCall) (interface{}, error) {
		if toolCall.Function.Name != "echo" {
			return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
		}
		msg := toolCall.Function.Arguments
		return msg, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := agent.RunLoop(ctx, messages, []openai.Tool{echoTool}, validator, 3)
	if err != nil {
		t.Fatalf("RunLoop failed: %v", err)
	}
	t.Logf("Echo tool call result: %v", result)
}

func TestAgent_BasicCompletion_Live(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set; skipping live test")
	}
	maskedKey := "[set]..." + apiKey[len(apiKey)-4:]
	fmt.Printf("[Test Debug] Using OPENROUTER_API_KEY: %s\n", maskedKey)

	client := newOpenRouterClient()
	model := "openai/gpt-3.5-turbo"

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "Hello!",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    model,
		Messages: messages,
	})
	if err != nil {
		t.Fatalf("Basic completion failed: %v", err)
	}
	respJSON, _ := json.MarshalIndent(resp, "", "  ")
	t.Logf("Basic completion response: %s", respJSON)
}

func TestAgent_WeatherToolCall_Live(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set; skipping live test")
	}
	maskedKey := "[set]..." + apiKey[len(apiKey)-4:]
	fmt.Printf("[Test Debug] Using OPENROUTER_API_KEY: %s\n", maskedKey)

	client := newOpenRouterClient()
	model := "openai/gpt-4o"
	agent := NewAgent(client, model, 3)

	weatherFunc := openai.FunctionDefinition{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"location": map[string]any{
					"type":        "string",
					"description": "The city and state, e.g. San Francisco, CA",
				},
				"unit": map[string]any{
					"type": "string",
					"enum": []string{"celsius", "fahrenheit"},
				},
			},
			"required": []string{"location"},
		},
	}
	weatherTool := openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &weatherFunc,
	}

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "What's the weather like in London?",
		},
	}

	validator := func(toolCall openai.ToolCall) (interface{}, error) {
		if toolCall.Function.Name != "get_current_weather" {
			return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
		}
		return toolCall.Function.Arguments, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := agent.RunLoop(ctx, messages, []openai.Tool{weatherTool}, validator, 3)
	if err != nil {
		t.Fatalf("RunLoop failed: %v", err)
	}
	t.Logf("Weather tool call result: %v", result)
}
