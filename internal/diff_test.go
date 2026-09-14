package internal

import (
	"strings"
	"testing"
)

// TestParseDiffRawDeletedFile verifies that a deleted file's Path holds the
// real (former) path rather than the /dev/null sentinel git emits on the +++
// line. The stored path feeds DOM ids, pill scroll targets, and comment file
// paths (also what `reviewer status` reports), so /dev/null there corrupts all
// of them.
func TestParseDiffRawDeletedFile(t *testing.T) {
	diff := `diff --git a/old.txt b/old.txt
deleted file mode 100644
index 1234567..0000000
--- a/old.txt
+++ /dev/null
@@ -1,2 +0,0 @@
-line one
-line two
`
	files := parseDiffRaw(diff)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Path != "old.txt" {
		t.Errorf("Path = %q, want %q", files[0].Path, "old.txt")
	}
	if files[0].OldPath != "old.txt" {
		t.Errorf("OldPath = %q, want %q", files[0].OldPath, "old.txt")
	}
}

// TestParseDiffRawAddedFile verifies the mirror case: an added file's --- line
// is /dev/null, which must not leak into OldPath.
func TestParseDiffRawAddedFile(t *testing.T) {
	diff := `diff --git a/new.txt b/new.txt
new file mode 100644
index 0000000..89abcde
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+line one
+line two
`
	files := parseDiffRaw(diff)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Path != "new.txt" {
		t.Errorf("Path = %q, want %q", files[0].Path, "new.txt")
	}
	if files[0].OldPath != "" {
		t.Errorf("OldPath = %q, want empty", files[0].OldPath)
	}
}

// TestParseDiffRawTwoDeletedFilesDistinct guards the DOM-id collision: two
// deleted files must carry distinct paths, not both /dev/null.
func TestParseDiffRawTwoDeletedFilesDistinct(t *testing.T) {
	diff := `diff --git a/a.txt b/a.txt
deleted file mode 100644
index 1111111..0000000
--- a/a.txt
+++ /dev/null
@@ -1 +0,0 @@
-alpha
diff --git a/b.txt b/b.txt
deleted file mode 100644
index 2222222..0000000
--- a/b.txt
+++ /dev/null
@@ -1 +0,0 @@
-beta
`
	files := parseDiffRaw(diff)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Path == files[1].Path {
		t.Errorf("deleted files share Path %q", files[0].Path)
	}
	if files[0].Path != "a.txt" || files[1].Path != "b.txt" {
		t.Errorf("paths = %q, %q; want a.txt, b.txt", files[0].Path, files[1].Path)
	}
}

// TestParseDiffRawModifiedFile confirms the ordinary case still round-trips
// both paths.
func TestParseDiffRawModifiedFile(t *testing.T) {
	diff := `diff --git a/mod.txt b/mod.txt
index 1111111..2222222 100644
--- a/mod.txt
+++ b/mod.txt
@@ -1 +1 @@
-before
+after
`
	files := parseDiffRaw(diff)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Path != "mod.txt" || files[0].OldPath != "mod.txt" {
		t.Errorf("Path=%q OldPath=%q, want both mod.txt", files[0].Path, files[0].OldPath)
	}
}

// TestParseDiffRawPureRename covers a 100%-similarity rename: git emits only
// rename from/to headers and no ---/+++ or hunks. The file must survive with
// both paths set rather than being filtered out as empty.
func TestParseDiffRawPureRename(t *testing.T) {
	diff := `diff --git a/old.txt b/new.txt
similarity index 100%
rename from old.txt
rename to new.txt
`
	files := parseDiffRaw(diff)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Path != "new.txt" || files[0].OldPath != "old.txt" {
		t.Errorf("Path=%q OldPath=%q, want new.txt / old.txt", files[0].Path, files[0].OldPath)
	}
	if len(files[0].Hunks) != 0 {
		t.Errorf("want 0 hunks, got %d", len(files[0].Hunks))
	}
}

// TestParseDiffRawRenameWithEdits covers a rename that also changes content:
// rename headers plus ---/+++ and a hunk. Both paths come from the rename
// headers and the content change is parsed.
func TestParseDiffRawRenameWithEdits(t *testing.T) {
	diff := `diff --git a/old.txt b/new.txt
similarity index 80%
rename from old.txt
rename to new.txt
index 1111111..2222222 100644
--- a/old.txt
+++ b/new.txt
@@ -1 +1 @@
-before
+after
`
	files := parseDiffRaw(diff)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Path != "new.txt" || files[0].OldPath != "old.txt" {
		t.Errorf("Path=%q OldPath=%q, want new.txt / old.txt", files[0].Path, files[0].OldPath)
	}
	if len(files[0].Hunks) != 1 {
		t.Errorf("want 1 hunk, got %d", len(files[0].Hunks))
	}
}

func TestParseDiffRawStatuses(t *testing.T) {
	diff := `diff --git a/added.txt b/added.txt
new file mode 100644
index 0000000..89abcde
--- /dev/null
+++ b/added.txt
@@ -0,0 +1 @@
+hello
diff --git a/gone.txt b/gone.txt
deleted file mode 100644
index 1234567..0000000
--- a/gone.txt
+++ /dev/null
@@ -1 +0,0 @@
-bye
diff --git a/edited.txt b/edited.txt
index 1111111..2222222 100644
--- a/edited.txt
+++ b/edited.txt
@@ -1 +1 @@
-before
+after
diff --git a/from.txt b/to.txt
similarity index 100%
rename from from.txt
rename to to.txt
`
	files := parseDiffRaw(diff)
	if len(files) != 4 {
		t.Fatalf("want 4 files, got %d", len(files))
	}
	want := []struct{ path, status string }{
		{"added.txt", "A"},
		{"gone.txt", "D"},
		{"edited.txt", "M"},
		{"to.txt", "R"},
	}
	for i, w := range want {
		if files[i].Path != w.path || files[i].Status != w.status {
			t.Errorf("file %d = (%q, %q), want (%q, %q)", i, files[i].Path, files[i].Status, w.path, w.status)
		}
	}
}

func TestParseDiffRawBinaryFilesGetDistinctPaths(t *testing.T) {
	diff := `diff --git a/logo.png b/logo.png
new file mode 100644
index 0000000..aaaaaaa
Binary files /dev/null and b/logo.png differ
diff --git a/data.bin b/data.bin
new file mode 100644
index 0000000..bbbbbbb
Binary files /dev/null and b/data.bin differ
`
	files := parseDiffRaw(diff)
	if len(files) != 2 {
		t.Fatalf("want 2 files, got %d", len(files))
	}
	if files[0].Path != "logo.png" || files[1].Path != "data.bin" {
		t.Fatalf("paths = %q, %q; want logo.png, data.bin", files[0].Path, files[1].Path)
	}
	for i := range files {
		if !files[i].Binary {
			t.Errorf("file %d: Binary = false, want true", i)
		}
		if files[i].Status != "A" {
			t.Errorf("file %d: Status = %q, want %q", i, files[i].Status, "A")
		}
	}
}

func TestDiffGitPathHandlesSpaces(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"diff --git a/dir/file.go b/dir/file.go", "dir/file.go"},
		{"diff --git a/my file.txt b/my file.txt", "my file.txt"},
		{"diff --git a/a b/c b/a b/c", "a b/c"},
		{"diff --git a/old.txt b/new.txt", "new.txt"},
		{"diff --git a/only", ""},
		{"nonsense", ""},
	}
	for _, c := range cases {
		if got := diffGitPath(c.raw); got != c.want {
			t.Errorf("diffGitPath(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

func TestMapLineShiftsAroundHunks(t *testing.T) {
	hunks := ParseHunkRanges("@@ -4,2 +4,3 @@\n@@ -8,0 +9,2 @@\n")
	cases := []struct{ in, want int }{
		{1, 1},  // before every hunk
		{3, 3},  // immediately before the first hunk
		{4, 0},  // rewritten
		{5, 0},  // rewritten
		{6, 7},  // after a +1 hunk
		{8, 9},  // last line before the insertion
		{9, 12}, // after both hunks: +1 then +2
		{10, 13},
	}
	for _, c := range cases {
		if got := MapLine(hunks, c.in); got != c.want {
			t.Errorf("MapLine(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestMapLineDeletionAndNoChange(t *testing.T) {
	del := ParseHunkRanges("@@ -3,2 +2,0 @@\n")
	if got := MapLine(del, 3); got != 0 {
		t.Errorf("deleted line: got %d, want 0", got)
	}
	if got := MapLine(del, 5); got != 3 {
		t.Errorf("line after deletion: got %d, want 3", got)
	}
	if got := MapLine(nil, 42); got != 42 {
		t.Errorf("no hunks: got %d, want 42", got)
	}
}

func TestParseHunkRangesDefaultsCountToOne(t *testing.T) {
	h := ParseHunkRanges("@@ -7 +7 @@ func main() {\n")
	if len(h) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(h))
	}
	if h[0].OldStart != 7 || h[0].OldCount != 1 || h[0].NewStart != 7 || h[0].NewCount != 1 {
		t.Errorf("got %+v, want single-line ranges at 7", h[0])
	}
}

func TestParseDiffRawTrailingNewlineAddsNoPhantomLine(t *testing.T) {
	diff := "diff --git a/f.txt b/f.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/f.txt\n" +
		"+++ b/f.txt\n" +
		"@@ -1,2 +1,2 @@\n" +
		" keep\n" +
		"-before\n" +
		"+after\n"
	files := parseDiffRaw(diff)
	if len(files) != 1 || len(files[0].Hunks) != 1 {
		t.Fatalf("want 1 file with 1 hunk, got %+v", files)
	}
	h := files[0].Hunks[0]
	if len(h.Lines) != 3 {
		t.Fatalf("want 3 lines, got %d: %#v", len(h.Lines), h.Lines)
	}
	if h.EndLeft != 3 || h.EndRight != 3 {
		t.Errorf("EndLeft/EndRight = %d/%d, want 3/3", h.EndLeft, h.EndRight)
	}
}

func TestParseDiffRawSkipsNoNewlineMarker(t *testing.T) {
	diff := "diff --git a/f.txt b/f.txt\n" +
		"index 1111111..2222222 100644\n" +
		"--- a/f.txt\n" +
		"+++ b/f.txt\n" +
		"@@ -1 +1 @@\n" +
		"-before\n" +
		"\\ No newline at end of file\n" +
		"+after\n" +
		"\\ No newline at end of file\n"
	files := parseDiffRaw(diff)
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if len(files[0].Hunks) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(files[0].Hunks))
	}
	lines := files[0].Hunks[0].Lines
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d: %#v", len(lines), lines)
	}
	if lines[0].Type != "del" || lines[0].Content != "before" {
		t.Errorf("line 1 = %+v, want del/before", lines[0])
	}
	if lines[1].Type != "add" || lines[1].Content != "after" {
		t.Errorf("line 2 = %+v, want add/after", lines[1])
	}
}

func TestGetParsedDiffHighlightSelectsCorrectSide(t *testing.T) {
	r := newTestRepo(t)
	base := r.Commit("main.go", "package main\n\nfunc f() {\n\tx := \"old\"\n\t_ = x\n}\n", "base")
	head := r.Commit("main.go", "package main\n\nfunc f() {\n\tx := \"new\"\n\t_ = x\n}\n", "head")

	files, err := GetParsedDiff(r.Work, base, head, true)
	if err != nil {
		t.Fatalf("GetParsedDiff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}

	var del, add *HighlightedLine
	for hi := range files[0].Hunks {
		for li := range files[0].Hunks[hi].Lines {
			l := &files[0].Hunks[hi].Lines[li]
			switch l.Type {
			case "del":
				del = l
			case "add":
				add = l
			}
		}
	}
	if del == nil || add == nil {
		t.Fatalf("want both a del and an add line, got del=%v add=%v", del, add)
	}
	if !strings.Contains(del.HTML, "old") || strings.Contains(del.HTML, "new") {
		t.Errorf("del.HTML = %q, want it to come from the old file", del.HTML)
	}
	if !strings.Contains(add.HTML, "new") || strings.Contains(add.HTML, "old") {
		t.Errorf("add.HTML = %q, want it to come from the new file", add.HTML)
	}
}

func TestGetParsedDiffHighlightContextLine(t *testing.T) {
	r := newTestRepo(t)
	base := r.Commit("main.go", "package main\n\nfunc f() {\n\tx := \"old\"\n\t_ = x\n}\n", "base")
	head := r.Commit("main.go", "package main\n\nfunc f() {\n\tx := \"new\"\n\t_ = x\n}\n", "head")

	files, err := GetParsedDiff(r.Work, base, head, true)
	if err != nil {
		t.Fatalf("GetParsedDiff: %v", err)
	}

	var ctx *HighlightedLine
	for hi := range files[0].Hunks {
		for li := range files[0].Hunks[hi].Lines {
			l := &files[0].Hunks[hi].Lines[li]
			if l.Type == "ctx" && strings.Contains(l.Content, "_ = x") {
				ctx = l
			}
		}
	}
	if ctx == nil {
		t.Fatal("want a context line containing `_ = x`")
	}
	if !strings.Contains(ctx.HTML, `class="ch-`) || !strings.Contains(ctx.HTML, "x") {
		t.Errorf("ctx.HTML = %q, want highlighted markup for the unchanged line", ctx.HTML)
	}
}

func TestGetParsedDiffAddedFileNoOldBlob(t *testing.T) {
	r := newTestRepo(t)
	base := r.Commit("keep.txt", "keep\n", "base")
	head := r.Commit("new.go", "package main\n\nfunc f() {}\n", "add new file")

	files, err := GetParsedDiff(r.Work, base, head, true)
	if err != nil {
		t.Fatalf("GetParsedDiff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Status != "A" {
		t.Fatalf("Status = %q, want A", files[0].Status)
	}
	for hi := range files[0].Hunks {
		for li := range files[0].Hunks[hi].Lines {
			l := files[0].Hunks[hi].Lines[li]
			if l.Type == "add" && l.HTML == "" {
				t.Errorf("add line %+v has empty HTML", l)
			}
		}
	}
}

func TestGetParsedDiffDeletedFileNoNewBlob(t *testing.T) {
	r := newTestRepo(t)
	r.Commit("gone.go", "package main\n\nfunc f() {}\n", "add file")
	base := r.SHA("HEAD")
	head := r.Remove("gone.go", "remove file")

	files, err := GetParsedDiff(r.Work, base, head, true)
	if err != nil {
		t.Fatalf("GetParsedDiff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	if files[0].Status != "D" {
		t.Fatalf("Status = %q, want D", files[0].Status)
	}
	for hi := range files[0].Hunks {
		for li := range files[0].Hunks[hi].Lines {
			l := files[0].Hunks[hi].Lines[li]
			if l.Type == "del" && l.HTML == "" {
				t.Errorf("del line %+v has empty HTML", l)
			}
		}
	}
}

func TestGetParsedDiffIdenticalShasReturnsEmptyNotNil(t *testing.T) {
	r := newTestRepo(t)
	head := r.Commit("f.txt", "content\n", "only commit")

	files, err := GetParsedDiff(r.Work, head, head, true)
	if err != nil {
		t.Fatalf("GetParsedDiff: %v", err)
	}
	if files == nil {
		t.Fatal("got nil, want a non-nil empty slice")
	}
	if len(files) != 0 {
		t.Fatalf("want 0 files, got %d", len(files))
	}
}
