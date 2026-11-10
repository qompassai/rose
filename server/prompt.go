package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/qompassai/rose/api"
	"github.com/qompassai/rose/llm"
	"github.com/qompassai/rose/model/renderers"
	"github.com/qompassai/rose/template"
)

type tokenizeFunc func(context.Context, string) ([]int, error)

// chatPrompt accepts a list of messages and returns the prompt and media that should be used for the next chat turn.
// chatPrompt truncates any messages that exceed the context window of the model, making sure to always include 1) the
// latest message and 2) system messages
func chatPrompt(ctx context.Context, m *Model, tokenize tokenizeFunc, opts *api.Options, msgs []api.Message, tools []api.Tool, think *api.ThinkValue, truncate bool) (prompt string, media []llm.MediaData, _ error) {
	var system []api.Message

	// TODO: This is only a truncation heuristic; llama-server handles the
	// actual image/media inputs. Replace this with projector/model-aware media
	// token accounting so image history is neither over-packed nor over-trimmed.
	// Clip images are represented as 768 tokens, each an embedding.
	imageNumTokens := 768

	lastMsgIdx := len(msgs) - 1
	currMsgIdx := 0

	if truncate {
		// A truncated window may not begin with a tool result: the
		// assistant message carrying the matching tool calls would be
		// dropped, and strict chat templates reject the orphaned
		// result outright. Cut points therefore land only on non-tool
		// messages, so a tool group is kept or dropped whole.
		// lastCutIdx is the pair-aware fallback — the latest legal
		// cut, used when no candidate fits; it keeps the final tool
		// group whole where the old code kept only the final message.
		lastCutIdx := lastMsgIdx
		for lastCutIdx > 0 && msgs[lastCutIdx].Role == "tool" {
			lastCutIdx--
		}

		// Start with all messages and remove from the front until it fits in context
		currMsgIdx = -1
		for i := 0; i <= lastCutIdx; i++ {
			if msgs[i].Role == "tool" {
				continue
			}

			// Collect system messages from the portion we're about to skip
			system = make([]api.Message, 0)
			for j := range i {
				if msgs[j].Role == "system" {
					system = append(system, msgs[j])
				}
			}

			p, err := renderPrompt(m, append(system, msgs[i:]...), tools, think)
			if err != nil {
				// A candidate window the template rejects is an
				// invalid candidate, not a fatal error: keep
				// advancing. Only the last legal candidate's render
				// failure is real — no fallback remains.
				if i == lastCutIdx {
					return "", nil, err
				}
				continue
			}

			s, err := tokenize(ctx, p)
			if err != nil {
				return "", nil, err
			}

			ctxLen := len(s)
			if m.ProjectorPaths != nil {
				for _, msg := range msgs[i:] {
					ctxLen += imageNumTokens * len(msg.Images)
				}
			}

			if ctxLen <= opts.NumCtx {
				currMsgIdx = i
				break
			}
		}

		if currMsgIdx < 0 {
			currMsgIdx = lastCutIdx
		}

		// The loop leaves system holding its last rendered candidate's
		// collection; recollect for the cut actually chosen, which the
		// fallback path may not have rendered.
		system = make([]api.Message, 0)
		for j := range currMsgIdx {
			if msgs[j].Role == "system" {
				system = append(system, msgs[j])
			}
		}
	}

	if currMsgIdx > 0 {
		slog.Debug("truncating input messages which exceed context length", "truncated", len(msgs[currMsgIdx:]))
	}

	renderMsgs, media, err := imageTaggedMessages(m, msgs, currMsgIdx, false)
	if err != nil {
		return "", nil, err
	}

	// truncate any messages that do not fit into the context window
	p, err := renderPrompt(m, append(system, renderMsgs[currMsgIdx:]...), tools, think)
	if err != nil {
		return "", nil, err
	}

	return p, media, nil
}

func imageTaggedMessages(m *Model, msgs []api.Message, start int, clearImages bool) ([]api.Message, []llm.MediaData, error) {
	renderMsgs := slices.Clone(msgs)
	var media []llm.MediaData

	for cnt, msg := range renderMsgs[start:] {
		if slices.Contains(m.Config.ModelFamilies, "mllama") && len(msg.Images) > 1 {
			return nil, nil, errors.New("this model only supports one image while more than one image requested")
		}

		var prefix string
		prompt := msg.Content

		for _, i := range msg.Images {
			mediaData := llm.NewMediaData(len(media), i)
			media = append(media, mediaData)

			if m.Config.Renderer != "" {
				continue
			}

			// The prompt marker is still image-named for compatibility with
			// existing templates and llama-server media marker replacement.
			imgTag := fmt.Sprintf("[img-%d]", mediaData.ID)
			if !strings.Contains(prompt, "[img]") {
				prefix += imgTag
			} else {
				prompt = strings.Replace(prompt, "[img]", imgTag, 1)
			}
		}

		if m.Config.Renderer == "" {
			renderMsgs[start+cnt].Content = prefix + prompt
		}
		if clearImages {
			renderMsgs[start+cnt].Images = nil
		}
	}

	return renderMsgs, media, nil
}

func renderPrompt(m *Model, msgs []api.Message, tools []api.Tool, think *api.ThinkValue) (string, error) {
	if m.Config.Renderer != "" {
		rendererName := resolveRendererName(m)
		rendered, err := renderers.RenderWithRenderer(rendererName, msgs, tools, think)
		if err != nil {
			return "", err
		}
		return rendered, nil
	}

	var b bytes.Buffer
	thinkVal := false
	thinkLevel := ""
	if think != nil {
		thinkVal = think.Bool()
		thinkLevel = think.String()
	}
	if err := m.Template.Execute(&b, template.Values{Messages: msgs, Tools: tools, Think: thinkVal, ThinkLevel: thinkLevel, IsThinkSet: think != nil}); err != nil {
		return "", err
	}
	return b.String(), nil
}
