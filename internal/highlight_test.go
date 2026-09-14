package internal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

func TestHighlightCSSThemeScoping(t *testing.T) {
	css := HighlightCSS()
	for _, line := range strings.Split(css, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "html:not(.dark) ") && !strings.HasPrefix(line, "html.dark ") {
			t.Errorf("unscoped rule: %q", line)
		}
	}
}

func TestHighlightCSSDropsBackgroundAndWhitespaceRules(t *testing.T) {
	css := HighlightCSS()
	if strings.Contains(css, ".ch-bg {") {
		t.Error("HighlightCSS() contains a .ch-bg rule, want it dropped")
	}
	if strings.Contains(css, ".ch-chroma .ch-w {") {
		t.Error("HighlightCSS() contains a .ch-chroma .ch-w rule, want it dropped")
	}
}

func TestHighlightCSSBaseColorRule(t *testing.T) {
	css := HighlightCSS()

	dark := darkStyle().Get(chroma.Background)
	if !dark.Colour.IsSet() {
		t.Fatal("github-dark has no background colour set; base-color rule can't be derived")
	}
	want := fmt.Sprintf("html.dark .ch-chroma .ch-cl { color: %s }", dark.Colour.String())
	if !strings.Contains(css, want) {
		t.Errorf("HighlightCSS() missing dark base-color rule %q", want)
	}

	light := githubStyle().Get(chroma.Background)
	if light.Colour.IsSet() {
		wantLight := fmt.Sprintf("html:not(.dark) .ch-chroma .ch-cl { color: %s }", light.Colour.String())
		if !strings.Contains(css, wantLight) {
			t.Errorf("HighlightCSS() missing light base-color rule %q", wantLight)
		}
	} else if strings.Contains(css, "html:not(.dark) .ch-chroma .ch-cl { color:") {
		t.Error("HighlightCSS() emits a light-theme base-color rule but github has no base color set")
	}
}

func TestDeclValue(t *testing.T) {
	body := "color: #fff; background-color: #000; }"
	if got := declValue(body, "color"); got != "#fff" {
		t.Errorf("declValue(color) = %q, want #fff", got)
	}
	if got := declValue(body, "background-color"); got != "#000" {
		t.Errorf("declValue(background-color) = %q, want #000", got)
	}
	if got := declValue(body, "border"); got != "" {
		t.Errorf("declValue(border) = %q, want empty", got)
	}
}

func TestStyleOrFallback(t *testing.T) {
	if got := styleOrFallback("github"); got == nil || got.Name != "github" {
		t.Errorf("styleOrFallback(github) = %v, want the github style", got)
	}
	if got := styleOrFallback("nope-does-not-exist"); got != styles.Fallback {
		t.Errorf("styleOrFallback(unknown) = %v, want styles.Fallback", got)
	}
}

func TestMatchLexer(t *testing.T) {
	if got := matchLexer("x.go"); got.Config().Name != "Go" {
		t.Errorf("matchLexer(x.go) = %q, want Go", got.Config().Name)
	}
	if got := matchLexer("x.unknownext"); got.Config().Name != lexers.Fallback.Config().Name {
		t.Errorf("matchLexer(x.unknownext) = %q, want the fallback lexer", got.Config().Name)
	}
}

func TestSplitHTMLLinesNestedSpans(t *testing.T) {
	html := `<span class="ch-line"><span class="ch-cl"><span class="ch-s">a</span></span></span>` +
		`<span class="ch-line"><span class="ch-cl"><span class="ch-s">b</span></span></span>`
	lines := splitHTMLLines(html)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %#v", len(lines), lines)
	}
	if lines[0] != `<span class="ch-line"><span class="ch-cl"><span class="ch-s">a</span></span></span>` {
		t.Errorf("line 1 = %q", lines[0])
	}
	if lines[1] != `<span class="ch-line"><span class="ch-cl"><span class="ch-s">b</span></span></span>` {
		t.Errorf("line 2 = %q", lines[1])
	}
}

func TestSplitHTMLLinesUnstyledTrailingToken(t *testing.T) {
	html := `<span class="ch-line"><span class="ch-cl"><span class="ch-nt">a</span> <span class="ch-p">{</span>
</span></span><span class="ch-line"><span class="ch-cl">  <span class="ch-k">color</span><span class="ch-p">:</span> <span class="ch-kc">red</span><span class="ch-p">;</span>
</span></span><span class="ch-line"><span class="ch-cl"><span class="ch-p">}</span>
</span></span>`
	lines := splitHTMLLines(html)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %#v", len(lines), lines)
	}
	if !strings.Contains(lines[0], `class="ch-nt"`) || strings.Contains(lines[0], `class="ch-k"`) {
		t.Errorf("line 1 = %q, want only the selector token", lines[0])
	}
	if !strings.Contains(lines[1], `class="ch-k"`) || !strings.Contains(lines[1], `class="ch-kc"`) {
		t.Errorf("line 2 = %q, want the property and value tokens", lines[1])
	}
	if !strings.Contains(lines[2], `class="ch-p"`) || strings.Contains(lines[2], `class="ch-nt"`) {
		t.Errorf("line 3 = %q, want only the closing brace", lines[2])
	}
	if strings.Join(lines, "") != html {
		t.Error("splitHTMLLines lost or duplicated content")
	}
}

func TestHighlightBlockCrossLineContext(t *testing.T) {
	src := "x := `raw\nstring`"
	lines := highlightBlock(src, lexers.Get("go"), githubStyle(), htmlFormatter())
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %#v", len(lines), lines)
	}
	for i, l := range lines {
		if !strings.Contains(l, `class="ch-s"`) {
			t.Errorf("line %d = %q, want the raw string's ch-s class to carry across both lines", i+1, l)
		}
	}
}

func TestHighlightFileContent(t *testing.T) {
	got := HighlightFileContent("x.go", []string{"line one", "line two"})
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(got), got)
	}
	if got[1] == "" || got[2] == "" {
		t.Errorf("got %#v, want non-empty HTML for lines 1 and 2", got)
	}
}
