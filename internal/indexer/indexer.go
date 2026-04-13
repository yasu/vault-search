// Package indexer manages a bleve index whose documents are markdown
// *chunks*, not whole files. One chunk ≈ one heading's worth of content.
package indexer

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/yasu/vault-search/internal/chunker"
)

type Indexer struct {
	vaultPath string
	index     bleve.Index
	mu        sync.Mutex // serializes writers; readers go through bleve directly
}

// indexDoc is the shape we hand to bleve.Index. Field names match the mapping.
type indexDoc struct {
	Path     string `json:"path"`
	Title    string `json:"title"`
	Heading  string `json:"heading"`
	Body     string `json:"body"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Modified int64  `json:"modified"`
}

func Open(indexPath, vaultPath string) (*Indexer, error) {
	var (
		idx bleve.Index
		err error
	)
	if _, statErr := os.Stat(indexPath); os.IsNotExist(statErr) {
		idx, err = bleve.New(indexPath, buildMapping())
		if err != nil {
			return nil, fmt.Errorf("bleve.New: %w", err)
		}
	} else {
		idx, err = bleve.Open(indexPath)
		if err != nil {
			return nil, fmt.Errorf("bleve.Open: %w", err)
		}
	}
	return &Indexer{vaultPath: vaultPath, index: idx}, nil
}

func buildMapping() mapping.IndexMapping {
	doc := bleve.NewDocumentMapping()

	// path is a keyword so TermQuery can find-and-delete all chunks of a file.
	path := bleve.NewTextFieldMapping()
	path.Analyzer = keyword.Name
	doc.AddFieldMappingsAt("path", path)

	title := bleve.NewTextFieldMapping()
	title.Analyzer = cjk.AnalyzerName
	doc.AddFieldMappingsAt("title", title)

	heading := bleve.NewTextFieldMapping()
	heading.Analyzer = cjk.AnalyzerName
	doc.AddFieldMappingsAt("heading", heading)

	body := bleve.NewTextFieldMapping()
	body.Analyzer = cjk.AnalyzerName
	body.Store = true
	body.IncludeTermVectors = true
	doc.AddFieldMappingsAt("body", body)

	m := bleve.NewIndexMapping()
	m.DefaultMapping = doc
	m.DefaultAnalyzer = cjk.AnalyzerName
	return m
}

func (i *Indexer) Close() error {
	return i.index.Close()
}

// InitialScan walks the vault and indexes every markdown file it finds.
// Returns the number of files indexed (not chunks).
func (i *Indexer) InitialScan(ctx context.Context) (int, error) {
	count := 0
	err := filepath.WalkDir(i.vaultPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if isHiddenDir(path, i.vaultPath, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isMarkdown(d.Name()) {
			return nil
		}
		if err := i.IndexFile(path); err != nil {
			return fmt.Errorf("index %s: %w", path, err)
		}
		count++
		return nil
	})
	return count, err
}

// IndexFile chunks the file and replaces any prior chunks for the same path.
func (i *Indexer) IndexFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	rel := i.rel(path)
	title := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	chunks := chunker.Split(string(data))

	i.mu.Lock()
	defer i.mu.Unlock()

	if err := i.deleteByPathLocked(rel); err != nil {
		return err
	}
	batch := i.index.NewBatch()
	for n, c := range chunks {
		id := fmt.Sprintf("%s#%d", rel, n)
		d := indexDoc{
			Path:     rel,
			Title:    title,
			Heading:  c.Heading,
			Body:     c.Content,
			Start:    c.Start,
			End:      c.End,
			Modified: info.ModTime().Unix(),
		}
		if err := batch.Index(id, d); err != nil {
			return err
		}
	}
	return i.index.Batch(batch)
}

func (i *Indexer) Delete(path string) error {
	rel := i.rel(path)
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.deleteByPathLocked(rel)
}

// deleteByPathLocked finds every chunk whose path field equals rel and
// removes it. Caller must hold i.mu.
func (i *Indexer) deleteByPathLocked(rel string) error {
	q := bleve.NewTermQuery(rel)
	q.SetField("path")
	req := bleve.NewSearchRequest(q)
	req.Size = 10000
	res, err := i.index.Search(req)
	if err != nil {
		return err
	}
	if len(res.Hits) == 0 {
		return nil
	}
	batch := i.index.NewBatch()
	for _, h := range res.Hits {
		batch.Delete(h.ID)
	}
	return i.index.Batch(batch)
}

type Hit struct {
	ID       string   `json:"id"`
	Path     string   `json:"path"`
	Title    string   `json:"title"`
	Heading  string   `json:"heading"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Score    float64  `json:"score"`
	Snippets []string `json:"snippets,omitempty"`
}

func (i *Indexer) Search(q string, limit int) ([]Hit, uint64, error) {
	if limit <= 0 {
		limit = 20
	}
	titleQ := bleve.NewMatchQuery(q)
	titleQ.SetField("title")
	titleQ.SetBoost(3.0)
	headingQ := bleve.NewMatchQuery(q)
	headingQ.SetField("heading")
	headingQ.SetBoost(2.0)
	bodyQ := bleve.NewMatchQuery(q)
	bodyQ.SetField("body")
	query := bleve.NewDisjunctionQuery(titleQ, headingQ, bodyQ)

	req := bleve.NewSearchRequest(query)
	req.Size = limit
	req.Fields = []string{"title", "path", "heading", "start", "end"}
	req.Highlight = bleve.NewHighlight()
	req.Highlight.AddField("body")

	res, err := i.index.Search(req)
	if err != nil {
		return nil, 0, err
	}
	hits := make([]Hit, 0, len(res.Hits))
	for _, h := range res.Hits {
		title, _ := h.Fields["title"].(string)
		path, _ := h.Fields["path"].(string)
		heading, _ := h.Fields["heading"].(string)
		start, _ := h.Fields["start"].(float64)
		end, _ := h.Fields["end"].(float64)
		hits = append(hits, Hit{
			ID:       h.ID,
			Path:     path,
			Title:    title,
			Heading:  heading,
			Start:    int(start),
			End:      int(end),
			Score:    h.Score,
			Snippets: h.Fragments["body"],
		})
	}
	return hits, res.Total, nil
}

func (i *Indexer) rel(path string) string {
	rel, err := filepath.Rel(i.vaultPath, path)
	if err != nil {
		return path
	}
	return rel
}

func isMarkdown(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".md" || ext == ".markdown"
}

func isHiddenDir(path, root, name string) bool {
	if path == root {
		return false
	}
	return strings.HasPrefix(name, ".")
}
