// Alpine.js component driving the chat page. POST /chat streams
// Server-Sent Events, but native EventSource (and htmx's SSE extension,
// which is built on it) only supports GET requests with no body — since
// /chat takes a JSON body, the stream is read by hand here via
// fetch + ReadableStream instead, parsing "event: ...\ndata: ...\n\n"
// frames ourselves. htmx is still vendored/loaded for any future
// declarative markup on this page; it isn't doing the streaming.
function chatApp() {
  return {
    messages: [], // {role, content, status: ""}
    input: "",
    streaming: false,
    errorMessage: "",
    usedTokens: 0,
    contextTokens: null,

    usagePercent() {
      if (!this.contextTokens) return 0;
      return Math.min(100, Math.round((this.usedTokens / this.contextTokens) * 100));
    },

    usageColor() {
      const p = this.usagePercent();
      if (p >= 80) return "bg-red-500";
      if (p >= 60) return "bg-amber-500";
      return "bg-emerald-500";
    },

    send() {
      const text = this.input.trim();
      if (!text || this.streaming) return;
      this.input = "";
      this.messages.push({ role: "user", content: text, status: "" });
      this.runTurn();
    },

    // Sent by the context-usage indicator's click handler — same
    // server-side path as a user typing /compact themselves.
    compact() {
      if (this.streaming || this.contextTokens === null) return;
      this.messages.push({ role: "user", content: "/compact", status: "" });
      this.runTurn();
    },

    async runTurn() {
      this.streaming = true;
      this.errorMessage = "";

      // Snapshot history *before* pushing the in-progress assistant
      // placeholder, so there's no need to filter it back out.
      const history = this.messages.map((m) => ({ role: m.role, content: m.content }));

      // Alpine wraps pushed objects in its own reactive proxy — the array
      // element is NOT the same object reference as the one just pushed,
      // so later mutations must go through this.messages[idx], never a
      // held reference to the plain object, or they won't trigger a
      // re-render.
      const idx = this.messages.push({ role: "assistant", content: "", status: "" }) - 1;

      try {
        const resp = await fetch("/chat", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ messages: history }),
        });
        if (!resp.ok || !resp.body) {
          const body = await resp.text().catch(() => "");
          throw new Error(body || `request failed: ${resp.status}`);
        }

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";

        for (;;) {
          const { value, done } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });

          let sep;
          while ((sep = buf.indexOf("\n\n")) !== -1) {
            const frame = buf.slice(0, sep);
            buf = buf.slice(sep + 2);
            this.handleFrame(frame, idx);
          }
        }
      } catch (err) {
        this.errorMessage = err && err.message ? err.message : String(err);
      } finally {
        this.streaming = false;
      }
    },

    handleFrame(frame, idx) {
      const assistantMsg = this.messages[idx];
      let event = "message";
      const dataLines = [];
      for (const line of frame.split("\n")) {
        if (line.startsWith("event:")) event = line.slice(6).trim();
        else if (line.startsWith("data:")) dataLines.push(line.slice(5).trim());
      }

      let data = {};
      try {
        data = JSON.parse(dataLines.join("\n") || "{}");
      } catch {
        // malformed frame; ignore rather than breaking the whole stream
      }

      switch (event) {
        case "thinking":
          // Fires once per reasoning-token delta (often dozens of times
          // per turn) — there's only ever one status line, so this just
          // keeps replacing it rather than piling up.
          this.setStatus(idx, "Thinking…");
          break;
        case "tool_call":
          this.setStatus(idx, `Searching documents: "${(data.args && data.args.query) || ""}"…`);
          break;
        case "tool_result":
          this.setStatus(
            idx,
            data.error
              ? `Search failed: ${data.error}`
              : `Found ${(data.results || []).length} matching chunk(s).`
          );
          break;
        case "compacting":
          this.setStatus(idx, "Summarizing earlier conversation…");
          break;
        case "compacted":
          this.setStatus(idx, data.summary ? `Compacted: ${data.summary}` : "Compacted.");
          break;
        case "verifying":
          this.setStatus(idx, "Double-checking the answer…");
          break;
        case "revised":
          assistantMsg.content = data.content || "";
          break;
        case "token":
          assistantMsg.content += data.content || "";
          break;
        case "context_usage":
          this.usedTokens = data.used_tokens || 0;
          this.contextTokens = data.context_tokens || null;
          break;
        case "error":
          this.errorMessage = data.error || "unknown error";
          break;
        case "done":
          // Show "Done!" briefly, then clear the status line on its own.
          this.setStatus(idx, "Done!");
          setTimeout(() => this.setStatus(idx, ""), 2000);
          break;
        default:
          break;
      }
    },

    // Replaces the single status line for the assistant message at idx —
    // always looked up fresh through this.messages (not a held object
    // reference), the same reactivity requirement runTurn's comment
    // explains, since this also runs later from a setTimeout callback.
    setStatus(idx, text) {
      this.messages[idx].status = text;
    },
  };
}
