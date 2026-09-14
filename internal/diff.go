package internal

import (
	"fmt"
	"regexp"
	"strings"
)

type HighlightedLine struct {
	Type    string `json:"type"`
	Left    *int   `json:"left"`
	Right   *int   `json:"right"`
	Content string `json:"content"`
	HTML    string `json:"html"`
}

type HighlightedHunk struct {
	Header     string            `json:"header"`
	Lines      []HighlightedLine `json:"lines"`
	StartLeft  int               `json:"start_left"`
	StartRight int               `json:"start_right"`
	EndLeft    int               `json:"end_left"`
	EndRight   int               `json:"end_right"`
}

type HighlightedFile struct {
	Path    string            `json:"path"`
	OldPath string            `json:"old_path"`
	Status  string            `json:"status"`
	Binary  bool              `json:"binary"`
	Hunks   []HighlightedHunk `json:"hunks"`
}

// GetParsedDiff returns the structured diff between two SHAs. When highlight is
// set, each line's HTML field is filled with syntax-highlighted markup; the full
// file is highlighted at each SHA so the lexer has cross-line context (e.g. raw
// string literals spanning many lines).
func GetParsedDiff(repoPath, baseSHA, headSHA string, highlight bool) ([]HighlightedFile, error) {
	diff, err := getDiff(repoPath, baseSHA, headSHA)
	if err != nil {
		return nil, err
	}

	files := parseDiffRaw(diff)

	if highlight {
		style := githubStyle()
		formatter := htmlFormatter()

		for fi := range files {
			// Paths are normalized in parseDiffRaw; only the addition case
			// (OldPath == "") still needs a fallback so the base-SHA highlight
			// pass has a path to resolve against.
			newPath := files[fi].Path
			oldPath := files[fi].OldPath
			if oldPath == "" {
				oldPath = newPath
			}

			lexer := matchLexer(newPath)

			// Highlight the complete file at each SHA so the lexer has full
			// cross-line context (e.g. raw string literals spanning many lines).
			newLines := highlightFullFile(repoPath, headSHA, newPath, lexer, style, formatter)
			oldLines := highlightFullFile(repoPath, baseSHA, oldPath, lexer, style, formatter)

			for hi := range files[fi].Hunks {
				for li := range files[fi].Hunks[hi].Lines {
					l := &files[fi].Hunks[hi].Lines[li]
					switch l.Type {
					case "add":
						if l.Right != nil {
							l.HTML = newLines[*l.Right]
						}
					case "del":
						if l.Left != nil {
							l.HTML = oldLines[*l.Left]
						}
					default: // ctx
						if l.Right != nil {
							l.HTML = newLines[*l.Right]
						}
					}
				}
			}
		}
	}

	if files == nil {
		files = []HighlightedFile{}
	}
	return files, nil
}

// parseDiffRaw parses a unified diff string into HighlightedFile structs
// with HTML fields left empty (to be filled by the caller).
func parseDiffRaw(text string) []HighlightedFile {
	var files []HighlightedFile
	var headerPaths []string
	var file *HighlightedFile
	var hunk *HighlightedHunk
	var leftLine, rightLine int

	for _, raw := range strings.Split(text, "\n") {
		if strings.HasPrefix(raw, "diff --git ") {
			file = &HighlightedFile{Hunks: []HighlightedHunk{}, Status: "M"}
			files = append(files, *file)
			file = &files[len(files)-1]
			headerPaths = append(headerPaths, diffGitPath(raw))
			hunk = nil
		} else if file == nil {
			continue
		} else if strings.HasPrefix(raw, "Binary files ") {
			file.Binary = true
		} else if strings.HasPrefix(raw, "new file mode ") {
			file.Status = "A"
		} else if strings.HasPrefix(raw, "deleted file mode ") {
			file.Status = "D"
		} else if strings.HasPrefix(raw, "rename from ") {
			// A pure rename (100% similarity) emits no ---/+++ lines; these
			// rename headers are the only path source, so parse them to keep
			// the file in the diff rather than filtering it out as empty.
			file.OldPath = strings.TrimPrefix(raw, "rename from ")
			file.Status = "R"
		} else if strings.HasPrefix(raw, "rename to ") {
			file.Path = strings.TrimPrefix(raw, "rename to ")
			file.Status = "R"
		} else if strings.HasPrefix(raw, "--- ") {
			file.OldPath = strings.TrimPrefix(safeSlice(raw, 4), "a/")
		} else if strings.HasPrefix(raw, "+++ ") {
			file.Path = strings.TrimPrefix(safeSlice(raw, 4), "b/")
		} else if strings.HasPrefix(raw, "@@ ") {
			m := hunkHeaderRe.FindStringSubmatch(raw)
			if m != nil {
				fmt.Sscanf(m[1], "%d", &leftLine)
				fmt.Sscanf(m[2], "%d", &rightLine)
				hunk = &HighlightedHunk{
					Header:     raw,
					StartLeft:  leftLine,
					StartRight: rightLine,
					EndLeft:    leftLine,
					EndRight:   rightLine,
				}
				file.Hunks = append(file.Hunks, *hunk)
				hunk = &file.Hunks[len(file.Hunks)-1]
			}
		} else if hunk != nil {
			if raw == "\\ No newline at end of file" || raw == "" {
				continue
			}
			var hl HighlightedLine
			if strings.HasPrefix(raw, "+") {
				hl = HighlightedLine{Type: "add", Right: intPtr(rightLine), Content: safeSlice(raw, 1)}
				rightLine++
				hunk.EndRight = rightLine
			} else if strings.HasPrefix(raw, "-") {
				hl = HighlightedLine{Type: "del", Left: intPtr(leftLine), Content: safeSlice(raw, 1)}
				leftLine++
				hunk.EndLeft = leftLine
			} else {
				hl = HighlightedLine{Type: "ctx", Left: intPtr(leftLine), Right: intPtr(rightLine), Content: safeSlice(raw, 1)}
				leftLine++
				rightLine++
				hunk.EndLeft = leftLine
				hunk.EndRight = rightLine
			}
			hunk.Lines = append(hunk.Lines, hl)
		}
	}
	// Normalize the /dev/null sentinels git emits for deletions and additions.
	// A deleted file has `+++ /dev/null`, an added file has `--- /dev/null`.
	// Store the real path in Path so headers, DOM ids, pill scroll targets, and
	// stored comment file paths (also what `reviewer status` reports) all agree.
	// Binary and mode-only changes emit no ---/+++ lines at all, so they fall
	// back to the path on the `diff --git` header line.
	for i := range files {
		if files[i].Path == "" || files[i].Path == "/dev/null" {
			files[i].Path = files[i].OldPath
		}
		if files[i].OldPath == "/dev/null" {
			files[i].OldPath = ""
		}
		if files[i].Path == "" {
			files[i].Path = headerPaths[i]
		}
	}
	out := files[:0]
	for _, f := range files {
		if f.Path != "" {
			out = append(out, f)
		}
	}
	return out
}

// diffGitPath recovers the file path from a `diff --git a/P b/P` header, the
// only path source for entries git emits without ---/+++ lines. Non-renames
// repeat the same path, so it is recovered by length and verified, which keeps
// paths containing spaces intact; renames fall back to the trailing `b/` path.
func diffGitPath(raw string) string {
	s := strings.TrimPrefix(raw, "diff --git ")
	if !strings.HasPrefix(s, "a/") {
		return ""
	}
	if n := (len(s) - 5) / 2; n > 0 && len(s) == 2*n+5 && s[2+n:2+n+3] == " b/" {
		if a, b := s[2:2+n], s[2+n+3:]; a == b {
			return a
		}
	}
	if i := strings.LastIndex(s, " b/"); i > 2 {
		return s[i+3:]
	}
	return ""
}

func intPtr(n int) *int { return &n }

func safeSlice(s string, start int) string {
	if len(s) <= start {
		return ""
	}
	return s[start:]
}

var hunkHeaderRe = regexp.MustCompile(`@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

type HunkRange struct {
	OldStart, OldCount, NewStart, NewCount int
}

var hunkRangeRe = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func ParseHunkRanges(text string) []HunkRange {
	var out []HunkRange
	for _, raw := range strings.Split(text, "\n") {
		m := hunkRangeRe.FindStringSubmatch(raw)
		if m == nil {
			continue
		}
		h := HunkRange{OldCount: 1, NewCount: 1}
		fmt.Sscanf(m[1], "%d", &h.OldStart)
		if m[2] != "" {
			fmt.Sscanf(m[2], "%d", &h.OldCount)
		}
		fmt.Sscanf(m[3], "%d", &h.NewStart)
		if m[4] != "" {
			fmt.Sscanf(m[4], "%d", &h.NewCount)
		}
		out = append(out, h)
	}
	return out
}

func MapLine(hunks []HunkRange, line int) int {
	delta := 0
	for _, h := range hunks {
		after := h.OldStart + h.OldCount
		if h.OldCount == 0 {
			after = h.OldStart + 1
		}
		if line >= after {
			delta += h.NewCount - h.OldCount
			continue
		}
		if h.OldCount > 0 && line >= h.OldStart {
			return 0
		}
		break
	}
	return line + delta
}
