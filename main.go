package main

import (
	"bytes"
	"cmp"
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/fxamacker/cbor/v2"
	"github.com/kirsle/configdir"
	"github.com/xmit-co/xmit/preview"
	"github.com/xmit-co/xmit/protocol"
	"github.com/zeebo/blake3"
	"golang.org/x/term"
)

type ingestion struct {
	protocol.Node
	contents map[protocol.Hash][]byte
}

var (
	keyPath = path.Join(configdir.LocalConfig("xmit"), "key")
)

func findKey() string {
	if key, found := os.LookupEnv("XMIT_KEY"); found {
		return key
	}
	if b, err := os.ReadFile(keyPath); err == nil {
		return strings.TrimSpace(string(b))
	}
	return ""
}

func findDirectory() string {
	var directory string
	if len(os.Args) > 2 {
		directory = os.Args[2]
	} else if _, err := os.Stat("dist"); !os.IsNotExist(err) {
		directory = "dist"
	} else {
		directory = "."
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		log.Fatalf("🛑 Failed to get absolute path: %v", err)
	}
	return directory
}

func storeKey(key string) error {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte(key), 0600)
}

func usage() {
	fmt.Println("Usage:")
	fmt.Println("  xmit set-key [KEY] (or set XMIT_KEY) → configure your API key")
	fmt.Println("  xmit DOMAIN [DIRECTORY] → upload to DOMAIN")
	fmt.Println("  xmit preview [DIRECTORY] → serve a preview locally (set LISTEN to override :4000)")
	fmt.Println("  xmit download DOMAIN[@ID] DIRECTORY → download from DOMAIN to DIRECTORY (specify an upload ID or omit ID for latest)")
}

func chunkSlice(data [][]byte, maxSize int) [][][]byte {
	var result [][][]byte
	var currentChunk [][]byte
	var currentSize int

	for _, item := range data {
		itemSize := len(item)
		// If adding this item to the current chunk exceeds maxSize, add the current chunk to result
		// and start a new chunk.
		if currentSize+itemSize > maxSize && currentSize > 0 {
			result = append(result, currentChunk)
			currentChunk = nil
			currentSize = 0
		}
		// Add the item to the current chunk and update the current size.
		currentChunk = append(currentChunk, item)
		currentSize += itemSize
	}
	// Add the last chunk if it's not empty.
	if len(currentChunk) > 0 {
		result = append(result, currentChunk)
	}

	return result
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	domain := os.Args[1]

	if domain == "-h" || domain == "--help" {
		usage()
		os.Exit(0)
	}

	if domain == "set-key" {
		var key string
		if len(os.Args) > 2 {
			key = os.Args[2]
		} else {
			fmt.Println("API keys are provisioned for users or teams after logging into https://xmit.co/admin\nUser keys are best on your personal machines, team keys for CI/CD systems.\n🔑 Enter your API key (no echo):")
			keyBytes, err := term.ReadPassword(int(syscall.Stdin))
			if err != nil {
				log.Fatalf("🛑 Failed to read API key: %v", err)
			}
			key = string(keyBytes)
		}
		if err := storeKey(key); err != nil {
			log.Fatalf("🛑 Failed to store API key: %v", err)
		}
		os.Exit(0)
	}

	if domain == "preview" {
		if err := preview.Serve(findDirectory()); err != nil {
			log.Fatalf("🛑 Failed to preview: %v", err)
		}
		return
	}

	key := findKey()
	if key == "" {
		log.Fatalf("🛑 No key found. Set XMIT_KEY or run 'xmit set-key'.")
	}

	client := protocol.NewClient()

	if domain == "download" {
		if len(os.Args) < 4 {
			log.Fatalf("🛑 Missing domain[@id] destination arguments")
		}
		domainAndID := os.Args[2]
		parts := strings.SplitN(domainAndID, "@", 2)
		domain = parts[0]
		id := ""
		if len(parts) == 2 {
			id = parts[1]
		}
		destination := os.Args[3]
		if err := download(key, domain, id, destination); err != nil {
			log.Fatalf("🛑 Failed to download: %v", err)
		}
		return
	}

	directory := findDirectory()

	log.Printf("📦 Bundling %s…", directory)
	b, err := ingest(directory)
	if err != nil {
		log.Fatalf("🛑 Failed to ingest: %v", err)
	}
	bb, err := client.EncMode.Marshal(b.Node)
	if err != nil {
		log.Fatalf("🛑 Failed to marshal: %v", err)
	}

	bytes := 0
	for _, value := range b.contents {
		bytes += len(value)
	}
	log.Printf("🎁 Bundled %d files (%d bytes)", len(b.contents), bytes)

	bbh := blake3.Sum256(bb)
	var toUpload [][]byte

	suggestResp, err := client.SuggestBundle(key, domain, bbh)
	if err != nil {
		log.Fatalf("🛑 Failed to suggest bundle: %v", err)
	}

	printMessages(suggestResp.Response)
	if !suggestResp.Response.Success {
		log.Fatalf("🛑 Bundle suggestion failed")
	}

	for _, h := range suggestResp.Missing {
		toUpload = append(toUpload, b.contents[h])
	}

	if !suggestResp.Present {
		bundleResp, err := client.UploadBundle(key, domain, bb)
		if err != nil {
			log.Fatalf("🛑 Failed to upload: %v", err)
		}

		printMessages(bundleResp.Response)
		if !bundleResp.Response.Success {
			log.Fatalf("🛑 Bundle upload failed")
		}

		for _, h := range bundleResp.Missing {
			toUpload = append(toUpload, b.contents[h])
		}
	}

	if len(toUpload) > 0 {
		// Sort toUpload by decreasing size
		slices.SortFunc(toUpload, func(i, j []byte) int {
			return cmp.Compare(len(j), len(i))
		})

		// Chunk toUpload into 10MB+ slices
		chunks := chunkSlice(toUpload, 10*1024*1024)

		for i, chunk := range chunks {
			missingResp, err := client.UploadMissing(key, domain, i, len(chunks), chunk)
			if err != nil {
				log.Fatalf("🛑 Failed to upload: %v", err)
			}

			printMessages(missingResp.Response)
			if !missingResp.Response.Success {
				log.Fatalf("🛑 Missing parts upload failed")
			}
		}
	}

	finalizeResp, err := client.Finalize(key, domain, bbh)
	if err != nil {
		log.Fatalf("🛑 Failed to finalize: %v", err)
	}

	printMessages(finalizeResp.Response)
	if !finalizeResp.Response.Success {
		log.Fatalf("🛑 Finalization failed")
	}
}

func download(key, domain, id, destination string) error {
	concurrency := 8
	concurrencyStr := os.Getenv("DOWNLOAD_CONCURRENCY")
	var err error
	if concurrencyStr != "" {
		concurrency, err = strconv.Atoi(concurrencyStr)
		if err != nil {
			return fmt.Errorf("invalid DOWNLOAD_CONCURRENCY: %w", err)
		}
	}

	client := protocol.NewClient()
	resp, err := client.DownloadBundle(key, domain, id)
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
	semaphore := make(chan struct{}, concurrency)
	return downloadTraversal(client, key, domain, &node, destination, semaphore)
}

func downloadTraversal(client *protocol.Client, key, domain string, node *protocol.Node, destination string, semaphore chan struct{}) error {
	if node.Hash != nil {
		hash := *node.Hash
		if b, err := os.ReadFile(destination); err == nil {
			h2 := protocol.Hash(blake3.Sum256(b))
			if bytes.Equal(h2[:], hash[:]) {
				return nil
			}
		}
		semaphore <- struct{}{}
		defer func() { <-semaphore }()
		log.Printf("🎁 Downloading %s", destination)
		resp, err := client.DownloadParts(key, domain, []protocol.Hash{hash})
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
		log.Printf("🎁 Downloaded %s", destination)
	} else {
		if err := os.MkdirAll(destination, 0755); err != nil {
			return err
		}
		var errors []error
		var mu sync.Mutex
		var wg sync.WaitGroup
		for name, child := range node.Children {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := downloadTraversal(client, key, domain, child, filepath.Join(destination, name), semaphore); err != nil {
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

func printMessages(resp protocol.Response) {
	errs := resp.Errors
	if len(errs) > 0 {
		for _, err := range errs {
			log.Printf("🛑 \033[91m%v\033[0m", err)
		}
	}
	warns := resp.Warnings
	if len(warns) > 0 {
		for _, warn := range warns {
			log.Printf("⚠️ \033[93m%v\033[0m", warn)
		}
	}

	messages := resp.Messages
	if len(messages) > 0 {
		for _, message := range messages {
			log.Println(message)
		}
	}
}

func ingest(directory string) (*ingestion, error) {
	bundle := ingestion{
		contents: make(map[protocol.Hash][]byte),
	}
	err := traverse(directory, &bundle.Node, &bundle.contents)
	return &bundle, err
}

func traverse(directory string, node *protocol.Node, contents *map[protocol.Hash][]byte) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	if node.Children == nil {
		node.Children = make(map[string]*protocol.Node)
	}
	for _, entry := range entries {
		p := filepath.Join(directory, entry.Name())
		if entry.IsDir() {
			if entry.Name() == ".git" {
				log.Printf("😇 Skipping %s", p)
				continue
			}
			child := protocol.Node{}
			err := traverse(p, &child, contents)
			if err != nil {
				return err
			}
			node.Children[entry.Name()] = &child
		} else {
			bytes, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			hash := protocol.Hash(blake3.Sum256(bytes))
			node.Children[entry.Name()] = &protocol.Node{
				Hash: &hash,
			}
			(*contents)[hash] = bytes
		}
	}
	return nil
}
