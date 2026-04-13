# vault-search

A small Go service that indexes a directory of Markdown notes (e.g. an
Obsidian vault) and exposes full-text search over HTTP. The index stays in
sync with the filesystem automatically.

## Features

- **Chunk-level indexing.** Files are split on Markdown headings, so search
  hits point at the relevant section rather than the whole file. Oversized
  sections are split further on paragraph boundaries to stay LLM-friendly.
- **CJK-aware full-text search** via [Bleve](https://github.com/blevesearch/bleve)
  using the `cjk` analyzer — works for Japanese/Chinese/Korean alongside
  Latin text.
- **Live updates.** A [fsnotify](https://github.com/fsnotify/fsnotify)
  watcher keeps the index in sync with create/write/rename/delete events,
  with 300 ms debouncing so editor save storms only trigger one reindex.
- **Relevance-tuned ranking.** Title matches are boosted 3×, heading
  matches 2×, body matches 1×.
- **Highlighted snippets** returned with each hit.
- **YAML frontmatter** at the top of a file is skipped automatically.

## Requirements

- Go 1.23+

## Build

```sh
go build -o vault-search .
```

## Run

```sh
./vault-search --vault /path/to/notes
```

Flags:

| Flag      | Default                  | Description                                 |
| --------- | ------------------------ | ------------------------------------------- |
| `--vault` | *(required)*             | Path to the Markdown directory to index.    |
| `--index` | `.vault-search.bleve`    | Path to the Bleve index directory.          |
| `--addr`  | `:8080`                  | HTTP listen address.                        |

On start-up the service performs an initial scan of the vault, then begins
watching for changes. Hidden directories (names starting with `.`) are
skipped. Only files ending in `.md` or `.markdown` are indexed.

## HTTP API

### `GET /search`

Query parameters:

- `q` (required) — search query.
- `limit` (optional, default `20`, max `200`) — maximum number of hits.

Example:

```sh
curl 'http://localhost:8080/search?q=vector+database&limit=10'
```

Response:

```json
{
  "query": "vector database",
  "total": 3,
  "hits": [
    {
      "id": "notes/db.md#2",
      "path": "notes/db.md",
      "title": "db",
      "heading": "Storage > Vector index",
      "start": 42,
      "end": 78,
      "score": 1.84,
      "snippets": ["... a <mark>vector</mark> <mark>database</mark> ..."]
    }
  ]
}
```

### `GET /healthz`

Returns `200 OK` — a liveness probe.

## How it works

- [main.go](main.go) wires everything together: open the index, run the
  initial scan, start the watcher, then serve HTTP until `SIGINT`/`SIGTERM`.
- [internal/chunker](internal/chunker/) splits a Markdown document into
  chunks using headings as primary boundaries, carrying the full heading
  path (e.g. `Part 1 > Section A`) on each chunk.
- [internal/indexer](internal/indexer/) wraps Bleve. Each chunk becomes one
  document keyed by `<relative-path>#<n>`. Reindexing a file first deletes
  all prior chunks for that path, then writes the new ones in a batch.
- [internal/watcher](internal/watcher/) uses fsnotify to track changes and
  debounces rapid writes before calling back into the indexer.
- [internal/server](internal/server/) exposes `GET /search` and
  `GET /healthz`.

## License

No license specified.
