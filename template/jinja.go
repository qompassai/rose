package template

import (
	"errors"
	"fmt"
	"strings"
)

// Jinja templates (the HuggingFace chat_template.jinja format) are not
// rendered by this package: llama-server renders them on the native
// chat path. This file only classifies a supplied template as Go or
// jinja and structurally validates jinja candidates, so a malformed
// jinja template fails at model-create time with a clean error instead
// of at runner start. Semantic validation (unknown filters, bad
// expressions) stays with llama.cpp, the same trust boundary as
// GGUF-embedded chat templates.

const (
	// MaxJinjaTemplateBytes bounds a supplied jinja template. The
	// largest production template in the field (gpt-oss harmony) is
	// under 17 KiB; 4 MiB leaves generous headroom while keeping
	// validation work bounded.
	MaxJinjaTemplateBytes = 4 << 20

	// MaxJinjaStatements bounds the number of jinja tags (expressions,
	// statements, and comments) in one template.
	MaxJinjaStatements = 10000
)

// ErrInvalidJinja is returned, wrapped with positional detail, when a
// jinja candidate fails structural validation.
var ErrInvalidJinja = errors.New("invalid jinja template")

// TemplateKind identifies the language a supplied template is written in.
type TemplateKind int

const (
	KindGo TemplateKind = iota
	KindJinja
)

// jinjaExpressionMarkers are constructs that only appear in jinja
// expressions. They classify expression-only templates (no {% %}
// statement tags) that Go parsing would otherwise reject with a
// misleading error.
var jinjaExpressionMarkers = []string{
	"raise_exception(",
	"namespace(",
	" is defined",
	" is not defined",
}

// LooksJinja reports whether s carries an unambiguous jinja marker: a
// {% statement tag, a {# comment tag, or an expression-only jinja
// construct. Go text/template has no {% or {# syntax, so the markers
// cannot collide with a Go action; they can only collide with literal
// {% or {# text in a Go template's output, which chat templates do
// not emit.
func LooksJinja(s string) bool {
	if strings.Contains(s, "{%") || strings.Contains(s, "{#") {
		return true
	}
	for _, marker := range jinjaExpressionMarkers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}

// ClassifyTemplate reports whether s is a Go template or a jinja
// template, validating the candidate either way. A Go parse failure is
// returned unchanged so existing error text for broken Go templates
// does not move; a jinja failure wraps ErrInvalidJinja. On error the
// returned kind is the classification that was attempted.
func ClassifyTemplate(s string) (TemplateKind, error) {
	if LooksJinja(s) {
		if err := ValidateJinja(s); err != nil {
			return KindJinja, err
		}
		return KindJinja, nil
	}

	if _, err := Parse(s); err != nil {
		return KindGo, err
	}
	return KindGo, nil
}

// jinjaBlock pairs a block-opening keyword with its closing keyword.
var jinjaBlocks = map[string]string{
	"for":    "endfor",
	"if":     "endif",
	"block":  "endblock",
	"macro":  "endmacro",
	"filter": "endfilter",
	"raw":    "endraw",
}

// jinjaEndBlocks is the reverse of jinjaBlocks.
var jinjaEndBlocks = func() map[string]string {
	m := make(map[string]string, len(jinjaBlocks))
	for open, end := range jinjaBlocks {
		m[end] = open
	}
	return m
}()

type jinjaFrame struct {
	keyword string
	offset  int
}

// ValidateJinja structurally checks a jinja template: size and tag
// bounds, delimiter balance (string-literal aware), and block nesting.
// It does not evaluate expressions. It returns nil for a structurally
// sound template and an ErrInvalidJinja-wrapping error otherwise.
func ValidateJinja(s string) error {
	if len(s) > MaxJinjaTemplateBytes {
		return fmt.Errorf("%w: %d bytes exceeds limit of %d bytes", ErrInvalidJinja, len(s), MaxJinjaTemplateBytes)
	}

	var stack []jinjaFrame
	tags := 0
	for i := 0; i < len(s); {
		open, kind := nextJinjaOpener(s, i)
		if open < 0 {
			break
		}
		tags++
		if tags > MaxJinjaStatements {
			return fmt.Errorf("%w: more than %d tags", ErrInvalidJinja, MaxJinjaStatements)
		}

		// Inside a raw block everything is literal until {% endraw %}:
		// skip non-statement tags, and consume statement tags without
		// folding them into the stack unless they close the block.
		if len(stack) > 0 && stack[len(stack)-1].keyword == "raw" {
			if kind != "statement" {
				i = open + 2
				continue
			}
			content, next, err := scanJinjaTag(s, open, kind)
			if err != nil {
				return err
			}
			i = next
			if jinjaStatementKeyword(content) == "endraw" {
				stack = stack[:len(stack)-1]
			}
			continue
		}

		content, next, err := scanJinjaTag(s, open, kind)
		if err != nil {
			return err
		}
		i = next

		if kind != "statement" {
			continue
		}
		stack, err = applyJinjaStatement(stack, content, open)
		if err != nil {
			return err
		}
	}

	if len(stack) > 0 {
		top := stack[len(stack)-1]
		return fmt.Errorf("%w: unclosed {%% %s %%} block opened at byte %d", ErrInvalidJinja, top.keyword, top.offset)
	}
	return nil
}

// nextJinjaOpener finds the next {{, {%, or {# at or after from,
// returning its offset and kind ("expression", "statement", or
// "comment"), or -1 when the rest of s is literal text.
func nextJinjaOpener(s string, from int) (int, string) {
	best, kind := -1, ""
	for _, candidate := range []struct {
		marker string
		kind   string
	}{
		{"{{", "expression"},
		{"{%", "statement"},
		{"{#", "comment"},
	} {
		if idx := strings.Index(s[from:], candidate.marker); idx >= 0 {
			if at := from + idx; best < 0 || at < best {
				best, kind = at, candidate.kind
			}
		}
	}
	return best, kind
}

// scanJinjaTag returns the content of the tag starting at open (the
// offset of its two-character opener) and the offset just past its
// closer. Quoted strings inside expressions and statements are skipped
// so a closer inside a string literal does not end the tag. Comments
// are scanned literally: jinja comment text is not string-parsed.
func scanJinjaTag(s string, open int, kind string) (string, int, error) {
	closer := map[string]string{
		"expression": "}}",
		"statement":  "%}",
		"comment":    "#}",
	}[kind]

	start := open + 2
	if kind == "comment" {
		idx := strings.Index(s[start:], closer)
		if idx < 0 {
			return "", 0, fmt.Errorf("%w: unterminated {# comment at byte %d", ErrInvalidJinja, open)
		}
		return s[start : start+idx], start + idx + 2, nil
	}

	var quote byte
	for i := start; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			switch {
			case c == '\\':
				i++ // skip the escaped character
			case c == quote:
				quote = 0
			}
			continue
		}
		switch {
		case c == '\'' || c == '"':
			quote = c
		case c == closer[0] && i+1 < len(s) && s[i+1] == closer[1]:
			return s[start:i], i + 2, nil
		}
	}

	if quote != 0 {
		return "", 0, fmt.Errorf("%w: unterminated string in tag at byte %d", ErrInvalidJinja, open)
	}
	return "", 0, fmt.Errorf("%w: unterminated tag at byte %d", ErrInvalidJinja, open)
}

// applyJinjaStatement folds one {% %} statement into the block stack.
// content is the raw tag body; offset is the tag's byte offset in the
// template, used in error messages.
func applyJinjaStatement(stack []jinjaFrame, content string, offset int) ([]jinjaFrame, error) {
	keyword := jinjaStatementKeyword(content)
	switch {
	case jinjaBlocks[keyword] != "":
		return append(stack, jinjaFrame{keyword: keyword, offset: offset}), nil
	case jinjaEndBlocks[keyword] != "":
		want := jinjaEndBlocks[keyword]
		if len(stack) == 0 || stack[len(stack)-1].keyword != want {
			return nil, fmt.Errorf("%w: {%% %s %%} at byte %d does not close an open %s block", ErrInvalidJinja, keyword, offset, want)
		}
		return stack[:len(stack)-1], nil
	case keyword == "elif" || keyword == "else":
		if len(stack) == 0 || (stack[len(stack)-1].keyword != "if" && stack[len(stack)-1].keyword != "for") {
			return nil, fmt.Errorf("%w: {%% %s %%} at byte %d outside an if/for block", ErrInvalidJinja, keyword, offset)
		}
		return stack, nil
	default:
		return stack, nil
	}
}

// jinjaStatementKeyword extracts the leading keyword of a {% %}
// statement body, tolerating whitespace-control dashes ({%- ... -%}).
func jinjaStatementKeyword(content string) string {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "-")
	content = strings.TrimSpace(content)
	if idx := strings.IndexAny(content, " \t\r\n("); idx >= 0 {
		content = content[:idx]
	}
	return content
}
