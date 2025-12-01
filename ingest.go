package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/xmit-co/xmit/protocol"
	"github.com/zeebo/blake3"
)

type ingestion struct {
	protocol.Node
	contents map[protocol.Hash][]byte
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
