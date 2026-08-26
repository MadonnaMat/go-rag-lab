package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os/exec"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MadonnaMat/go-rag-lab/internal/chat"
)

// requireChrome skips the test if no Chrome/Chromium binary is on PATH —
// mirrors the DATABASE_URL-gated pattern used for DB-integration tests,
// so `go test ./...`/`make test-unit` stay zero-infra by default.
func requireChrome(t *testing.T) {
	t.Helper()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	t.Skip("no Chrome/Chromium found on PATH; skipping browser test")
}

func newChromedpContext(t *testing.T) context.Context {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", true),
		chromedp.Flag("no-sandbox", true), // needed when running as root, e.g. in CI containers
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	t.Cleanup(cancelAlloc)
	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	t.Cleanup(cancelCtx)
	return ctx
}

// TestWeb_ChatFlow drives the real chat page in headless Chrome against a
// real HTTP listener (real templates/static from web.FS) with a scripted
// fake Chatter — no real Ollama or Postgres. Proves the DOM actually
// updates as the SSE stream plays: streamed tokens land in the assistant
// bubble in order, the context-usage indicator reflects the reported
// usage, and there's a single status line that gets replaced (not
// accumulated) as the turn progresses through its phases, ending on
// "Done!" for a couple seconds before clearing itself.
func TestWeb_ChatFlow(t *testing.T) {
	requireChrome(t)

	chatter := &fakeChatter{events: []chat.Event{
		// Real Ollama sends dozens of these per turn, one per reasoning
		// token — there's only ever one status line, so these must all
		// collapse into it rather than piling up a line per delta.
		{Type: chat.EventThinking, Token: "thinking"},
		{Type: chat.EventThinking, Token: " about"},
		{Type: chat.EventThinking, Token: " it"},
		{Type: chat.EventToolCall, ToolName: "retrieve_documents", ToolArgs: map[string]any{"query": "x"}},
		{Type: chat.EventToolResult, ToolResult: nil},
		{Type: chat.EventToken, Token: "Hello"},
		{Type: chat.EventToken, Token: " world"},
		{Type: chat.EventContextUsage, UsedTokens: 500, ContextTokens: 1000},
		{Type: chat.EventDone},
	}}
	srv := httptest.NewServer(NewRouter(&Handler{Chatter: chatter}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 30*time.Second)
	defer cancel()

	const statusSel = `#messages .message[data-role="assistant"] ~ p.status`

	var assistantText, indicatorText, doneStatus string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "how does X work?", chromedp.ByQuery),
		chromedp.Click("#chat-send", chromedp.ByQuery),
		// The input is disabled while streaming and re-enabled once the
		// stream's "done" event fires — waiting for that is the
		// synchronization point for "the turn finished".
		chromedp.WaitEnabled("#chat-input", chromedp.ByQuery),
		chromedp.Text(`#messages .message[data-role="assistant"]`, &assistantText, chromedp.ByQuery),
		chromedp.Text("#context-indicator-text", &indicatorText, chromedp.ByQuery),
		// "done" fires right before streaming flips off, so the "Done!"
		// status should already be showing here.
		chromedp.Text(statusSel, &doneStatus, chromedp.ByQuery),
	)
	require.NoError(t, err)

	assert.Equal(t, "Hello world", assistantText)
	assert.Equal(t, "50%", indicatorText)
	assert.Equal(t, "Done!", doneStatus)
	require.Len(t, chatter.gotHist, 1)
	assert.Equal(t, "how does X work?", chatter.gotHist[0].Content)

	// The status line clears itself ~2s after showing "Done!".
	var stillVisible bool
	err = chromedp.Run(ctx,
		chromedp.Sleep(2200*time.Millisecond),
		chromedp.EvaluateAsDevTools(fmt.Sprintf(`(() => { const el = document.querySelector(%q); return !!el && el.offsetParent !== null; })()`, statusSel), &stillVisible),
	)
	require.NoError(t, err)
	assert.False(t, stillVisible, "status line should have cleared itself after ~2s")
}

// TestWeb_CompactIndicatorClick proves clicking the context-usage
// indicator sends the same /compact command a user could type — the two
// entry points share one server-side path.
func TestWeb_CompactIndicatorClick(t *testing.T) {
	requireChrome(t)

	chatter := &fakeChatter{events: []chat.Event{
		{Type: chat.EventToken, Token: "ok"},
		{Type: chat.EventContextUsage, UsedTokens: 900, ContextTokens: 1000},
		{Type: chat.EventDone},
	}}
	srv := httptest.NewServer(NewRouter(&Handler{Chatter: chatter}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 30*time.Second)
	defer cancel()

	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "hi", chromedp.ByQuery),
		chromedp.Click("#chat-send", chromedp.ByQuery),
		chromedp.WaitEnabled("#chat-input", chromedp.ByQuery),
		chromedp.Click("#context-indicator", chromedp.ByQuery),
		chromedp.WaitEnabled("#chat-input", chromedp.ByQuery),
	)
	require.NoError(t, err)

	// The client resends the full accumulated history each request: the
	// second call's history should be [user:hi, assistant:ok, user:/compact].
	require.Len(t, chatter.gotHist, 3)
	last := chatter.gotHist[len(chatter.gotHist)-1]
	assert.Equal(t, "/compact", last.Content)
}
