package main

import (
	"cmp"
	"fmt"
	"github.com/kirsle/configdir"
	"github.com/xmit-co/xmit/protocol"
	"github.com/zeebo/blake3"
	"golang.org/x/term"
	"log"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
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
	if bytes, err := os.ReadFile(keyPath); err == nil {
		return strings.TrimSpace(string(bytes))
	}
	return ""
}

func storeKey(key string) error {
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(keyPath, []byte(key), 0600)
}

func usage() {
	fmt.Println("Usage:\nxmit set-key [key] (or set XMIT_KEY)\nxmit domain [directory]")
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

	client := protocol.NewClient()

	key := findKey()
	if key == "" {
		log.Fatalf("🛑 No key found. Set XMIT_KEY or run 'xmit set-key'.")
	}

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
			log.Printf("📤 Uploading chunk %d/%d…", i+1, len(chunks))
			missingResp, err := client.UploadMissing(key, domain, chunk)
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
