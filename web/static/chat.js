// Alpine.js component driving the chat page. POST /chat streams
// Server-Sent Events, but native EventSource (and htmx's SSE extension,
// which is built on it) only supports GET requests with no body — since
// /chat takes a JSON body, the stream is read by hand here via
// fetch + ReadableStream instead, parsing "event: ...\ndata: ...\n\n"
// frames ourselves. htmx is still vendored/loaded for any future
// declarative markup on this page; it isn't doing the streaming.
function chatApp() {
  return {
    messages: [], // {role, content, status: "", sources: []}
    input: "",
    streaming: false,
    errorMessage: "",
    usedTokens: 0,
    contextTokens: null,
    // Whether the transcript is "stuck" to the bottom (autoscrolls on new
    // content). Flips off when the user scrolls up, back on when they
    // scroll back down or send a message.
    stick: true,
    // Source drawer: shows one lore .md rendered server-side, cited passages
    // highlighted. file/chunks come from the "sources" SSE frame.
    drawer: { open: false, file: "", html: "", loading: false },

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
      this.pushUser(text);
      this.runTurn();
    },

    // A "/compact" message (typed here or sent by the context indicator)
    // gets kind:"compaction" so the turn renders as a centered divider
    // notice instead of a chat bubble + empty reply.
    pushUser(text) {
      const kind = text === "/compact" ? "compaction" : undefined;
      this.messages.push({ role: "user", content: text, kind, status: "", sources: [] });
      this.stick = true; // a fresh send always follows the conversation down
      this.scrollDown();
    },

    // Keep the transcript pinned to the bottom as replies stream in, but
    // only while the user hasn't scrolled up to read earlier messages
    // (this.stick, refreshed on every scroll event — see x-on:scroll in the
    // template). $nextTick so the DOM has the new content before we measure.
    scrollDown() {
      if (!this.stick) return;
      this.$nextTick(() => {
        const el = document.getElementById("messages");
        if (el) el.scrollTop = el.scrollHeight;
      });
    },

    onScroll() {
      const el = document.getElementById("messages");
      if (!el) return;
      this.stick = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    },

    // Sent by the context-usage indicator's click handler — same
    // server-side path as a user typing /compact themselves.
    compact() {
      if (this.streaming || this.contextTokens === null) return;
      this.pushUser("/compact");
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
      // A compaction turn's reply is the summary notice, not a chat bubble.
      const compaction = this.messages[this.messages.length - 1]?.kind === "compaction";
      const idx =
        this.messages.push({
          role: "assistant",
          content: "",
          kind: compaction ? "compaction" : undefined,
          status: "",
          sources: [],
        }) - 1;

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
        case "tool_call": {
          const args = data.args || {};
          switch (data.tool) {
            case "list_resources":
              this.setStatus(idx, "Listing documents…");
              break;
            case "get_resource":
              this.setStatus(idx, `Reading ${args.name || "document"}…`);
              break;
            case "lore_drop":
              this.setStatus(
                idx,
                `Saving new lore: ${args.filename || "document"}${args.reason ? ` (${args.reason})` : ""}…`
              );
              break;
            default:
              this.setStatus(idx, `Searching documents: "${args.query || ""}"…`);
          }
          break;
        }
        case "tool_result": {
          if (data.error) {
            this.setStatus(idx, `Tool failed: ${data.error}`);
            break;
          }
          if (data.message) {
            // list_resources hands back an array of {name, chunks} — show
            // the filenames inline rather than just the count.
            let extra = "";
            if (Array.isArray(data.payload) && data.payload.every((r) => r && r.name)) {
              extra = ` — ${data.payload.map((r) => r.name).join(", ")}`;
            }
            this.setStatus(idx, `${data.message}${extra}`);
            break;
          }
          this.setStatus(idx, `Found ${(data.results || []).length} matching chunk(s).`);
          break;
        }
        case "compacting":
          if (assistantMsg.kind === "compaction") assistantMsg.content = "Compacting the conversation…";
          else this.setStatus(idx, "Summarizing earlier conversation…");
          break;
        case "compacted": {
          const s = data.summary || "";
          const noop = !s || s === "nothing to compact";
          if (assistantMsg.kind === "compaction") {
            assistantMsg.content = noop ? "Nothing to compact." : `Context compacted — ${s}`;
          } else {
            this.setStatus(idx, noop ? "Nothing to compact." : `Compacted: ${s}`);
          }
          break;
        }
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
        case "sources":
          // Mutate through this.messages[idx], not a held reference — same
          // reactivity requirement runTurn's comment explains.
          this.messages[idx].sources = Array.isArray(data.sources) ? data.sources : [];
          break;
        case "error":
          this.errorMessage = data.error || "unknown error";
          break;
        case "done":
          // A compaction turn's notice already carries its final text.
          if (assistantMsg.kind === "compaction") break;
          // Show "Done!" briefly, then clear the status line on its own.
          this.setStatus(idx, "Done!");
          setTimeout(() => this.setStatus(idx, ""), 2000);
          break;
        default:
          break;
      }
      this.scrollDown();
    },

    // Opens the drawer for one source: fetches the .md rendered to HTML
    // (with the cited blocks already tagged class="cited" server-side),
    // then scrolls the first highlighted block into view.
    async openSource(s) {
      this.drawer = { open: true, file: s.file, html: "", loading: true };
      const params = (s.chunk_indices || []).map((i) => "chunks=" + i).join("&");
      try {
        const resp = await fetch("/lore/" + encodeURIComponent(s.file) + (params ? "?" + params : ""));
        if (!resp.ok) throw new Error(`request failed: ${resp.status}`);
        const data = await resp.json();
        this.drawer.html = data.html || "";
      } catch (err) {
        this.drawer.html =
          '<p style="color:#b91c1c">Could not load this document: ' +
          (err && err.message ? err.message : String(err)) +
          "</p>";
      } finally {
        this.drawer.loading = false;
      }
      this.$nextTick(() => {
        const first = this.$root.querySelector(".drawer-body .cited");
        if (first) first.scrollIntoView({ block: "center" });
      });
    },

    closeDrawer() {
      this.drawer.open = false;
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
