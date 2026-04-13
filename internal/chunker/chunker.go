// Package chunker splits markdown documents into retrieval-friendly chunks.
//
// A chunk is typically one heading's worth of content — small enough to hand
// to an LLM as context, large enough to carry a coherent idea. Chunks carry
// the full heading path ("Part 1 > Section A") so a ranker or reader can
// judge relevance without loading the parent file.
package chunker

import (
	"regexp"
	"strings"
)

// DefaultMaxChars caps any single chunk. Oversized sections get split on
// paragraph boundaries so each piece fits comfortably in an LLM context.
const DefaultMaxChars = 2000

type Chunk struct {
	Heading string // full heading path joined by " > "; empty for pre-heading body
	Content string // trimmed chunk body, including the heading line itself
	Start   int    // 1-indexed line of the first line in Content
	End     int    // 1-indexed line of the last line in Content
}

var headingRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)

// Split parses markdown text and returns chunks. Frontmatter (--- delimited)
// at the top of the file is skipped.
func Split(text string) []Chunk {
	return SplitWithLimit(text, DefaultMaxChars)
}

func SplitWithLimit(text string, maxChars int) []Chunk {
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	lines := strings.Split(text, "\n")
	start := skipFrontmatter(lines)

	var (
		chunks    []Chunk
		stack     []string
		curHead   string
		curLines  []string
		curStart  = start + 1
	)

	flush := func(endLine int) {
		body := strings.TrimSpace(strings.Join(curLines, "\n"))
		if body == "" {
			curLines = nil
			return
		}
		chunks = append(chunks, Chunk{
			Heading: curHead,
			Content: body,
			Start:   curStart,
			End:     endLine,
		})
		curLines = nil
	}

	for i := start; i < len(lines); i++ {
		line := lines[i]
		lineNo := i + 1
		if m := headingRe.FindStringSubmatch(line); m != nil {
			flush(lineNo - 1)
			level := len(m[1])
			title := strings.TrimSpace(m[2])
			stack = adjustStack(stack, level, title)
			curHead = strings.Join(stack, " > ")
			curStart = lineNo
			curLines = []string{line}
			continue
		}
		curLines = append(curLines, line)
	}
	flush(len(lines))

	var out []Chunk
	for _, c := range chunks {
		out = append(out, splitLarge(c, maxChars)...)
	}
	return out
}

func splitLarge(c Chunk, maxChars int) []Chunk {
	if len(c.Content) <= maxChars {
		return []Chunk{c}
	}
	paras := strings.Split(c.Content, "\n\n")
	var (
		out []Chunk
		buf strings.Builder
	)
	flush := func() {
		if buf.Len() == 0 {
			return
		}
		out = append(out, Chunk{
			Heading: c.Heading,
			Content: strings.TrimSpace(buf.String()),
			Start:   c.Start,
			End:     c.End,
		})
		buf.Reset()
	}
	for _, p := range paras {
		if buf.Len()+len(p)+2 > maxChars && buf.Len() > 0 {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteString("\n\n")
		}
		buf.WriteString(p)
	}
	flush()
	return out
}

func adjustStack(stack []string, level int, title string) []string {
	if level-1 < len(stack) {
		stack = stack[:level-1]
	}
	for len(stack) < level-1 {
		stack = append(stack, "")
	}
	return append(stack, title)
}

func skipFrontmatter(lines []string) int {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i + 1
		}
	}
	return 0
}
