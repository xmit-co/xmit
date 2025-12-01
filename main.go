package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/xmit-co/xmit/preview"
	"golang.org/x/term"
)

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

func usage() {
	fmt.Println("Usage:")
	fmt.Println("  xmit set-key [KEY] (or set XMIT_KEY) → configure your API key")
	fmt.Println("  xmit DOMAIN [DIRECTORY] → upload to DOMAIN")
	fmt.Println("  xmit preview [DIRECTORY] → serve a preview locally (set LISTEN to override :4000)")
	fmt.Println("  xmit download DOMAIN[@ID] DIRECTORY → download from DOMAIN to DIRECTORY (specify an upload ID or omit ID for latest)")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	command := os.Args[1]

	if command == "-h" || command == "--help" {
		usage()
		os.Exit(0)
	}

	if command == "set-key" {
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

	if command == "preview" {
		if err := preview.Serve(findDirectory()); err != nil {
			log.Fatalf("🛑 Failed to preview: %v", err)
		}
		return
	}

	if command == "download" {
		key := findKey()
		if key == "" {
			log.Fatalf("🛑 No key found. Set XMIT_KEY or run 'xmit set-key'.")
		}
		if len(os.Args) < 4 {
			log.Fatalf("🛑 Missing domain[@id] destination arguments")
		}
		domainAndID := os.Args[2]
		parts := strings.SplitN(domainAndID, "@", 2)
		domain := parts[0]
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

	// Default: upload to domain
	domain := command
	key := findKey()
	directory := findDirectory()
	upload(key, domain, directory)
}
