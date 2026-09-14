package internal

import (
	"fmt"
	"strings"
	"sync"

	"github.com/alecthomas/chroma/v2"
	chromaHTML "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// classPrefix namespaces the CSS classes chroma emits so they cannot collide
// with the app's own styles. The matching rules live in the /highlight.css
// stylesheet produced by HighlightCSS.
const classPrefix = "ch-"

// githubStyle and darkStyle are the light/dark chroma themes. Highlight HTML is
// theme-independent (class-based); these only feed the generated stylesheet, so
// the same highlighted markup renders correctly in either mode.
func githubStyle() *chroma.Style { return styleOrFallback("github") }
func darkStyle() *chroma.Style   { return styleOrFallback("github-dark") }

func styleOrFallback(name string) *chroma.Style {
	if s := styles.Get(name); s != nil {
		return s
	}
	return styles.Fallback
}

// htmlFormatter returns the chroma HTML formatter used for all highlighting.
// Class-based output (rather than inline styles) lets a single rendering serve
// both light and dark themes; colors come from the HighlightCSS stylesheet.
func htmlFormatter() *chromaHTML.Formatter {
	return chromaHTML.New(
		chromaHTML.WithClasses(true),
		chromaHTML.ClassPrefix(classPrefix),
		chromaHTML.WithCSSComments(false),
	)
}

var (
	highlightCSSOnce sync.Once
	highlightCSSVal  string
)

// HighlightCSS returns the stylesheet that colors highlighted diff markup. The
// light theme applies by default; dark-theme rules are scoped under "html.dark"
// so they take over when the app toggles dark mode. Computed once and cached.
func HighlightCSS() string {
	highlightCSSOnce.Do(func() {
		f := htmlFormatter()
		var b strings.Builder
		// Scope each theme so the other's rules never apply. This matters because
		// a token the dark theme leaves unstyled must inherit the dark base color
		// (via .cl below), not fall through to the light theme's explicit color.
		writeThemeCSS(&b, f, githubStyle(), "html:not(.dark) ")
		writeThemeCSS(&b, f, darkStyle(), "html.dark ")
		highlightCSSVal = b.String()
	})
	return highlightCSSVal
}

// writeThemeCSS emits chroma's class rules for one style, scoping each selector
// with scope (e.g. "html.dark "). It drops the wrapper background rules — we
// want token coloring over the diff's own row backgrounds, not chroma's — but
// preserves the theme's base text color, retargeted to highlighted code lines
// so tokens the theme leaves unstyled (common in dark themes) still get a
// readable color instead of inheriting the other theme's explicit one.
func writeThemeCSS(b *strings.Builder, f *chromaHTML.Formatter, style *chroma.Style, scope string) {
	var raw strings.Builder
	f.WriteCSS(&raw, style)
	bgSel := "." + classPrefix + "bg"
	chromaSel := "." + classPrefix + "chroma"
	wsSel := chromaSel + " ." + classPrefix + "w"
	for _, line := range strings.Split(raw.String(), "\n") {
		line = strings.TrimSpace(line)
		selRaw, body, ok := strings.Cut(line, "{")
		if !ok {
			continue
		}
		switch strings.TrimSpace(selRaw) {
		case bgSel, wsSel:
			// bg: wrapper background only. w: whitespace — coloring it is
			// pointless and the light theme even paints it white (invisible).
			continue
		case chromaSel:
			// PreWrapper rule: keep just the base text color, applied to the
			// highlighted code lines (.cl) so it doesn't restyle the whole diff.
			if c := declValue(body, "color"); c != "" {
				fmt.Fprintf(b, "%s%s .%scl { color: %s }\n", scope, chromaSel, classPrefix, c)
			}
			continue
		}
		b.WriteString(scope)
		b.WriteString(line)
		b.WriteByte('\n')
	}
}

// declValue returns the value of the named CSS property from a rule body like
// "color: #fff; background-color: #000; }", or "" if absent. Matching is exact
// on the property name so "color" does not match "background-color".
func declValue(body, prop string) string {
	body = strings.TrimRight(strings.TrimSpace(body), "}")
	for _, decl := range strings.Split(body, ";") {
		name, val, ok := strings.Cut(decl, ":")
		if ok && strings.TrimSpace(name) == prop {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// matchLexer picks a lexer for filePath, falling back to the plaintext lexer.
func matchLexer(filePath string) chroma.Lexer {
	if l := lexers.Match(filePath); l != nil {
		return l
	}
	return lexers.Fallback
}

// splitHTMLLines splits chroma's HTML output (between <code> tags) into
// per-line segments. Chroma wraps each line in a display:flex outer span and
// a CodeLine inner span. Tracking span depth to find when the outermost span
// closes is the only reliable split — the simpler triple-</span> marker only
// works when the last content token is styled (true for Go, not for CSS/JS/etc.).
func splitHTMLLines(s string) []string {
	var lines []string
	depth, start := 0, 0
	for i := 0; i < len(s); {
		if strings.HasPrefix(s[i:], "</span>") {
			depth--
			i += len("</span>")
			if depth == 0 {
				lines = append(lines, s[start:i])
				start = i
			}
		} else if strings.HasPrefix(s[i:], "<span") {
			depth++
			i += len("<span")
		} else {
			i++
		}
	}
	return lines
}

// highlightBlock highlights a multi-line block of code and returns per-line HTML.
// Highlighting the whole block (rather than line by line) gives the lexer enough
// context to correctly tokenize constructs that span lines (e.g. Go raw strings).
func highlightBlock(content string, lexer chroma.Lexer, style *chroma.Style, formatter *chromaHTML.Formatter) []string {
	iterator, err := lexer.Tokenise(nil, content+"\n")
	if err != nil {
		return nil
	}
	var buf strings.Builder
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return nil
	}
	s := buf.String()
	if i := strings.Index(s, "<code>"); i >= 0 {
		s = s[i+len("<code>"):]
	}
	if i := strings.LastIndex(s, "</code>"); i >= 0 {
		s = s[:i]
	}
	return splitHTMLLines(s)
}

// highlightFullFile fetches a complete file at a given SHA and returns a
// 1-indexed map of line number → highlighted HTML. Highlighting the whole file
// gives the lexer full context for constructs that span many lines.
// Returns nil (not an error) if the file doesn't exist at that SHA.
func highlightFullFile(repoPath, sha, filePath string, lexer chroma.Lexer, style *chroma.Style, formatter *chromaHTML.Formatter) map[int]string {
	lines, err := GetFileContent(repoPath, sha, filePath)
	if err != nil {
		return nil
	}
	return highlightLineMap(strings.Join(lines, "\n"), lexer, style, formatter)
}

// highlightLineMap highlights a block of source and returns a 1-indexed map of
// line number → highlighted HTML.
func highlightLineMap(content string, lexer chroma.Lexer, style *chroma.Style, formatter *chromaHTML.Formatter) map[int]string {
	htmlLines := highlightBlock(content, lexer, style, formatter)
	result := make(map[int]string, len(htmlLines))
	for i, h := range htmlLines {
		result[i+1] = h
	}
	return result
}

// HighlightFileContent highlights already-fetched file lines (e.g. from
// GetFileContent) and returns a 1-indexed map of line number → highlighted HTML.
// Reusing the fetched content avoids a second git invocation for the same blob.
func HighlightFileContent(filePath string, lines []string) map[int]string {
	return highlightLineMap(strings.Join(lines, "\n"), matchLexer(filePath), githubStyle(), htmlFormatter())
}
