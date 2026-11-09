package tools

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/qompassai/rose/api"
)

// fencedCall is the only accepted shape inside a fence: exactly a name and an
// arguments object, as phi-4 writes {"name": ..., "arguments": {...}}.
type fencedCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// FencedCalls returns the tool calls in a complete reply whose only code
// fence is a terminal ```json block holding one call object or an array of
// them, e.g. phi-4 writing prose followed by
//
//	```json
//	[{"name": "run_shell", "arguments": {"command": "ls"}}]
//	```
//
// instead of its functools[...] form. It returns nil, leaving the reply as
// text, unless tools were provided, the reply has exactly one fence, that
// fence is tagged json and ends the reply, its body is strict JSON in the
// shape above, and every call names a provided tool exactly with an arguments
// object that has all required and no unknown parameters. All calls promote or
// none do.
func FencedCalls(tools []api.Tool, content string) []api.ToolCall {
	if len(tools) == 0 {
		return nil
	}

	const fence = "```"
	const open = fence + "json\n"
	reply := strings.TrimRight(content, " \t\r\n")
	if strings.Count(reply, fence) != 2 || !strings.HasSuffix(reply, fence) {
		return nil
	}
	start := strings.Index(reply, open)
	if start == -1 || (start > 0 && reply[start-1] != '\n') {
		return nil
	}
	body := reply[start+len(open) : len(reply)-len(fence)]

	var parsed []fencedCall
	trimmed := strings.TrimSpace(body)
	if strings.HasPrefix(trimmed, "[") {
		if !decodeStrict(trimmed, &parsed) {
			return nil
		}
	} else {
		var one fencedCall
		if !decodeStrict(trimmed, &one) {
			return nil
		}
		parsed = []fencedCall{one}
	}
	if len(parsed) == 0 {
		return nil
	}

	calls := make([]api.ToolCall, 0, len(parsed))
	for i, fc := range parsed {
		tool := providedTool(tools, fc.Name)
		if tool == nil || fc.Arguments == nil || !argumentsFit(tool, fc.Arguments) {
			return nil
		}
		args := api.NewToolCallFunctionArguments()
		for k, v := range fc.Arguments {
			args.Set(k, v)
		}
		calls = append(calls, api.ToolCall{
			Function: api.ToolCallFunction{
				Name:      tool.Function.Name,
				Arguments: args,
				Index:     i,
			},
		})
	}
	return calls
}

// decodeStrict decodes s into v, rejecting unknown fields and trailing data.
func decodeStrict(s string, v any) bool {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return false
	}
	_, err := dec.Token()
	return err == io.EOF
}

func providedTool(tools []api.Tool, name string) *api.Tool {
	for i := range tools {
		if tools[i].Function.Name == name {
			return &tools[i]
		}
	}
	return nil
}

// argumentsFit reports whether args carries every required parameter and,
// when the tool declares properties, no parameter outside them.
func argumentsFit(tool *api.Tool, args map[string]any) bool {
	params := tool.Function.Parameters
	for _, name := range params.Required {
		if _, ok := args[name]; !ok {
			return false
		}
	}
	if params.Properties == nil {
		return true
	}
	for name := range args {
		if _, ok := params.Properties.Get(name); !ok {
			return false
		}
	}
	return true
}
