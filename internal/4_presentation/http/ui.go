package httphandlers

import (
	"embed"
	"html/template"
)

//go:embed web
var uiTemplateFS embed.FS

var UITmpl = template.Must(template.ParseFS(uiTemplateFS, "web/index.html"))
