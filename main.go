package main

import (
	"fmt"
	"github.com/fatih/color"
	"github.com/fxamacker/cbor/v2"
	"github.com/kirsle/configdir"
	"github.com/xmit-co/xmit/protocol"
	"github.com/zeebo/blake3"
	"golang.org/x/term"
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
)

type bundle struct {
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
	fmt.Println("Usage:\nxmit set-key\nxmit [--stage] domain [directory]")
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
		fmt.Println("🗝️ Enter your key (no echo):")
		key, err := term.ReadPassword(syscall.Stdin)
		if err != nil {
			log.Fatalf("Failed to read key (%s)", err)
		}
		if err := storeKey(string(key)); err != nil {
			log.Fatalf("Failed to store key (%s)", err)
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

	key := findKey()
	if key == "" {
		log.Fatalf("No key found. Set XMIT_KEY or run 'xmit set-key' to set one.")
	}

	log.Println("📦 Packaging…")
	b, err := ingest(directory)
	if err != nil {
		log.Fatalf("Failed to ingest (%s)", err)
	}
	bb, err := cbor.Marshal(b.Node)
	if err != nil {
		log.Fatalf("Failed to marshal (%s)", err)
	}

	log.Println("🚶 Uploading bundle…")
	bundleResp, err := protocol.UploadBundle(key, domain, bb)
	if err != nil {
		log.Fatalf("Failed to upload (%s)", err)
	}

	printMessages(bundleResp.Response)
	if !bundleResp.Response.Success {
		log.Fatalf("Bundle upload failed")
	}

	toUpload := make(map[protocol.Hash][]byte)
	for _, h := range bundleResp.Missing {
		toUpload[h] = b.contents[h]
	}

	log.Printf("🏃 Uploading %d missing parts…", len(toUpload))
	missingResp, err := protocol.UploadMissing(key, domain, toUpload)
	if err != nil {
		log.Fatalf("Failed to upload (%s)", err)
	}

	printMessages(missingResp.Response)
	if !missingResp.Response.Success {
		log.Fatalf("Missing parts upload failed")
	}

	log.Printf("🏁 Finalizing…")
	finalizeResp, err := protocol.Finalize(key, domain, bundleResp.ID)

	printMessages(finalizeResp.Response)
	if !finalizeResp.Response.Success {
		log.Fatalf("Finalization failed")
	}

	log.Printf("Visible at URL: %s", finalizeResp.URL)
}

func printMessages(resp protocol.Response) {
	errs := resp.Errors
	if len(errs) > 0 {
		for _, err := range errs {
			color.Red("Received error: %v", err)
		}
	}
	warns := resp.Warnings
	if len(warns) > 0 {
		for _, warn := range warns {
			color.Yellow("Received warning: %v", warn)
		}
	}
}

func ingest(directory string) (*bundle, error) {
	bundle := bundle{
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
			hash := blake3.Sum256(bytes)
			node.Children[entry.Name()] = &protocol.Node{
				Hash: hash,
			}
			(*contents)[hash] = bytes
		}
	}
	return nil
}
