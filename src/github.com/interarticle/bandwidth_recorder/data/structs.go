package data

import (
	"github.com/google/gopacket"
	"net"
	"time"
)

type PacketMetadata struct {
	CaptureTime time.Time
	TotalSize   int

	Layer1Type gopacket.LayerType
	Layer2Type gopacket.LayerType
	Layer3Type gopacket.LayerType
	Layer1Size int
	Layer2Size int
	Layer3Size int

	SrcMAC  net.HardwareAddr
	DstMAC  net.HardwareAddr
	SrcIP   net.IP
	DstIP   net.IP
	SrcPort uint16
	DstPort uint16
}
