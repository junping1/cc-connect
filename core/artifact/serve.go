package artifact

import (
	"bytes"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
)

// handler serves a file identified by token in the URL path /a/{token}.
func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	const prefix = "/a/"
	if !strings.HasPrefix(path, prefix) {
		http.NotFound(w, r)
		return
	}
	token := strings.TrimPrefix(path, prefix)
	token = strings.SplitN(token, "/", 2)[0]
	if token == "" {
		http.NotFound(w, r)
		return
	}

	entry, ok := s.store.get(token)
	if !ok {
		http.Error(w, "not found or expired", http.StatusNotFound)
		return
	}

	w.Header().Set("X-Artifact-Expires", entry.Expires.UTC().Format(time.RFC3339))

	ext := strings.ToLower(filepath.Ext(entry.Path))
	name := filepath.Base(entry.Path)

	switch {
	case isMarkdown(ext):
		serveMarkdown(w, r, entry)
	case isImage(ext):
		serveImage(w, r, entry)
	case isText(ext) || ext == "":
		serveCode(w, r, entry, name)
	default:
		// Binary: force download
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
		http.ServeFile(w, r, entry.Path)
	}
}

func isMarkdown(ext string) bool {
	return ext == ".md" || ext == ".markdown"
}

func isImage(ext string) bool {
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico":
		return true
	}
	return false
}

func isText(ext string) bool {
	textExts := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true,
		".html": true, ".css": true, ".scss": true, ".json": true, ".yaml": true, ".yml": true,
		".toml": true, ".sh": true, ".bash": true, ".zsh": true, ".fish": true,
		".c": true, ".cpp": true, ".h": true, ".rs": true, ".java": true, ".kt": true,
		".rb": true, ".php": true, ".swift": true, ".sql": true, ".xml": true,
		".txt": true, ".log": true, ".csv": true, ".env": true, ".gitignore": true,
		".dockerfile": true, ".makefile": true, ".tf": true, ".proto": true,
		".lua": true, ".vim": true, ".diff": true, ".patch": true,
	}
	return textExts[ext]
}

func serveMarkdown(w http.ResponseWriter, _ *http.Request, entry Entry) {
	data, err := os.ReadFile(entry.Path)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusGone)
		return
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
		goldmark.WithRendererOptions(
			goldmarkhtml.WithHardWraps(),
			goldmarkhtml.WithUnsafe(),
		),
	)

	var body bytes.Buffer
	if err := md.Convert(data, &body); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	stat, _ := os.Stat(entry.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, markdownPage(filepath.Base(entry.Path), formatMeta(stat), body.String(), entry.Expires))
}

func serveCode(w http.ResponseWriter, _ *http.Request, entry Entry, name string) {
	data, err := os.ReadFile(entry.Path)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusGone)
		return
	}

	lexer := lexers.Match(name)
	if lexer == nil {
		lexer = lexers.Analyse(string(data))
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}

	style := styles.Get("github-dark")
	if style == nil {
		style = styles.Fallback
	}

	formatter := chromahtml.New(
		chromahtml.WithLineNumbers(true),
		chromahtml.WithLinkableLineNumbers(true, "L"),
		chromahtml.TabWidth(4),
	)

	iterator, err := lexer.Tokenise(nil, string(data))
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		stat, _ := os.Stat(entry.Path)
		fmt.Fprint(w, plainPage(name, formatMeta(stat), html.EscapeString(string(data)), entry.Expires))
		return
	}

	var highlighted bytes.Buffer
	if err := formatter.Format(&highlighted, style, iterator); err != nil {
		http.Error(w, "render error", http.StatusInternalServerError)
		return
	}

	var chromaCSS bytes.Buffer
	_ = formatter.WriteCSS(&chromaCSS, style)

	stat, _ := os.Stat(entry.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, codePage(name, formatMeta(stat), chromaCSS.String(), highlighted.String(), entry.Expires))
}

func serveImage(w http.ResponseWriter, r *http.Request, entry Entry) {
	f, err := os.Open(entry.Path)
	if err != nil {
		http.Error(w, "file unavailable", http.StatusGone)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	http.ServeContent(w, r, filepath.Base(entry.Path), fi.ModTime(), f)
}

func formatMeta(fi os.FileInfo) string {
	if fi == nil {
		return ""
	}
	size := fi.Size()
	var sizeStr string
	switch {
	case size < 1024:
		sizeStr = fmt.Sprintf("%d B", size)
	case size < 1024*1024:
		sizeStr = fmt.Sprintf("%.1f KB", float64(size)/1024)
	default:
		sizeStr = fmt.Sprintf("%.1f MB", float64(size)/1024/1024)
	}
	return fmt.Sprintf("%s · %s", sizeStr, fi.ModTime().Format("2006-01-02 15:04"))
}

func expiresLabel(t time.Time) string {
	remaining := time.Until(t).Round(time.Minute)
	if remaining <= 0 {
		return "expired"
	}
	h := int(remaining.Hours())
	m := int(remaining.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("expires in %dh %dm", h, m)
	}
	return fmt.Sprintf("expires in %dm", m)
}

// ── HTML templates ────────────────────────────────────────────────────────────

const baseCSS = `
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',system-ui,sans-serif;background:#0d1117;color:#e6edf3;min-height:100vh}
header{display:flex;align-items:center;gap:12px;padding:12px 24px;background:#161b22;border-bottom:1px solid #30363d;position:sticky;top:0;z-index:10}
header .name{font-weight:600;font-size:15px;color:#e6edf3;flex:1}
header .meta{font-size:12px;color:#8b949e}
.expires{font-size:11px;color:#8b949e;background:#21262d;border:1px solid #30363d;border-radius:12px;padding:2px 10px;white-space:nowrap}
main{padding:24px;max-width:960px;margin:0 auto}
`

func markdownPage(name, meta, body string, expires time.Time) string {
	return fmt.Sprintf(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/github-markdown-css/5.5.1/github-markdown-dark.min.css">
<style>
%s
.markdown-body{background:#0d1117;padding:32px;border:1px solid #30363d;border-radius:8px}
</style></head><body>
<header>
  <span class="name">%s</span>
  <span class="meta">%s</span>
  <span class="expires">%s</span>
</header>
<main><article class="markdown-body">%s</article></main>
</body></html>`, html.EscapeString(name), baseCSS, html.EscapeString(name), html.EscapeString(meta), expiresLabel(expires), body)
}

func codePage(name, meta, chromaCSS, highlighted string, expires time.Time) string {
	return fmt.Sprintf(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>
%s
%s
.highlight{padding:0;border:1px solid #30363d;border-radius:8px;overflow:auto}
.highlight pre{padding:16px;font-size:13px;line-height:1.5;font-family:'JetBrains Mono','Fira Code','Cascadia Code',monospace}
</style></head><body>
<header>
  <span class="name">%s</span>
  <span class="meta">%s</span>
  <span class="expires">%s</span>
</header>
<main>%s</main>
</body></html>`, html.EscapeString(name), baseCSS, chromaCSS, html.EscapeString(name), html.EscapeString(meta), expiresLabel(expires), highlighted)
}

func plainPage(name, meta, content string, expires time.Time) string {
	return fmt.Sprintf(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>%s
pre{background:#161b22;border:1px solid #30363d;border-radius:8px;padding:16px;overflow:auto;font-size:13px;line-height:1.5;font-family:monospace;color:#e6edf3}
</style></head><body>
<header>
  <span class="name">%s</span>
  <span class="meta">%s</span>
  <span class="expires">%s</span>
</header>
<main><pre>%s</pre></main>
</body></html>`, html.EscapeString(name), baseCSS, html.EscapeString(name), html.EscapeString(meta), expiresLabel(expires), content)
}
