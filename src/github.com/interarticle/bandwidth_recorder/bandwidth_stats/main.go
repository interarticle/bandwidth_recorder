package main

import (
	"encoding/gob"
	"flag"
	"github.com/interarticle/bandwidth_recorder/data"
	"io"
	"log"
	"os"
)

var gobFile = flag.String("recording", "", "Path to the recorded gob file")

func main() {
	flag.Parse()
	if *gobFile == "" {
		log.Fatal("you must specify --recording")
	}
	srcFile, err := os.Open(*gobFile)
	if err != nil {
		log.Fatal(err)
	}

	var metadata data.PacketMetadata
	reader := gob.NewDecoder(srcFile)
	var totalSize uint64 = 0
	for {
		err := reader.Decode(&metadata)
		if err != nil {
			if err == io.EOF {
				break
			}
			log.Printf("Decoding failed: %v", err)
		}
		totalSize += uint64(metadata.TotalSize - metadata.Layer1Size)
	}
	log.Printf("Total size: %d", totalSize)
}
