package api

import (
	"html/template"
	"io/fs"
	"net/http"

	"github.com/MadonnaMat/go-rag-lab/web"
)

// chatPageTemplate is parsed once at package init — a broken template
// fails the build/startup immediately rather than surfacing as a 500 on
// the first request.
var chatPageTemplate = template.Must(template.ParseFS(web.FS, "templates/*.html"))

// staticFS serves web/static's vendored JS/CSS straight out of the
// embedded binary — no disk path or env var involved at runtime.
var staticFS = func() fs.FS {
	sub, err := fs.Sub(web.FS, "static")
	if err != nil {
		panic(err) // web.FS is compiled in; a missing "static" dir is a build-time bug
	}
	return sub
}()

// handleChatPage serves the chat frontend's single page. Not an API
// endpoint, so it carries no swagger annotations — same as
// handleHealthz.
func (h *Handler) handleChatPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = chatPageTemplate.ExecuteTemplate(w, "chat.html", nil)
}
