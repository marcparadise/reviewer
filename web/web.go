package web

import "embed"

//go:embed index.html style.css api.js dom.js router.js diff.js views.js
var Files embed.FS
