package tools

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/qompassai/rose/api"
)

func TestFencedCalls(t *testing.T) {
	tools := []api.Tool{
		{
			Type: "function",
			Function: api.ToolFunction{
				Name: "run_shell",
				Parameters: api.ToolFunctionParameters{
					Type:     "object",
					Required: []string{"command"},
					Properties: testPropsMap(map[string]api.ToolProperty{
						"command": {Type: api.PropertyType{"string"}},
					}),
				},
			},
		},
		{
			Type: "function",
			Function: api.ToolFunction{
				Name: "get_weather",
				Parameters: api.ToolFunctionParameters{
					Type:     "object",
					Required: []string{"city"},
					Properties: testPropsMap(map[string]api.ToolProperty{
						"city": {Type: api.PropertyType{"string"}},
						"unit": {Type: api.PropertyType{"string"}},
					}),
				},
			},
		},
		{
			Type: "function",
			Function: api.ToolFunction{
				Name: "list_tasks",
				Parameters: api.ToolFunctionParameters{
					Type: "object",
					Properties: testPropsMap(map[string]api.ToolProperty{
						"status": {Type: api.PropertyType{"string"}},
					}),
				},
			},
		},
	}

	call := func(index int, name string, args map[string]any) api.ToolCall {
		return api.ToolCall{Function: api.ToolCallFunction{Index: index, Name: name, Arguments: testArgs(args)}}
	}

	tests := []struct {
		name    string
		tools   []api.Tool
		content string
		want    []api.ToolCall
	}{
		// promoted
		{
			name:    "phi-4 compact array after prose (captured live)",
			tools:   tools,
			content: "To list the files in the current directory, I will execute a shell command that retrieves this information. Here is the function call to achieve that:\n\n```json\n[{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}]\n```",
			want:    []api.ToolCall{call(0, "run_shell", map[string]any{"command": "ls"})},
		},
		{
			name:    "phi-4 pretty-printed array after prose (captured live)",
			tools:   tools,
			content: "To check the free disk space on this machine, I can run a shell command that retrieves this information. Here's how I'll proceed:\n\n```json\n[\n  {\n    \"name\": \"run_shell\",\n    \"arguments\": {\n      \"command\": \"df -h\"\n    }\n  }\n]\n```",
			want:    []api.ToolCall{call(0, "run_shell", map[string]any{"command": "df -h"})},
		},
		{
			name:    "single object, no prose, trailing whitespace",
			tools:   tools,
			content: "```json\n{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Paris\", \"unit\": \"celsius\"}}\n```\n\n",
			want:    []api.ToolCall{call(0, "get_weather", map[string]any{"city": "Paris", "unit": "celsius"})},
		},
		{
			name:    "two calls in one array keep order",
			tools:   tools,
			content: "```json\n[{\"name\": \"get_weather\", \"arguments\": {\"city\": \"Oslo\"}}, {\"name\": \"run_shell\", \"arguments\": {\"command\": \"date\"}}]\n```",
			want: []api.ToolCall{
				call(0, "get_weather", map[string]any{"city": "Oslo"}),
				call(1, "run_shell", map[string]any{"command": "date"}),
			},
		},
		{
			name:    "empty arguments for all-optional tool",
			tools:   tools,
			content: "```json\n{\"name\": \"list_tasks\", \"arguments\": {}}\n```",
			want:    []api.ToolCall{call(0, "list_tasks", map[string]any{})},
		},
		// stays text
		{
			name:    "no tools provided",
			tools:   nil,
			content: "```json\n[{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}]\n```",
		},
		{
			name:    "unknown tool name",
			tools:   tools,
			content: "```json\n[{\"name\": \"delete_everything\", \"arguments\": {\"command\": \"ls\"}}]\n```",
		},
		{
			name:    "tool name prefix is not a match",
			tools:   tools,
			content: "```json\n{\"name\": \"run\", \"arguments\": {\"command\": \"ls\"}}\n```",
		},
		{
			name:    "ordinary non-json code fence",
			tools:   tools,
			content: "Here is how to do it in Go:\n\n```go\nfmt.Println(\"hi\")\n```",
		},
		{
			name:    "json fence that is not a tool call",
			tools:   tools,
			content: "Your config should look like:\n\n```json\n{\"port\": 8080, \"debug\": true}\n```",
		},
		{
			name:    "json fence with tool name but extra keys",
			tools:   tools,
			content: "```json\n{\"name\": \"run_shell\", \"version\": 2, \"arguments\": {\"command\": \"ls\"}}\n```",
		},
		{
			name:    "plain prose with a python fence mentioning a tool",
			tools:   tools,
			content: "You could call run_shell yourself:\n\n```python\nrun_shell({\"command\": \"ls\"})\n```",
		},
		{
			name:    "malformed json fence",
			tools:   tools,
			content: "```json\n[{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}]\n```",
		},
		{
			name:    "trailing data after json value",
			tools:   tools,
			content: "```json\n{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}} {\"name\": \"run_shell\", \"arguments\": {\"command\": \"rm -rf /\"}}\n```",
		},
		{
			name:    "fence followed by more prose",
			tools:   tools,
			content: "For example:\n\n```json\n{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}\n```\n\nThat is what a call would look like.",
		},
		{
			name:    "two fences in one reply",
			tools:   tools,
			content: "```bash\nls\n```\n\n```json\n{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}\n```",
		},
		{
			name:    "unclosed fence",
			tools:   tools,
			content: "```json\n{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}",
		},
		{
			name:    "fence opened mid-line",
			tools:   tools,
			content: "call: ```json\n{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}\n```",
		},
		{
			name:    "missing required argument",
			tools:   tools,
			content: "```json\n{\"name\": \"get_weather\", \"arguments\": {\"unit\": \"celsius\"}}\n```",
		},
		{
			name:    "unknown argument",
			tools:   tools,
			content: "```json\n{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\", \"sudo\": true}}\n```",
		},
		{
			name:    "missing arguments object",
			tools:   tools,
			content: "```json\n{\"name\": \"list_tasks\"}\n```",
		},
		{
			name:    "stringified arguments",
			tools:   tools,
			content: "```json\n{\"name\": \"run_shell\", \"arguments\": \"{\\\"command\\\": \\\"ls\\\"}\"}\n```",
		},
		{
			name:    "one invalid call poisons the array",
			tools:   tools,
			content: "```json\n[{\"name\": \"run_shell\", \"arguments\": {\"command\": \"ls\"}}, {\"name\": \"nope\", \"arguments\": {}}]\n```",
		},
		{
			name:    "empty array",
			tools:   tools,
			content: "```json\n[]\n```",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FencedCalls(tt.tools, tt.content)
			if diff := cmp.Diff(tt.want, got, argsComparer); diff != "" {
				t.Errorf("FencedCalls mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
