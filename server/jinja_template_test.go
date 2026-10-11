package server

import (
	"slices"
	"testing"

	"github.com/qompassai/rose/types/model"
)

// suppliedToolJinja references tools the way upstream chat templates
// do, which is what chatTemplateCapabilities keys on.
const suppliedToolJinja = `{%- if tools %}{% for tool in tools %}{{ tool }}{% endfor %}{% endif %}{% for message in messages %}{{ message.role }}: {{ message.content }}{% endfor %}`

const suppliedPlainJinja = `{% for message in messages %}{{ message.role }}: {{ message.content }}{% endfor %}`

func TestEffectiveChatTemplatePrefersSupplied(t *testing.T) {
	m := &Model{metadata: ggufMetadata{KV: map[string]any{
		"general.architecture":    "granite",
		"tokenizer.chat_template": "gguf template",
	}}}
	if got := m.effectiveChatTemplate(); got != "gguf template" {
		t.Fatalf("effectiveChatTemplate = %q, want GGUF template", got)
	}

	m.chatTemplate = "supplied template"
	if got := m.effectiveChatTemplate(); got != "supplied template" {
		t.Fatalf("effectiveChatTemplate = %q, want supplied template", got)
	}
}

// TestSuppliedChatTemplateCapabilities is the regression test for the
// 2026-10 tool refusal: a model whose GGUF carries no chat template
// must still report CapabilityTools when the supplied jinja template
// references tools, or the chat handler refuses tool calls.
func TestSuppliedChatTemplateCapabilities(t *testing.T) {
	m := &Model{
		ModelPath:    "granite.gguf",
		chatTemplate: suppliedToolJinja,
	}
	if caps := m.Capabilities(); !slices.Contains(caps, model.CapabilityTools) {
		t.Fatalf("Capabilities = %v, want tools capability from supplied template", caps)
	}

	m.chatTemplate = suppliedPlainJinja
	if caps := m.Capabilities(); slices.Contains(caps, model.CapabilityTools) {
		t.Fatalf("Capabilities = %v, must not claim tools for a tool-less supplied template", caps)
	}
}

func TestLlamaServerConfigForModelSuppliedTemplate(t *testing.T) {
	m := &Model{chatTemplatePath: "/models/blobs/sha256-abc"}
	if got := llamaServerConfigForModel(m).ChatTemplateFile; got != "/models/blobs/sha256-abc" {
		t.Fatalf("ChatTemplateFile = %q, want the supplied template blob path", got)
	}

	m.chatTemplatePath = ""
	if got := llamaServerConfigForModel(m).ChatTemplateFile; got != "" {
		t.Fatalf("ChatTemplateFile = %q, want empty without a supplied template", got)
	}
}
