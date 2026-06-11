package main

import (
	"embed"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

var tmpl = template.Must(template.New("").Funcs(template.FuncMap{
	"humanSize": humanSize,
	"add":       func(a, b int) int { return a + b },
	"sub":       func(a, b int) int { return a - b },
	"mul":       func(a, b int) int { return a * b },
	"even":      func(i int) bool { return i%2 == 0 },
	"nl2br": func(s string) template.HTML {
		esc := template.HTMLEscapeString(s)
		esc = strings.ReplaceAll(esc, "\r\n", "\n")
		esc = strings.ReplaceAll(esc, "\n", "<br>\n")
		return template.HTML(esc)
	},
	"indent": func(n int) template.HTML {
		s := ""
		for i := 0; i < n; i++ {
			s += "&nbsp;&nbsp;"
		}
		return template.HTML(s)
	},
}).ParseFS(templateFS, "templates/*.html"))

func render(w http.ResponseWriter, name string, data interface{}) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
	}
}

func renderError(w http.ResponseWriter, back, msg string) {
	render(w, "error.html", map[string]string{
		"Org":  config.OrgName,
		"Back": back,
		"Msg":  msg,
	})
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
