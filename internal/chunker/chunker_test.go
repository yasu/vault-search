package chunker

import (
	"strings"
	"testing"
)

func TestSplit_NoHeadings(t *testing.T) {
	got := Split("just a paragraph.\n\nanother one.")
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if got[0].Heading != "" {
		t.Errorf("want empty heading, got %q", got[0].Heading)
	}
	if !strings.Contains(got[0].Content, "another one") {
		t.Errorf("content missing paragraph: %q", got[0].Content)
	}
}

func TestSplit_HeadingStack(t *testing.T) {
	md := `# Top

intro under top.

## Sub A

body a.

## Sub B

body b.

# Another

body another.
`
	got := Split(md)
	if len(got) != 4 {
		t.Fatalf("want 4 chunks, got %d:\n%#v", len(got), got)
	}
	headings := []string{got[0].Heading, got[1].Heading, got[2].Heading, got[3].Heading}
	want := []string{"Top", "Top > Sub A", "Top > Sub B", "Another"}
	for i := range want {
		if headings[i] != want[i] {
			t.Errorf("chunk %d heading = %q, want %q", i, headings[i], want[i])
		}
	}
}

func TestSplit_SkipsFrontmatter(t *testing.T) {
	md := `---
title: hello
tags: [a, b]
---

# Real Heading

body.
`
	got := Split(md)
	if len(got) != 1 {
		t.Fatalf("want 1 chunk, got %d", len(got))
	}
	if got[0].Heading != "Real Heading" {
		t.Errorf("heading = %q, want Real Heading", got[0].Heading)
	}
	if strings.Contains(got[0].Content, "title: hello") {
		t.Errorf("frontmatter leaked into content")
	}
}

func TestSplit_OversizedSection(t *testing.T) {
	big := strings.Repeat("paragraph body. ", 200) // ~3200 chars
	md := "# H\n\n" + big + "\n\n" + big
	got := SplitWithLimit(md, 500)
	if len(got) < 2 {
		t.Fatalf("expected multiple chunks from oversize section, got %d", len(got))
	}
	for _, c := range got {
		if c.Heading != "H" {
			t.Errorf("sub-chunk heading = %q, want H", c.Heading)
		}
	}
}

func TestSplit_JapaneseHeadings(t *testing.T) {
	md := `# 都市論

都市は知の交差点である。

## 分裂生成

分裂生成の力学について。
`
	got := Split(md)
	if len(got) != 2 {
		t.Fatalf("want 2 chunks, got %d", len(got))
	}
	if got[1].Heading != "都市論 > 分裂生成" {
		t.Errorf("heading = %q", got[1].Heading)
	}
}
