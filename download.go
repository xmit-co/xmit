package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/fxamacker/cbor/v2"
	"github.com/xmit-co/xmit/protocol"
	"github.com/zeebo/blake3"
)

func download(key, domain, id, destination string) error {
	downloadParallelism := 3
	if s := os.Getenv("DOWNLOAD_PARALLELISM"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			downloadParallelism = v
		}
	}

	// Discover endpoint
	log.Print("🔍 Discovering endpoint…")
	discovery, err := protocol.Discover()
	if err != nil {
		return fmt.Errorf("discovering endpoint: %w", err)
	}
	log.Printf("🌐 Using URL: %s", discovery.URL)

	downloader, err := protocol.NewParallelDownloader(discovery.URL, downloadParallelism)
	if err != nil {
		return fmt.Errorf("creating parallel downloader: %w", err)
	}

	resp, err := downloader.DownloadBundle(key, domain, id)
	if err != nil {
		return fmt.Errorf("downloading bundle: %w", err)
	}
	if !resp.Response.Success {
		return fmt.Errorf("downloading bundle, server-side: %v", resp.Response.Errors)
	}
	var node protocol.Node
	if err := cbor.NewDecoder(bytes.NewReader(resp.Bundle)).Decode(&node); err != nil {
		return fmt.Errorf("unmarshaling bundle: %w", err)
	}

	return downloadTraversal(downloader, key, domain, &node, destination)
}

// safePath ensures the resulting path stays within the base directory
func safePath(base, name string) (string, error) {
	joined := filepath.Join(base, name)
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", fmt.Errorf("getting absolute base path: %w", err)
	}
	absJoined, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("getting absolute joined path: %w", err)
	}
	// Ensure the joined path is within the base directory
	if !strings.HasPrefix(absJoined, absBase+string(filepath.Separator)) && absJoined != absBase {
		return "", fmt.Errorf("path traversal detected: %s escapes %s", name, base)
	}
	return joined, nil
}

func downloadTraversal(downloader *protocol.ParallelDownloader, key, domain string, node *protocol.Node, destination string) error {
	if node.Hash != nil {
		hash := *node.Hash
		if b, err := os.ReadFile(destination); err == nil {
			h2 := protocol.Hash(blake3.Sum256(b))
			if bytes.Equal(h2[:], hash[:]) {
				return nil
			}
		}
		log.Printf("🎁 Downloading %s", destination)
		resp, err := downloader.DownloadParts(key, domain, []protocol.Hash{hash})
		if err != nil {
			return fmt.Errorf("downloading part: %w", err)
		}
		if !resp.Response.Success {
			return fmt.Errorf("downloading part, server-side: %v", resp.Response.Errors)
		}
		if len(resp.Parts) == 0 {
			return fmt.Errorf("no part found for %s", hash)
		}
		if err := os.WriteFile(destination, resp.Parts[0], 0644); err != nil {
			return fmt.Errorf("writing file: %w", err)
		}
		log.Printf("✅ Downloaded %s", destination)
	} else {
		if err := os.MkdirAll(destination, 0755); err != nil {
			return err
		}
		var errors []error
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, child := range node.Children {
			childPath, err := safePath(destination, name)
			if err != nil {
				mu.Lock()
				errors = append(errors, err)
				mu.Unlock()
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := downloadTraversal(downloader, key, domain, child, childPath); err != nil {
					mu.Lock()
					errors = append(errors, err)
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if len(errors) > 0 {
			for _, err := range errors {
				log.Printf("🛑 Failed to download: %v", err)
			}
			return fmt.Errorf("%d subtraversals failed", len(errors))
		}
	}
	return nil
}
