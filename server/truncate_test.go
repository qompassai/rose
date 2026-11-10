package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/qompassai/rose/api"
	"github.com/qompassai/rose/llm"
	"github.com/qompassai/rose/template"
)

// strictNativeTemplate renders messages as "role: content" lines and
// rejects any window whose first non-system message is a tool result.
// That is the behaviour of the strict GGUF chat templates (gpt-oss,
// devstral) whose rejection of an orphaned tool result surfaced in
// production as a fatal HTTP 500 once truncation cut a tool group in
// half (the Floor 2 deaths, 2026-11-09).
func strictNativeTemplate(_ context.Context, r llm.ChatRequest) (string, error) {
	for _, msg := range r.Messages {
		if msg.Role == "system" {
			continue
		}
		if msg.Role == "tool" {
			return "", errors.New("tool message without a preceding assistant tool call")
		}
		break
	}

	var b strings.Builder
	for _, msg := range r.Messages {
		b.WriteString(msg.Role)
		b.WriteString(": ")
		b.WriteString(msg.Content)
		for _, call := range msg.ToolCalls {
			b.WriteString(" call:")
			b.WriteString(call.Function.Name)
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

func toolGroupHistory() []api.Message {
	return []api.Message{
		{Role: "user", Content: strings.Repeat("word ", 30)},
		{Role: "assistant", ToolCalls: []api.ToolCall{
			{ID: "call_1", Function: api.ToolCallFunction{Name: "lookup"}},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "result payload here"},
		{Role: "user", Content: "the real question"},
		{Role: "assistant", Content: "short answer"},
		{Role: "user", Content: "final ask"},
	}
}

// Rendered field counts for toolGroupHistory under strictNativeTemplate
// and mockRunner.Tokenize (one token per whitespace-separated field):
// window@0=47, @1=16, @2=14, @3=10, @4=6, @5=3.

func TestTruncateNativeChatMessagesSkipsOrphanedToolResult(t *testing.T) {
	runner := &mockRunner{TemplateFn: strictNativeTemplate}
	opts := &api.Options{Runner: api.Runner{NumCtx: 12}}

	got, err := truncateNativeChatMessages(t.Context(), nil, runner, opts, llm.ChatRequest{Messages: toolGroupHistory()}, true)
	if err != nil {
		t.Fatalf("truncation must advance past the orphaned tool result, got error: %v", err)
	}

	// The first fitting naive cut is index 2 (the tool result); the
	// pair-aware cut must land on index 3 instead.
	if len(got) != 3 {
		t.Fatalf("len(messages) = %d, want 3 (cut at index 3)", len(got))
	}
	if got[0].Role != "user" || got[0].Content != "the real question" {
		t.Errorf("window starts with %q %q, want the user message at index 3", got[0].Role, got[0].Content)
	}
	if got[len(got)-1].Content != "final ask" {
		t.Errorf("last message = %q, want the final user message preserved", got[len(got)-1].Content)
	}
}

func TestTruncateNativeChatMessagesFallbackKeepsToolGroupWhole(t *testing.T) {
	runner := &mockRunner{TemplateFn: strictNativeTemplate}
	opts := &api.Options{Runner: api.Runner{NumCtx: 1}}

	// The agent-loop shape: the request ends on a tool result. Nothing
	// fits, so the fallback decides — it must keep the assistant parent
	// with its result instead of the result alone.
	msgs := toolGroupHistory()[:3]
	got, err := truncateNativeChatMessages(t.Context(), nil, runner, opts, llm.ChatRequest{Messages: msgs}, true)
	if err != nil {
		t.Fatalf("fallback window must render, got error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(messages) = %d, want 2 (assistant parent + tool result)", len(got))
	}
	if got[0].Role != "assistant" || got[1].Role != "tool" {
		t.Errorf("fallback window = [%s, %s], want [assistant, tool]", got[0].Role, got[1].Role)
	}
}

func TestTruncateNativeChatMessagesRenderErrorAtLastCandidateIsFatal(t *testing.T) {
	runner := &mockRunner{TemplateFn: func(context.Context, llm.ChatRequest) (string, error) {
		return "", errors.New("template is broken")
	}}
	opts := &api.Options{Runner: api.Runner{NumCtx: 100}}
	msgs := []api.Message{
		{Role: "user", Content: "one"},
		{Role: "user", Content: "two"},
	}

	_, err := truncateNativeChatMessages(t.Context(), nil, runner, opts, llm.ChatRequest{Messages: msgs}, true)
	if err == nil {
		t.Fatal("a template that rejects every candidate must surface its error once no fallback remains")
	}
}

func TestTruncateNativeChatMessagesKeepsSystemAndFittingHistory(t *testing.T) {
	runner := &mockRunner{TemplateFn: strictNativeTemplate}
	opts := &api.Options{Runner: api.Runner{NumCtx: 100}}
	msgs := []api.Message{
		{Role: "system", Content: "be terse"},
		{Role: "user", Content: "hello there"},
		{Role: "assistant", Content: "general kenobi"},
	}

	got, err := truncateNativeChatMessages(t.Context(), nil, runner, opts, llm.ChatRequest{Messages: msgs}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("fitting history changed: got %d messages, want %d", len(got), len(msgs))
	}
	if got[0].Role != "system" {
		t.Errorf("system message lost, window starts with %q", got[0].Role)
	}
}

func TestChatPromptNeverBeginsWindowWithToolResult(t *testing.T) {
	tmpl, err := template.Parse(`{{ range .Messages }}{{ .Role }}: {{ .Content }}{{ range .ToolCalls }} call:{{ .Function.Name }}{{ end }};{{ end }}`)
	if err != nil {
		t.Fatal(err)
	}
	m := Model{Template: tmpl}
	// Rendered field counts under this template (fields merge across
	// the ";" separators): window@1=12, @2=11, @3=8. NumCtx 11 makes
	// the tool result at index 2 the first fitting naive cut.
	opts := api.Options{Runner: api.Runner{NumCtx: 11}}
	think := false

	// Same shape as the native test: the first fitting naive cut is
	// the tool result at index 2.
	prompt, _, err := chatPrompt(t.Context(), &m, mockRunner{}.Tokenize, &opts, toolGroupHistory(), nil, &api.ThinkValue{Value: think}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(prompt, "result payload here") {
		t.Errorf("prompt kept the tool result while its assistant parent was cut: %q", prompt)
	}
	if !strings.Contains(prompt, "final ask") {
		t.Errorf("prompt lost the final message: %q", prompt)
	}
}

func TestChatPromptKeepsToolGroupWholeWhenItFits(t *testing.T) {
	tmpl, err := template.Parse(`{{ range .Messages }}{{ .Role }}: {{ .Content }}{{ range .ToolCalls }} call:{{ .Function.Name }}{{ end }};{{ end }}`)
	if err != nil {
		t.Fatal(err)
	}
	m := Model{Template: tmpl}
	opts := api.Options{Runner: api.Runner{NumCtx: 16}}
	think := false

	prompt, _, err := chatPrompt(t.Context(), &m, mockRunner{}.Tokenize, &opts, toolGroupHistory(), nil, &api.ThinkValue{Value: think}, true)
	if err != nil {
		t.Fatal(err)
	}
	parent := strings.Index(prompt, "call:lookup")
	result := strings.Index(prompt, "result payload here")
	if parent < 0 || result < 0 {
		t.Fatalf("fitting tool group must survive whole, prompt: %q", prompt)
	}
	if parent > result {
		t.Errorf("tool result precedes its assistant parent in prompt: %q", prompt)
	}
}
