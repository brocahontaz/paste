// Package paste embeds the static web assets (templates, CSS, JS) so the
// paste binary is fully self-contained with no runtime file dependencies.
package paste

import "embed"

//go:embed web/templates/*.html web/static/style.css web/static/app.js web/static/highlight.min.js
var Assets embed.FS
