package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/yasu/vault-search/internal/indexer"
	"github.com/yasu/vault-search/internal/server"
	"github.com/yasu/vault-search/internal/watcher"
)

func main() {
	var (
		vaultPath = flag.String("vault", "", "path to the markdown directory to index (required)")
		indexPath = flag.String("index", ".vault-search.bleve", "path to the bleve index directory")
		addr      = flag.String("addr", ":8080", "HTTP listen address")
	)
	flag.Parse()

	if *vaultPath == "" {
		log.Fatal("--vault is required")
	}
	abs, err := os.Stat(*vaultPath)
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	if !abs.IsDir() {
		log.Fatalf("vault: %s is not a directory", *vaultPath)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	idx, err := indexer.Open(*indexPath, *vaultPath)
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	log.Printf("scanning %s ...", *vaultPath)
	n, err := idx.InitialScan(ctx)
	if err != nil {
		log.Fatalf("initial scan: %v", err)
	}
	log.Printf("indexed %d documents", n)

	w, err := watcher.New(*vaultPath, idx)
	if err != nil {
		log.Fatalf("watcher: %v", err)
	}
	go w.Run(ctx)

	srv := server.New(*addr, idx)
	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server: %v", err)
	}
}
