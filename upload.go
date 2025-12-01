package main

import (
	"cmp"
	"log"
	"os"
	"slices"
	"strconv"

	"github.com/xmit-co/xmit/protocol"
	"github.com/zeebo/blake3"
)

func upload(key, domain, directory string) {
	// Discover upload URL
	log.Print("🔍 Discovering upload endpoint…")
	discovery, err := protocol.Discover()
	if err != nil {
		log.Fatalf("🛑 Failed to discover upload endpoint: %v", err)
	}
	log.Printf("🌐 Using upload URL: %s", discovery.URL)

	if key == "" {
		log.Fatalf("🛑 No key found. Set XMIT_KEY or run 'xmit set-key'.\n   API keys can be managed at: %s", discovery.APIKeyManagementURL)
	}

	// Create parallel uploader
	uploadParallelism := 3
	if s := os.Getenv("UPLOAD_PARALLELISM"); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			uploadParallelism = v
		}
	}
	uploader, err := protocol.NewParallelUploader(discovery.URL, uploadParallelism)
	if err != nil {
		log.Fatalf("🛑 Failed to create parallel uploader: %v", err)
	}

	log.Printf("📦 Bundling %s…", directory)
	b, err := ingest(directory)
	if err != nil {
		log.Fatalf("🛑 Failed to ingest: %v", err)
	}
	bb, err := uploader.EncMode().Marshal(b.Node)
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

	suggestResp, err := uploader.SuggestBundle(key, domain, bbh)
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
		bundleResp, err := uploader.UploadBundle(key, domain, bb)
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

		// Upload chunks in parallel
		results := uploader.UploadChunksParallel(key, domain, chunks)

		// Check results
		for _, result := range results {
			if result.Err != nil {
				log.Fatalf("🛑 Failed to upload chunk %d: %v", result.Index+1, result.Err)
			}
			printMessages(result.Response.Response)
			if !result.Response.Response.Success {
				log.Fatalf("🛑 Missing parts upload failed for chunk %d", result.Index+1)
			}
		}
	}

	finalizeResp, err := uploader.Finalize(key, domain, bbh)
	if err != nil {
		log.Fatalf("🛑 Failed to finalize: %v", err)
	}

	printMessages(finalizeResp.Response)
	if !finalizeResp.Response.Success {
		log.Fatalf("🛑 Finalization failed")
	}
}
