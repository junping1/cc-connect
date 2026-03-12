package core

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	activityUpdateInterval = 800 * time.Millisecond
	activityToolInputMax   = 60  // max runes for tool input preview
	activityMaxChars       = 3800 // Telegram edit limit is 4096; leave headroom
)

// toolActivityTracker maintains a single editable "activity" message showing
// thinking blocks and tool calls in **chronological order**.
//
// Example output while working:
//
//	*Let me check the files first…*
//
//	✓ Bash: ls /home/ubuntu
//	*Now I can see the structure, let me read the main file…*
//	→ Read: /home/ubuntu/main.go
//
// Consecutive addThinking calls update the current thinking block in-place.
// A new thinking block starts after each tool call.
type toolActivityTracker struct {
	mu sync.Mutex

	platform Platform
	replyCtx any
	ctx      context.Context //nolint:containedctx

	events    []activityEvent
	msgHandle any
	degraded  bool
	timer     *time.Timer
}

type activityEvent struct {
	kind  string // "thinking" or "tool"
	text  string // thinking: full text; tool: name
	input string // tool input preview
	done  bool   // tool: true when completed
}

func newToolActivityTracker(p Platform, replyCtx any, ctx context.Context) *toolActivityTracker { //nolint:revive
	return &toolActivityTracker{
		platform: p,
		replyCtx: replyCtx,
		ctx:      ctx,
	}
}

// addThinking updates the current thinking block, or starts a new one if the
// last event was a tool call (i.e. a new reasoning phase has begun).
func (t *toolActivityTracker) addThinking(text string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.degraded || text == "" {
		return
	}
	// Update existing thinking block if it's the last event
	if len(t.events) > 0 && t.events[len(t.events)-1].kind == "thinking" {
		t.events[len(t.events)-1].text = text
	} else {
		t.events = append(t.events, activityEvent{kind: "thinking", text: text})
	}
	if t.msgHandle != nil {
		t.scheduleUpdateLocked()
	}
}

// addTool marks the previous tool as done and appends a new tool call.
// First call creates the activity message immediately.
func (t *toolActivityTracker) addTool(name, input string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.degraded {
		return
	}
	// Mark previous tool done
	for i := len(t.events) - 1; i >= 0; i-- {
		if t.events[i].kind == "tool" && !t.events[i].done {
			t.events[i].done = true
			break
		}
	}
	preview := truncateIf(input, activityToolInputMax)
	preview = strings.ReplaceAll(preview, "\n", " ")
	t.events = append(t.events, activityEvent{kind: "tool", text: name, input: preview})

	if t.msgHandle == nil {
		t.updateLocked()
	} else {
		t.scheduleUpdateLocked()
	}
}

// finish marks the last tool as done and does a final update.
func (t *toolActivityTracker) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if t.degraded || t.msgHandle == nil {
		return
	}
	for i := len(t.events) - 1; i >= 0; i-- {
		if t.events[i].kind == "tool" && !t.events[i].done {
			t.events[i].done = true
			break
		}
	}
	t.updateLocked()
}

func (t *toolActivityTracker) scheduleUpdateLocked() {
	if t.timer != nil {
		return
	}
	t.timer = time.AfterFunc(activityUpdateInterval, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.timer = nil
		if !t.degraded {
			t.updateLocked()
		}
	})
}

func (t *toolActivityTracker) updateLocked() {
	text := t.renderLocked()
	if text == "" {
		return
	}
	if t.msgHandle == nil {
		if starter, ok := t.platform.(PreviewStarter); ok {
			handle, err := starter.SendPreviewStart(t.ctx, t.replyCtx, text)
			if err != nil {
				t.degraded = true
				return
			}
			t.msgHandle = handle
		} else {
			if err := t.platform.Send(t.ctx, t.replyCtx, text); err != nil {
				t.degraded = true
				return
			}
			t.msgHandle = t.replyCtx
			t.degraded = true
		}
		return
	}
	updater, ok := t.platform.(MessageUpdater)
	if !ok {
		t.degraded = true
		return
	}
	if err := updater.UpdateMessage(t.ctx, t.msgHandle, text); err != nil {
		t.degraded = true
	}
}

func (t *toolActivityTracker) renderLocked() string {
	if len(t.events) == 0 {
		return ""
	}

	var sb strings.Builder
	i := 0
	for i < len(t.events) {
		ev := t.events[i]
		if ev.kind == "thinking" {
			// Italic paragraphs
			paragraphs := strings.Split(strings.TrimSpace(ev.text), "\n\n")
			for _, p := range paragraphs {
				p = strings.TrimSpace(strings.ReplaceAll(p, "\n", " "))
				if p == "" {
					continue
				}
				sb.WriteString("*")
				sb.WriteString(p)
				sb.WriteString("*\n\n")
			}
			i++
		} else {
			// Collect consecutive tool events into one code block
			sb.WriteString("```\n")
			for i < len(t.events) && t.events[i].kind == "tool" {
				tool := t.events[i]
				if tool.done {
					sb.WriteString("✓ ")
				} else {
					sb.WriteString("→ ")
				}
				sb.WriteString(tool.text)
				if tool.input != "" {
					sb.WriteString(": ")
					sb.WriteString(tool.input)
				}
				sb.WriteString("\n")
				i++
			}
			sb.WriteString("```\n\n")
		}
	}

	result := strings.TrimRight(sb.String(), "\n")

	// If over Telegram's edit limit, trim oldest content from the top
	if len(result) > activityMaxChars {
		result = "…\n" + result[len(result)-activityMaxChars:]
		if idx := strings.Index(result, "\n"); idx >= 0 {
			result = "…\n" + result[idx+1:]
		}
	}

	return result
}
