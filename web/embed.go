// Package web embeds the chat frontend (HTMX + Alpine.js + Tailwind,
// no build step) into cmd/serve's binary, so serving it needs no extra
// Docker COPY step or runtime file path — it's compiled in the same way
// docs/ is for Swagger.
package web

import "embed"

//go:embed templates static
var FS embed.FS
