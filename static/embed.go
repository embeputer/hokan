package static

import "embed"

//go:embed style.css app.js
var FS embed.FS
