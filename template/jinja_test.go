package template

import (
	"errors"
	"strings"
	"testing"
)

// graniteLike is the upstream granite shape in miniature: a tools
// block, a message loop, and string literals carrying the tool-call
// markers the native parser keys on.
const graniteLike = `{%- if tools %}
<|im_start|>system
{%- for tool in tools %}
{{ tool | tojson }}
{%- endfor %}
{%- endif %}
{%- for message in messages %}
<|im_start|>{{ message.role }}
{%- if message.tool_calls %}
<tool_call>{{ message.tool_calls[0] | tojson }}</tool_call>
{%- else %}
{{ message.content }}
{%- endif %}
<|im_end|>
{%- endfor %}
{%- if add_generation_prompt %}
<|im_start|>assistant
{%- endif %}`

// chatMLLike is the authored phi-4 shape in miniature: ChatML framing
// with set statements and an inline tool-call emission.
const chatMLLike = `{%- set system_message = '' -%}
{%- if messages and messages[0].role == 'system' -%}
{%- set system_message = messages[0].content -%}
{%- endif -%}
{%- if system_message or tools -%}
{{- '<|im_start|>system<|im_sep|>' + system_message -}}
{%- endif -%}
{%- for message in messages -%}
{{- '<|im_start|>' + message.role + '<|im_sep|>' + message.content + '<|im_end|>' -}}
{%- endfor -%}
{{- '<|im_start|>assistant<|im_sep|>' -}}`

func TestClassifyTemplateJinja(t *testing.T) {
	cases := map[string]string{
		"granite-like":                    graniteLike,
		"chatml-like":                     chatMLLike,
		"expression only raise_exception": `{{ raise_exception('System role not supported') }}`,
		"expression only namespace":       `{% set ns = namespace(x=1) %}{{ ns.x }}`,
		"defined test":                    `{{ messages is defined }}`,
		"raw block with literal tags":     `{% raw %}{{ not_evaluated }}{% if %}{% endraw %}done`,
		"comment hiding a closer":         `{% set x = 1 %}{# a %} in a comment #}{{ x }}`,
		"closer inside a string":          `{{ 'a}}b' }}{% if true %}x{% endif %}`,
		"nested blocks":                   `{% for m in messages %}{% if m.role == 'user' %}{{ m.content }}{% endif %}{% endfor %}`,
	}
	for name, tmpl := range cases {
		t.Run(name, func(t *testing.T) {
			kind, err := ClassifyTemplate(tmpl)
			if err != nil {
				t.Fatalf("ClassifyTemplate: %v", err)
			}
			if kind != KindJinja {
				t.Fatalf("kind = %v, want KindJinja", kind)
			}
		})
	}
}

func TestClassifyTemplateGo(t *testing.T) {
	cases := map[string]string{
		"messages range": `{{ range .Messages }}{{ .Role }}: {{ .Content }}
{{ end }}{{ .Response }}`,
		"prompt":       `{{ .Prompt }}`,
		"with tools":   `{{ range .Tools }}{{ .Function.Name }}{{ end }}{{ range .Messages }}{{ .Content }}{{ end }}`,
		"plain text":   `no actions at all`,
		"percent text": `100% coverage {{ .Prompt }}`,
	}
	for name, tmpl := range cases {
		t.Run(name, func(t *testing.T) {
			kind, err := ClassifyTemplate(tmpl)
			if err != nil {
				t.Fatalf("ClassifyTemplate: %v", err)
			}
			if kind != KindGo {
				t.Fatalf("kind = %v, want KindGo", kind)
			}
		})
	}
}

// TestClassifyBuiltinTemplates pins the built-in library: every
// shipped .gotmpl must keep classifying (and parsing) as Go.
func TestClassifyBuiltinTemplates(t *testing.T) {
	templates, err := templatesOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) == 0 {
		t.Fatal("no built-in templates loaded")
	}
	for _, tmpl := range templates {
		kind, err := ClassifyTemplate(string(tmpl.Bytes))
		if err != nil {
			t.Errorf("%s: %v", tmpl.Name, err)
			continue
		}
		if kind != KindGo {
			t.Errorf("%s: kind = %v, want KindGo", tmpl.Name, kind)
		}
	}
}

func TestValidateJinjaMalformed(t *testing.T) {
	cases := map[string]string{
		"unclosed for":         `{% for m in messages %}{{ m.content }}`,
		"unclosed if":          `{% if tools %}x`,
		"stray endif":          `hello {% endif %}`,
		"mismatched end":       `{% for m in messages %}{% endif %}{% endfor %}`,
		"unterminated expr":    `{{ messages[0].content `,
		"unterminated stmt":    `{% if tools `,
		"unterminated string":  `{{ 'abc }}`,
		"unterminated comment": `{# never closed`,
		"else outside block":   `{% else %}`,
		"elif outside block":   `{% elif x %}`,
		"endraw without raw":   `{% endraw %}`,
		"unclosed raw":         `{% raw %}{{ x }}`,
	}
	for name, tmpl := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateJinja(tmpl)
			if err == nil {
				t.Fatal("ValidateJinja = nil, want error")
			}
			if !errors.Is(err, ErrInvalidJinja) {
				t.Fatalf("error = %v, want ErrInvalidJinja", err)
			}
			kind, classErr := ClassifyTemplate(tmpl)
			if classErr == nil {
				t.Fatalf("ClassifyTemplate(%q) = %v, nil; want error", name, kind)
			}
		})
	}
}

func TestValidateJinjaBounds(t *testing.T) {
	oversized := strings.Repeat("x", MaxJinjaTemplateBytes+1)
	if err := ValidateJinja(oversized); !errors.Is(err, ErrInvalidJinja) {
		t.Fatalf("oversized: err = %v, want ErrInvalidJinja", err)
	}

	manyTags := strings.Repeat("{{ x }}", MaxJinjaStatements+1)
	if err := ValidateJinja(manyTags); !errors.Is(err, ErrInvalidJinja) {
		t.Fatalf("tag flood: err = %v, want ErrInvalidJinja", err)
	}
}

// TestClassifyBrokenGoTemplatePreservesError: a template with no jinja
// marker that fails Go parsing must surface the Go parse error, not a
// jinja error, so existing diagnostics do not move.
func TestClassifyBrokenGoTemplatePreservesError(t *testing.T) {
	_, goErr := Parse(`{{ .Prompt `)
	if goErr == nil {
		t.Fatal("Parse unexpectedly succeeded")
	}

	kind, err := ClassifyTemplate(`{{ .Prompt `)
	if err == nil {
		t.Fatal("ClassifyTemplate = nil error, want Go parse error")
	}
	if errors.Is(err, ErrInvalidJinja) {
		t.Fatalf("error = %v, must not wrap ErrInvalidJinja", err)
	}
	if kind != KindGo {
		t.Fatalf("kind = %v, want KindGo", kind)
	}
	if err.Error() != goErr.Error() {
		t.Fatalf("error = %q, want Go parse error %q", err, goErr)
	}
}
