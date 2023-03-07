// Copyright 2023, DASH-Industry Forum. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE.md file.

package app

import (
	htmpl "html/template"
	"io/fs"
	"path"
	ttmpl "text/template"
)

func compileTextTemplates(templateRoot fs.FS, dir string) (*ttmpl.Template, error) {
	return ttmpl.ParseFS(templateRoot, path.Join(dir, "*.xml"))
}

func compileHTMLTemplates(templateRoot fs.FS, dir string) (*htmpl.Template, error) {
	return htmpl.ParseFS(templateRoot, path.Join(dir, "*.html"))
}
