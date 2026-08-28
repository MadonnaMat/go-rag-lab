package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
		// GitHub Actions runners default /dev/shm to 64MB, too small for
		// Chrome's shared memory use — without this, Chrome can crash or
		// hang on startup, surfacing as chromedp's "websocket url timeout
		// reached" rather than any more obvious error.
		chromedp.Flag("disable-dev-shm-usage", true),
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

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 60*time.Second)
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

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 60*time.Second)
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

// TestWeb_CompactionNotice proves a /compact turn renders as a centered
// divider notice carrying the summary — not an empty chat bubble.
func TestWeb_CompactionNotice(t *testing.T) {
	requireChrome(t)

	chatter := &fakeChatter{events: []chat.Event{
		{Type: chat.EventCompacting},
		{Type: chat.EventCompacted, Summary: "Earlier turns covered ulmarin biology and diet."},
		{Type: chat.EventContextUsage, UsedTokens: 200, ContextTokens: 1000},
		{Type: chat.EventDone},
	}}
	srv := httptest.NewServer(NewRouter(&Handler{Chatter: chatter}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 60*time.Second)
	defer cancel()

	var noticeText string
	var bubbleCount int
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "/compact", chromedp.ByQuery),
		chromedp.Click("#chat-send", chromedp.ByQuery),
		chromedp.WaitEnabled("#chat-input", chromedp.ByQuery),
		chromedp.WaitVisible(".compaction-notice", chromedp.ByQuery),
		chromedp.Text(".compaction-notice", &noticeText, chromedp.ByQuery),
		chromedp.Evaluate(`document.querySelectorAll('#messages .message').length`, &bubbleCount),
	)
	require.NoError(t, err)

	assert.Contains(t, noticeText, "Context compacted")
	assert.Contains(t, noticeText, "ulmarin biology and diet")
	assert.Equal(t, 0, bubbleCount, "a /compact turn renders no chat bubbles (x-if, not just hidden)")
}

// TestWeb_SourceDrawer proves the "sources" frame renders clickable chips
// under the answer, and clicking one opens the drawer with the .md rendered
// and its cited passage highlighted.
func TestWeb_SourceDrawer(t *testing.T) {
	requireChrome(t)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "03-diet.md"),
		[]byte("# Diet\n\nThe ulmarin graze on drifting glowfronds at dusk.\n\nUnrelated closing note.\n"), 0o600))

	chatter := &fakeChatter{events: []chat.Event{
		{Type: chat.EventToken, Token: "They eat glowfronds [03-diet.md]."},
		{Type: chat.EventSources, Sources: []chat.SourceRef{{File: "03-diet.md", ChunkIndices: []int{0}}}},
		{Type: chat.EventContextUsage, UsedTokens: 100, ContextTokens: 1000},
		{Type: chat.EventDone},
	}}
	srv := httptest.NewServer(NewRouter(&Handler{
		Chatter: chatter,
		LoreDir: dir,
		LoreChunks: &fakeChunkSource{byPath: map[string]map[int]string{
			"03-diet.md": {0: "The ulmarin graze on drifting glowfronds at dusk."},
		}},
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 60*time.Second)
	defer cancel()

	var chipText, drawerFile, markText string
	var drawerOpen bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "what do they eat?", chromedp.ByQuery),
		chromedp.Click("#chat-send", chromedp.ByQuery),
		chromedp.WaitEnabled("#chat-input", chromedp.ByQuery),
		chromedp.WaitVisible(".sources .source-chip", chromedp.ByQuery),
		chromedp.Text(".sources .source-chip", &chipText, chromedp.ByQuery),
		chromedp.Click(".sources .source-chip", chromedp.ByQuery),
		chromedp.WaitVisible(".drawer.drawer-open .markdown-body h1", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`document.querySelector('.drawer').classList.contains('drawer-open')`, &drawerOpen),
		chromedp.Text(".drawer-head span", &drawerFile, chromedp.ByQuery),
		chromedp.Text(".drawer-body p.cited", &markText, chromedp.ByQuery),
	)
	require.NoError(t, err)

	assert.Equal(t, "03-diet.md", chipText)
	assert.True(t, drawerOpen)
	assert.Equal(t, "03-diet.md", drawerFile)
	assert.Contains(t, markText, "glowfronds", "the cited chunk should be highlighted in the drawer")

	// Closing the drawer removes the open class.
	var stillOpen bool
	err = chromedp.Run(ctx,
		chromedp.EvaluateAsDevTools(`document.querySelector('.drawer-close').click()`, nil),
		chromedp.Sleep(500*time.Millisecond),
		chromedp.EvaluateAsDevTools(`document.querySelector('.drawer').classList.contains('drawer-open')`, &stillOpen),
	)
	require.NoError(t, err)
	assert.False(t, stillOpen, "closeDrawer should remove .drawer-open")
}

// TestWeb_AutoScroll proves the transcript follows a streaming reply down to
// the bottom, but stops sticking once the user scrolls up.
func TestWeb_AutoScroll(t *testing.T) {
	requireChrome(t)

	var events []chat.Event
	for i := 0; i < 60; i++ { // enough tokens to overflow the message pane
		events = append(events, chat.Event{Type: chat.EventToken, Token: "line of streamed answer text " + fmt.Sprint(i) + "\n"})
	}
	events = append(events, chat.Event{Type: chat.EventDone})
	srv := httptest.NewServer(NewRouter(&Handler{Chatter: &fakeChatter{events: events}}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(newChromedpContext(t), 60*time.Second)
	defer cancel()

	var pinnedAfterStream, stuckAfterScrollUp bool
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#chat-input", chromedp.ByQuery),
		chromedp.SendKeys("#chat-input", "go", chromedp.ByQuery),
		chromedp.Click("#chat-send", chromedp.ByQuery),
		chromedp.WaitEnabled("#chat-input", chromedp.ByQuery),
		chromedp.EvaluateAsDevTools(`(() => { const el = document.getElementById('messages'); return el.scrollHeight - el.scrollTop - el.clientHeight < 40; })()`, &pinnedAfterStream),
		// Scroll up: the transcript should stop sticking to the bottom.
		chromedp.EvaluateAsDevTools(`(() => { const el = document.getElementById('messages'); el.scrollTop = 0; el.dispatchEvent(new Event('scroll')); return Alpine.$data(document.querySelector('[x-data]')).stick; })()`, &stuckAfterScrollUp),
	)
	require.NoError(t, err)
	assert.True(t, pinnedAfterStream, "transcript should be scrolled to the bottom after a streamed reply")
	assert.False(t, stuckAfterScrollUp, "scrolling up should disable autoscroll")
}
