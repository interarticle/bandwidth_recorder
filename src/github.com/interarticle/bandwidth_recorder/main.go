package main

import (
	"encoding/gob"
	"flag"
	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"
	"github.com/interarticle/bandwidth_recorder/data"
	"log"
	"os"
	"sync/atomic"
	"time"
)

var device = flag.String("device", "eth0", "Name of the device to monitor.")
var outputFile = flag.String("output_file", "", "gob file to write to.")

func main() {
	flag.Parse()
	if *outputFile == "" {
		log.Fatal("you must specify --output_file")
	}

	outputFile, err := os.OpenFile(*outputFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("Failed to open file: %v", err)
	}
	writer := gob.NewEncoder(outputFile)

	log.Printf("Starting bandwidth monitoring on device %s", *device)
	handle, err := pcap.OpenLive(*device, 500, false, pcap.BlockForever)
	if err != nil {
		log.Fatal(err)
	}
	packets := gopacket.NewPacketSource(handle, handle.LinkType())
	var packetCount uint64 = 0
	var byteCount uint64 = 0
	go func() {
		for {
			time.Sleep(time.Second)
			log.Printf("%d packets/s; %d bytes/s", atomic.SwapUint64(&packetCount, 0), atomic.SwapUint64(&byteCount, 0))
		}
	}()
	for packet := range packets.Packets() {
		atomic.AddUint64(&packetCount, 1)
		atomic.AddUint64(&byteCount, uint64(packet.Metadata().Length))
		metadata := data.PacketMetadata{}
		metadata.CaptureTime = packet.Metadata().Timestamp
		metadata.TotalSize = packet.Metadata().Length
		for i, layer := range packet.Layers() {
			if _, ok := layer.(gopacket.ErrorLayer); ok {
				continue // Ignore error layer.
			}
			switch i {
			case 0:
				metadata.Layer1Type = layer.LayerType()
				metadata.Layer1Size = len(layer.LayerContents())
				if eth, ok := layer.(*layers.Ethernet); ok {
					metadata.SrcMAC = eth.SrcMAC
					metadata.DstMAC = eth.DstMAC
				}
			case 1:
				metadata.Layer2Type = layer.LayerType()
				metadata.Layer2Size = len(layer.LayerContents())
				if ip, ok := layer.(*layers.IPv4); ok {
					metadata.SrcIP = ip.SrcIP
					metadata.DstIP = ip.DstIP
				} else if ip, ok := layer.(*layers.IPv6); ok {
					metadata.SrcIP = ip.SrcIP
					metadata.DstIP = ip.DstIP
				}
			case 2:
				metadata.Layer3Type = layer.LayerType()
				metadata.Layer3Size = len(layer.LayerContents())
				if transport, ok := layer.(*layers.TCP); ok {
					metadata.SrcPort = uint16(transport.SrcPort)
					metadata.DstPort = uint16(transport.DstPort)
				} else if transport, ok := layer.(*layers.UDP); ok {
					metadata.SrcPort = uint16(transport.SrcPort)
					metadata.DstPort = uint16(transport.DstPort)
				} else if transport, ok := layer.(*layers.SCTP); ok {
					metadata.SrcPort = uint16(transport.SrcPort)
					metadata.DstPort = uint16(transport.DstPort)
				}
			default:
				break // Go up to three layers.
			}
		}
		writer.Encode(metadata)
	}
}
