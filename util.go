package main

import (
	"log"

	"github.com/xmit-co/xmit/protocol"
)

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
