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
	highlighting "github.com/yuin/goldmark-highlighting/v2"
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

	// Build Chroma CSS: dark as default, light scoped to [data-theme="light"]
	darkStyle := styles.Get("github-dark")
	if darkStyle == nil {
		darkStyle = styles.Fallback
	}
	lightStyle := styles.Get("github")
	if lightStyle == nil {
		lightStyle = styles.Fallback
	}
	cssFormatter := chromahtml.New(chromahtml.WithClasses(true))
	var darkBuf, lightBuf bytes.Buffer
	_ = cssFormatter.WriteCSS(&darkBuf, darkStyle)
	_ = cssFormatter.WriteCSS(&lightBuf, lightStyle)
	lightScoped := strings.ReplaceAll(lightBuf.String(), ".chroma", "[data-theme=\"light\"] .chroma")
	chromaCSS := darkBuf.String() + "\n" + lightScoped

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github-dark"),
				highlighting.WithGuessLanguage(true),
			),
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

	// Wrap tables for responsive horizontal scroll
	bodyHTML := strings.ReplaceAll(body.String(), "<table>", `<div class="table-wrapper"><table>`)
	bodyHTML = strings.ReplaceAll(bodyHTML, "</table>", "</table></div>")

	stat, _ := os.Stat(entry.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, markdownPage(filepath.Base(entry.Path), formatMeta(stat), chromaCSS, bodyHTML, entry.Expires))
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

func markdownPage(name, meta, chromaCSS, body string, expires time.Time) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Atkinson+Hyperlegible+Mono:ital,wght@0,200..800;1,200..800&family=Atkinson+Hyperlegible+Next:ital,wght@0,200..800;1,200..800&display=swap" rel="stylesheet">
<script>
(function() {
  var t = localStorage.getItem('vf-theme');
  if (!t) t = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  document.documentElement.setAttribute('data-theme', t);
  var fs = parseInt(localStorage.getItem('vf-fs'));
  if (fs >= 14 && fs <= 26) document.documentElement.style.setProperty('--fs', fs + 'px');
})();
</script>
<style>
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }
html { overflow-x: hidden; scroll-behavior: smooth; }
:root {
  --font-body: 'Atkinson Hyperlegible Next', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-ui:   'Atkinson Hyperlegible Next', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-mono: 'Atkinson Hyperlegible Mono', 'SF Mono', 'Menlo', 'Consolas', monospace;
  --fs: 18px;
}
:root, [data-theme="light"] {
  --bg: #F9F7F4; --bg-raised: #F2EFE9; --bg-code: #EDE9E2;
  --border: #DDD9D1; --border-soft: #E8E4DC;
  --text-head: #1C1916; --text-body: #36322C; --text-muted: #7A746C;
  --link: #1A5296; --link-hover: #0E3870; --code-fg: #7C3319;
  --quote-bg: #F2EDE3; --quote-border: #C4B49A;
  --progress: #1A5296;
  --ctrl-bg: rgba(0,0,0,0.05); --ctrl-hover: rgba(0,0,0,0.10);
  --ctrl-active: rgba(0,0,0,0.15); --ctrl-shadow: rgba(0,0,0,0.10);
}
[data-theme="dark"] {
  --bg: #1C1814; --bg-raised: #252018; --bg-code: #201C18;
  --border: #322C26; --border-soft: #2A2520;
  --text-head: #E0D6C8; --text-body: #C4BAA8; --text-muted: #78706A;
  --link: #82B8F5; --link-hover: #A8CEFF; --code-fg: #F0A070;
  --quote-bg: #231E18; --quote-border: #5A4C3E;
  --progress: #82B8F5;
  --ctrl-bg: rgba(255,255,255,0.06); --ctrl-hover: rgba(255,255,255,0.12);
  --ctrl-active: rgba(255,255,255,0.18); --ctrl-shadow: rgba(0,0,0,0.40);
}
[data-theme="night"] {
  --bg: #0E0B08; --bg-raised: #161008; --bg-code: #130E08;
  --border: #1E1710; --border-soft: #1A1410;
  --text-head: #C8A870; --text-body: #A88C60; --text-muted: #5C4C38;
  --link: #C49460; --link-hover: #D8AC78; --code-fg: #C49460;
  --quote-bg: #120E08; --quote-border: #4A3820;
  --progress: #C49460;
  --ctrl-bg: rgba(200,160,100,0.08); --ctrl-hover: rgba(200,160,100,0.15);
  --ctrl-active: rgba(200,160,100,0.22); --ctrl-shadow: rgba(0,0,0,0.60);
}
#vf-progress {
  position: fixed; top: 0; left: 0; height: 2px; width: 0%%;
  background: var(--progress); z-index: 1000;
  transition: width 80ms linear; opacity: 0.65; pointer-events: none;
}
.vf-ctrl-group {
  position: fixed; right: 16px; display: flex; align-items: center; gap: 2px;
  background: var(--bg); border: 1px solid var(--border); border-radius: 20px;
  padding: 4px 5px; box-shadow: 0 2px 10px var(--ctrl-shadow);
  z-index: 999; opacity: 0.25; transition: opacity 0.25s;
}
.vf-ctrl-group:hover { opacity: 1; }
#vf-themes { top: 14px; }
#vf-fonts  { top: 54px; }
.vf-btn {
  border: none; background: none; cursor: pointer;
  padding: 4px 9px; border-radius: 14px;
  font-family: var(--font-ui); font-size: 12px; line-height: 1;
  color: var(--text-muted); transition: background 0.15s, color 0.15s;
  -webkit-user-select: none; user-select: none;
}
.vf-btn:hover  { background: var(--ctrl-hover); color: var(--text-body); }
.vf-btn.active { background: var(--ctrl-active); color: var(--text-head); font-weight: 500; }
body {
  font-family: var(--font-body); font-size: var(--fs);
  line-height: 1.65; letter-spacing: 0.01em;
  background: var(--bg); color: var(--text-body); min-height: 100vh;
  -webkit-font-smoothing: antialiased; -moz-osx-font-smoothing: grayscale;
  text-rendering: optimizeLegibility;
}
.md { max-width: 65ch; margin: 0 auto; padding: 52px 24px 80px; }
h1, h2, h3, h4, h5, h6 {
  font-family: var(--font-ui); color: var(--text-head);
  line-height: 1.22; font-weight: 600; letter-spacing: -0.02em;
}
h1 { font-size: 1.75em; margin: 0 0 0.8em; padding-bottom: 0.4em; border-bottom: 1px solid var(--border-soft); }
h2 { font-size: 1.30em; margin: 2.2em 0 0.6em; padding-bottom: 0.35em; border-bottom: 1px solid var(--border-soft); }
h3 { font-size: 1.08em; margin: 1.8em 0 0.5em; font-weight: 600; }
h4 { font-size: 1.00em; margin: 1.5em 0 0.4em; font-weight: 600; letter-spacing: 0; }
h5, h6 { font-size: 0.85em; margin: 1.3em 0 0.35em; font-weight: 600; color: var(--text-muted); letter-spacing: 0.04em; text-transform: uppercase; }
p { margin-bottom: 1.2em; }
strong { color: var(--text-head); font-weight: 600; }
em { font-style: italic; }
a { color: var(--link); text-decoration: underline; text-decoration-color: transparent; text-underline-offset: 3px; transition: color 0.15s, text-decoration-color 0.15s; }
a:hover { color: var(--link-hover); text-decoration-color: var(--link-hover); }
ul, ol { padding-left: 1.7em; margin-bottom: 1.2em; }
li { margin-bottom: 0.35em; }
li > ul, li > ol { margin-top: 0.25em; margin-bottom: 0.25em; }
blockquote {
  border-left: 3px solid var(--quote-border); background: var(--quote-bg);
  margin: 1.6em 0; padding: 0.8em 1.2em;
  border-radius: 0 5px 5px 0; color: var(--text-muted); font-style: italic;
}
blockquote p:last-child { margin-bottom: 0; }
hr { border: none; border-top: 1px solid var(--border); margin: 2.4em 0; }
code {
  font-family: var(--font-mono); font-size: 0.80em;
  background: var(--bg-code); color: var(--code-fg);
  padding: 0.15em 0.42em; border-radius: 3px;
  font-variant-ligatures: none; letter-spacing: 0;
}
pre { margin: 1.6em 0; border-radius: 6px; overflow: hidden; border: 1px solid var(--border); }
pre code { background: none; color: inherit; padding: 0; }
pre > code {
  display: block; padding: 1.1em 1.3em;
  font-family: var(--font-mono); font-size: 0.82em;
  line-height: 1.6; white-space: pre; overflow-x: auto;
  letter-spacing: 0; font-variant-ligatures: none;
  background: var(--bg-code); color: var(--text-body);
}
.table-wrapper { width: 100%%; overflow-x: auto; margin: 1.6em 0; border: 1px solid var(--border); border-radius: 6px; }
table { border-collapse: collapse; width: 100%%; font-family: var(--font-ui); font-size: 0.875em; letter-spacing: 0; }
th { background: var(--bg-raised); color: var(--text-head); padding: 0.65em 1em; border-bottom: 2px solid var(--border); text-align: left; font-weight: 600; white-space: nowrap; }
td { padding: 0.6em 1em; border-bottom: 1px solid var(--border-soft); color: var(--text-body); }
tr:last-child td { border-bottom: none; }
tr:nth-child(even) td { background: var(--bg-raised); }
tbody tr:hover td { background: var(--bg-code); }
img { max-width: 100%%; height: auto; border-radius: 5px; }
.vf-footer {
  max-width: 65ch; margin: 0 auto; padding: 14px 24px 36px;
  border-top: 1px solid var(--border); display: flex; align-items: center; gap: 10px;
  font-family: var(--font-ui);
}
.vf-footer-path { font-size: 11px; font-family: var(--font-mono); color: var(--text-muted); letter-spacing: 0; }
.vf-footer-meta { font-size: 11px; color: var(--text-muted); opacity: 0.55; }
.vf-footer-expires { font-size: 11px; color: var(--text-muted); opacity: 0.55; margin-left: auto; }
@media (max-width: 600px) {
  :root { --fs: 17px; }
  .md { padding: 36px 18px 60px; }
  .vf-ctrl-group { opacity: 0.5; }
  #vf-themes { top: auto; bottom: 56px; right: 12px; }
  #vf-fonts  { top: auto; bottom: 14px; right: 12px; }
}
@media print {
  #vf-progress, .vf-ctrl-group { display: none !important; }
  body { background: #fff; color: #111; font-size: 11pt; }
  a { color: #111; text-decoration: underline; }
  pre, code { background: #f4f4f4 !important; color: #333 !important; border: 1px solid #ddd; }
  h1, h2, h3, h4, h5, h6 { color: #000; }
  .md { max-width: 100%%; padding: 0; }
  .vf-footer { padding: 12pt 0 0; border-top: 1pt solid #ccc; }
}
%s
</style>
</head>
<body>
<div id="vf-progress"></div>
<nav id="vf-themes" class="vf-ctrl-group" aria-label="Theme">
  <button class="vf-btn" data-t="light" title="Light mode">&#9728;</button>
  <button class="vf-btn" data-t="dark" title="Dark mode">&#9680;</button>
  <button class="vf-btn" data-t="night" title="Night mode (warm amber)">&#9790;</button>
</nav>
<div id="vf-fonts" class="vf-ctrl-group" aria-label="Font size">
  <button class="vf-btn" id="vf-fd" title="Smaller text">A&#8722;</button>
  <button class="vf-btn" id="vf-fu" title="Larger text">A+</button>
</div>
<main class="md">
%s
</main>
<footer class="vf-footer">
  <span class="vf-footer-path">%s</span>
  <span class="vf-footer-meta">%s</span>
  <span class="vf-footer-expires">%s</span>
</footer>
<script>
(function() {
  var bar = document.getElementById('vf-progress');
  function tick() {
    var scrolled = window.scrollY || document.documentElement.scrollTop;
    var total = document.documentElement.scrollHeight - window.innerHeight;
    bar.style.width = (total > 0 ? Math.min(100, scrolled / total * 100) : 0) + '%%';
  }
  window.addEventListener('scroll', tick, {passive: true});
  tick();
  var btns = document.querySelectorAll('[data-t]');
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    localStorage.setItem('vf-theme', t);
    btns.forEach(function(b) { b.classList.toggle('active', b.dataset.t === t); });
  }
  btns.forEach(function(b) {
    b.addEventListener('click', function() { applyTheme(b.dataset.t); });
  });
  applyTheme(document.documentElement.getAttribute('data-theme') || 'light');
  var FS_MIN = 14, FS_MAX = 26;
  var fs = parseInt(localStorage.getItem('vf-fs')) || 18;
  function applyFs(n) {
    fs = Math.max(FS_MIN, Math.min(FS_MAX, n));
    document.documentElement.style.setProperty('--fs', fs + 'px');
    localStorage.setItem('vf-fs', fs);
  }
  document.getElementById('vf-fu').addEventListener('click', function() { applyFs(fs + 1); });
  document.getElementById('vf-fd').addEventListener('click', function() { applyFs(fs - 1); });
  applyFs(fs);
})();
</script>
</body>
</html>`,
		html.EscapeString(name), chromaCSS, body,
		html.EscapeString(name), html.EscapeString(meta), expiresLabel(expires))
}

func codePage(name, meta, chromaCSS, highlighted string, expires time.Time) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Google+Sans+Flex:wght@100..900&family=Google+Sans+Code:wght@100..700&display=swap" rel="stylesheet">
<style>
:root {
  --bg: #1e1e1e; --bg-header: #2d2d2d; --border: #404040;
  --text: #d4d4d4; --text-header: #e0e0e0; --text-muted: #888888;
  --font-sans: 'Google Sans Flex', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-mono: 'Google Sans Code', 'SF Mono', 'Menlo', 'Monaco', 'Consolas', monospace;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #ffffff; --bg-header: #f6f8fa; --border: #d0d7de;
    --text: #1f2328; --text-header: #1f2328; --text-muted: #656d76;
  }
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: var(--font-sans); background: var(--bg); color: var(--text); min-height: 100vh; }
.file-header {
  background: var(--bg-header); border-bottom: 1px solid var(--border);
  padding: 12px 16px; display: flex; align-items: center; gap: 12px;
  position: sticky; top: 0; z-index: 10;
}
.file-path { font-size: 14px; font-weight: 500; color: var(--text-header); word-break: break-all; font-family: var(--font-mono); flex: 1; }
.file-meta { font-size: 12px; color: var(--text-muted); font-family: var(--font-sans); white-space: nowrap; }
.file-expires { font-size: 11px; color: var(--text-muted); background: var(--bg); border: 1px solid var(--border); border-radius: 12px; padding: 2px 10px; white-space: nowrap; }
.file-content { overflow-x: auto; }
%s
.highlight { background: var(--bg); padding: 0; }
.highlight pre {
  padding: 12px 16px; margin: 0;
  font-family: var(--font-mono); font-size: 14px;
  line-height: 1.65; white-space: pre-wrap;
  word-wrap: break-word; overflow-wrap: break-word;
}
@media (max-width: 768px) {
  .file-header { padding: 10px 12px; }
  .highlight pre { font-size: 13px; line-height: 1.5; padding: 8px 12px; }
}
</style>
</head>
<body>
  <div class="file-header">
    <span class="file-path">%s</span>
    <span class="file-meta">%s</span>
    <span class="file-expires">%s</span>
  </div>
  <div class="file-content">%s</div>
</body>
</html>`,
		html.EscapeString(name), chromaCSS,
		html.EscapeString(name), html.EscapeString(meta), expiresLabel(expires),
		highlighted)
}

func plainPage(name, meta, content string, expires time.Time) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link href="https://fonts.googleapis.com/css2?family=Google+Sans+Flex:wght@100..900&family=Google+Sans+Code:wght@100..700&display=swap" rel="stylesheet">
<style>
:root {
  --bg: #1e1e1e; --bg-header: #2d2d2d; --border: #404040;
  --text: #d4d4d4; --text-header: #e0e0e0; --text-muted: #888888;
  --font-sans: 'Google Sans Flex', -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
  --font-mono: 'Google Sans Code', 'SF Mono', 'Menlo', 'Monaco', 'Consolas', monospace;
}
@media (prefers-color-scheme: light) {
  :root {
    --bg: #ffffff; --bg-header: #f6f8fa; --border: #d0d7de;
    --text: #1f2328; --text-header: #1f2328; --text-muted: #656d76;
  }
}
* { margin: 0; padding: 0; box-sizing: border-box; }
body { font-family: var(--font-sans); background: var(--bg); color: var(--text); min-height: 100vh; }
.file-header {
  background: var(--bg-header); border-bottom: 1px solid var(--border);
  padding: 12px 16px; display: flex; align-items: center; gap: 12px;
  position: sticky; top: 0; z-index: 10;
}
.file-path { font-size: 14px; font-weight: 500; color: var(--text-header); word-break: break-all; font-family: var(--font-mono); flex: 1; }
.file-meta { font-size: 12px; color: var(--text-muted); font-family: var(--font-sans); white-space: nowrap; }
.file-expires { font-size: 11px; color: var(--text-muted); background: var(--bg); border: 1px solid var(--border); border-radius: 12px; padding: 2px 10px; white-space: nowrap; }
pre {
  padding: 16px; margin: 0; overflow-x: auto;
  font-family: var(--font-mono); font-size: 14px;
  line-height: 1.65; white-space: pre-wrap;
  word-wrap: break-word; overflow-wrap: break-word;
  color: var(--text);
}
@media (max-width: 768px) {
  .file-header { padding: 10px 12px; }
  pre { font-size: 13px; line-height: 1.5; padding: 8px 12px; }
}
</style>
</head>
<body>
  <div class="file-header">
    <span class="file-path">%s</span>
    <span class="file-meta">%s</span>
    <span class="file-expires">%s</span>
  </div>
  <pre>%s</pre>
</body>
</html>`,
		html.EscapeString(name),
		html.EscapeString(name), html.EscapeString(meta), expiresLabel(expires),
		content)
}
