package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"errors"
	"flag"
	"github.com/interarticle/bandwidth_recorder/data"
	"io"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
)

var gobFile = flag.String("recording", "", "Path to the recorded gob file")

func ReadGobUint32(reader io.Reader) (uint32, error) {
	var buffer [4]byte
	_, err := reader.Read(buffer[3:])
	if err != nil {
		return 0, err
	}
	if buffer[3]&0x80 != 0 {
		byteCount := ^buffer[3] + 1
		if byteCount > 4 {
			return 0, errors.New("excessive byte count")
		}
		_, err = io.ReadFull(reader, buffer[4-byteCount:])
		if err != nil {
			return 0, err
		}
	}
	return binary.BigEndian.Uint32(buffer[:]), nil
}

type ByteChannelReader struct {
	Channel chan []byte
	reader  *bytes.Reader
}

func (r *ByteChannelReader) Read(b []byte) (int, error) {
	if r.reader == nil || r.reader.Len() == 0 {
		var buffer []byte
		var ok bool
		if buffer, ok = <-r.Channel; !ok {
			return 0, io.EOF
		}
		r.reader = bytes.NewReader(buffer)
	}
	return r.reader.Read(b)
}

type GobMapper struct {
	reader          io.Reader
	privateChannels []chan []byte
	sharedChannel   chan []byte
}

func NewGobMapper(reader io.Reader) *GobMapper {
	return &GobMapper{
		reader:        reader,
		sharedChannel: make(chan []byte, runtime.NumCPU()*3),
	}
}

func (g *GobMapper) RegisterReader() io.Reader {
	ch := make(chan []byte)
	g.privateChannels = append(g.privateChannels, ch)
	go func() {
		defer close(ch)
		for b := range g.sharedChannel {
			ch <- b
		}
	}()
	return &ByteChannelReader{
		Channel: ch,
	}
}

func (g *GobMapper) Start() {
	go func() {
		defer close(g.sharedChannel)
		for {
			buffer := new(bytes.Buffer)
			reader := io.TeeReader(g.reader, buffer)
			length, err := ReadGobUint32(reader)
			if err != nil {
				if err == io.EOF {
					return
				}
				log.Printf("Failed to read length: %v", err)
				return
			}
			headerByteCount := buffer.Len()
			typeId, err := ReadGobUint32(reader)
			if err != nil {
				log.Printf("Failed to read type id: %v", err)
				return
			}
			remainingToRead := length - uint32(buffer.Len()-headerByteCount)
			if remainingToRead < 0 {
				log.Printf("Invalid gob item size")
				return
			}
			n, err := buffer.ReadFrom(io.LimitReader(g.reader, int64(remainingToRead)))
			if err != nil {
				log.Printf("Failed to read item: %v", err)
				return
			}
			if n < int64(remainingToRead) {
				log.Printf("short read item body")
				return
			}

			if typeId&1 != 0 {
				for _, ch := range g.privateChannels {
					ch <- buffer.Bytes()
				}
			} else {
				g.sharedChannel <- buffer.Bytes()
			}
		}
	}()
}

func main() {
	flag.Parse()
	if *gobFile == "" {
		log.Fatal("you must specify --recording")
	}
	var err error
	var srcFile io.Reader
	if *gobFile == "-" {
		srcFile = os.Stdin
	} else {
		srcFile, err = os.Open(*gobFile)
		if err != nil {
			log.Fatal(err)
		}
	}
	mapper := NewGobMapper(bufio.NewReader(srcFile))

	var totalSize uint64 = 0
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU()-1 || i < 1; i++ {
		wg.Add(1)
		go func(mapperReader io.Reader) {
			defer wg.Done()
			var metadata data.PacketMetadata
			reader := gob.NewDecoder(mapperReader)
			for {
				err := reader.Decode(&metadata)
				if err != nil {
					if err == io.EOF {
						break
					}
					log.Printf("Decoding failed: %v", err)
					break
				}
				atomic.AddUint64(&totalSize, uint64(metadata.TotalSize-metadata.Layer1Size))
			}
		}(mapper.RegisterReader())
	}
	log.Printf("Starting")
	mapper.Start()
	wg.Wait()
	log.Printf("Done")

	log.Printf("Total size: %d", totalSize)
}
