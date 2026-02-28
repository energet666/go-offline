package main

import (
	"embed"
	"html/template"
)

//go:embed web
var uiTemplateFS embed.FS

var uiTmpl = template.Must(template.ParseFS(uiTemplateFS, "web/index.html"))
