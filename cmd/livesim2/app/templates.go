package app

import (
	htmpl "html/template"
	"io/fs"
	"path"
	ttmpl "text/template"
)

func compileTextTemplates(templateRoot fs.FS, dir string) (*ttmpl.Template, error) {
	return ttmpl.ParseFS(templateRoot, path.Join(dir, "*.gotxt"))
}

func compileHTMLTemplates(templateRoot fs.FS, dir string) (*htmpl.Template, error) {
	return htmpl.ParseFS(templateRoot, path.Join(dir, "*.gohtml"))
}
