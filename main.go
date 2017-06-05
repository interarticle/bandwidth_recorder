package main

import (
	"bytes"
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"github.com/google/gopacket/pcap"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/interarticle/bandwidth_recorder/persistmetric"
)

var (
	wanDevice    = flag.String("wan_device", "eth0", "Name of the WAN (Internet) device to monitor.")
	lanDevice    = flag.String("lan_device", "", "Name of the LAN device to monitor; This is only enabled if set.")
	listenSpec   = flag.String("listen_spec", "", "Host and port on which to provide Prometheus monitoring.")
	databasePath = flag.String("database_path", "", "Path to the database used to store persistent metrics.")
)

const (
	monthDateFormat = "2006-01"
)

func getIPAddresses(intf *net.Interface) ([]net.IP, error) {
	addrs, err := intf.Addrs()
	if err != nil {
		return nil, err
	}
	var outAddrs []net.IP
	for _, addr := range addrs {
		if ipAddr, ok := addr.(*net.IPAddr); ok {
			outAddrs = append(outAddrs, ipAddr.IP)
		}
	}
	return outAddrs, nil
}

func wanMonitoringWorker() error {
	startTime := time.Now()
	jobBaseLabel := prometheus.Labels{"job_start_time": startTime.Format(time.RFC3339)}
	gauge := wanTotalBytesGauge.With(jobBaseLabel)

	intf, err := net.InterfaceByName(*wanDevice)
	if err != nil {
		return err
	}
	log.Printf("Starting bandwidth monitoring on wanDevice %v", intf)
	handle, err := pcap.OpenLive(*wanDevice, 500, false, pcap.BlockForever)
	if err != nil {
		return err
	}
	packets := gopacket.NewPacketSource(handle, handle.LinkType())
	var layer2PlusTotal uint64
	var layer2PlusDelta uint64
	var layer3PlusDelta uint64
	var layer4PlusDelta uint64
	var layer4TxDelta uint64
	var layer4RxDelta uint64
	var layer4UnknownDelta uint64
	go func() {
		for {
			time.Sleep(time.Second)
			gauge.Set(float64(atomic.LoadUint64(&layer2PlusTotal)))
			datetimeString := time.Now().Format(monthDateFormat)
			l2TotalBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer2PlusDelta, 0)))
			l3TotalBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer3PlusDelta, 0)))
			l4TotalBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer4PlusDelta, 0)))
			l4TxBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer4TxDelta, 0)))
			l4RxBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer4RxDelta, 0)))
			l4UnknownBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer4UnknownDelta, 0)))
		}
	}()
PacketLoop:
	for {
		packet, err := packets.NextPacket()
		if err != nil {
			return err
		}
		remainingSize := uint64(packet.Metadata().Length)
		var srcMAC, dstMAC *net.HardwareAddr
		for i, layer := range packet.Layers() {
			if _, ok := layer.(gopacket.ErrorLayer); ok {
				break // Stop at error layer.
			}
			switch i {
			case 0:
				remainingSize -= uint64(len(layer.LayerContents()))
				atomic.AddUint64(&layer2PlusTotal, remainingSize)
				atomic.AddUint64(&layer2PlusDelta, remainingSize)

				if eth, ok := layer.(*layers.Ethernet); ok {
					srcMAC = &eth.SrcMAC
					dstMAC = &eth.DstMAC
				}
			case 1:
				remainingSize -= uint64(len(layer.LayerContents()))
				atomic.AddUint64(&layer3PlusDelta, remainingSize)
			case 2:
				remainingSize -= uint64(len(layer.LayerContents()))
				atomic.AddUint64(&layer4PlusDelta, remainingSize)

				switch {
				case srcMAC != nil && bytes.Equal(*srcMAC, intf.HardwareAddr):
					atomic.AddUint64(&layer4TxDelta, remainingSize)
				case dstMAC != nil && bytes.Equal(*dstMAC, intf.HardwareAddr):
					atomic.AddUint64(&layer4RxDelta, remainingSize)
				default:
					atomic.AddUint64(&layer4UnknownDelta, remainingSize)
				}
			default:
				continue PacketLoop // Stop at the first unmatched layer.
			}
		}
	}
}

func mustParseMAC(s string) net.HardwareAddr {
	addr, err := net.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return addr
}

type macMatch struct {
	addr net.HardwareAddr
	mask net.HardwareAddr
}

func (m macMatch) Match(a net.HardwareAddr) bool {
	reference := make([]byte, len(m.addr))
	copy(reference, m.addr)
	target := make([]byte, len(a))
	copy(target, a)
	for i, b := range m.mask {
		if i < len(reference) {
			reference[i] &= b
		}
		if i < len(target) {
			target[i] &= b
		}
	}
	return bytes.Equal(reference, target)
}

var ignoreLANMACRanges = []macMatch{
	// Broadcast.
	macMatch{mustParseMAC("ff:ff:ff:ff:ff:ff"), mustParseMAC("ff:ff:ff:ff:ff:ff")},
	// All multicast.
	macMatch{mustParseMAC("01:00:00:00:00:00"), mustParseMAC("01:00:00:00:00:00")},
}

func lanMonitoringWorker() error {
	intf, err := net.InterfaceByName(*lanDevice)
	if err != nil {
		return err
	}
	log.Printf("Starting bandwidth monitoring on lanDevice %v", intf)
	handle, err := pcap.OpenLive(*lanDevice, 500, false, pcap.BlockForever)
	if err != nil {
		return err
	}
	packets := gopacket.NewPacketSource(handle, handle.LinkType())
	var localAddresses atomic.Value // []net.IP
	{
		localIPs, err := getIPAddresses(intf)
		if err != nil {
			return err
		}
		localAddresses.Store(localIPs)
	}
	go func() {
		for {
			time.Sleep(time.Second)

			localIPs, err := getIPAddresses(intf)
			if err != nil {
				log.Fatalf("Failed to update LAN IP addresses: %v", err)
			}
			localAddresses.Store(localIPs)
		}
	}()
PacketLoop:
	for {
		packet, err := packets.NextPacket()
		if err != nil {
			return err
		}
		remainingSize := uint64(packet.Metadata().Length)
		var srcMAC, dstMAC net.HardwareAddr
		var srcIP, dstIP net.IP
		for i, layer := range packet.Layers() {
			if _, ok := layer.(gopacket.ErrorLayer); ok {
				break // Stop at error layer.
			}
			switch i {
			case 0:
				remainingSize -= uint64(len(layer.LayerContents()))

				if eth, ok := layer.(*layers.Ethernet); ok {
					srcMAC = eth.SrcMAC
					dstMAC = eth.DstMAC
					for _, mMatch := range ignoreLANMACRanges {
						if mMatch.Match(srcMAC) || mMatch.Match(dstMAC) {
							continue PacketLoop // Drop ignored ranges early.
						}
					}
					if !bytes.Equal(srcMAC, intf.HardwareAddr) &&
						!bytes.Equal(dstMAC, intf.HardwareAddr) {
						continue PacketLoop
					}
				} else {
					continue PacketLoop // LAN without MAC should be ignored.
				}
			case 1:
				remainingSize -= uint64(len(layer.LayerContents()))

				if ip, ok := layer.(*layers.IPv4); ok {
					srcIP = ip.SrcIP
					dstIP = ip.DstIP
				} else if ip, ok := layer.(*layers.IPv6); ok {
					srcIP = ip.SrcIP
					dstIP = ip.DstIP
				} else {
					continue PacketLoop // LAN without IP should be ignored.
				}
				for _, ip := range localAddresses.Load().([]net.IP) {
					if ip.Equal(srcIP) || ip.Equal(dstIP) {
						continue PacketLoop // Packets explicitly sent to or from the router should be dropped.
					}
				}
			case 2:
				remainingSize -= uint64(len(layer.LayerContents()))
				datetimeString := time.Now().Format(monthDateFormat)

				switch {
				case bytes.Equal(srcMAC, intf.HardwareAddr):
					lanL4TxBytesCounter.Add(datetimeString, float64(remainingSize))
					lanL4DeviceTxBytesCounter.WithLabelValues(dstMAC.String()).Add(datetimeString, float64(remainingSize))
				case bytes.Equal(dstMAC, intf.HardwareAddr):
					lanL4RxBytesCounter.Add(datetimeString, float64(remainingSize))
					lanL4DeviceRxBytesCounter.WithLabelValues(srcMAC.String()).Add(datetimeString, float64(remainingSize))
				default:
					panic("should not be reached")
				}
				lanL4TotalBytesCounter.Add(datetimeString, float64(remainingSize))
			default:
				continue PacketLoop // Stop at the first unmatched layer.
			}
		}
	}
}

var (
	persistStorage     = persistmetric.MustNew()
	wanTotalBytesGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "wan_total_bytes",
			Help: "Total number of bytes sent and received from the Internet as recorded by this recorder job instance",
		}, []string{
			"job_start_time",
		})
	l2TotalBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "l2_total_bytes",
		Help: "Total number of bytes sent and received from the Internet on Layer 2",
	})
	l3TotalBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "l3_total_bytes",
		Help: "Total number of bytes sent and received from the Internet on Layer 3",
	})
	l4TotalBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "l4_total_bytes",
		Help: "Total number of bytes sent and received from the Internet on Layer 4",
	})
	l4TxBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "l4_tx_bytes",
		Help: "Number of bytes sent to the Internet on Layer 4",
	})
	l4RxBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "l4_rx_bytes",
		Help: "Number of bytes received from the Internet on Layer 4",
	})
	l4UnknownBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "l4_unknown_bytes",
		Help: "Number of bytes transmitted on the Internet interface which cannot be classified under Tx or Rx",
	})
)

var (
	lanL4TotalBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "lan_l4_total_bytes",
		Help: "Number of bytes sent/received on the LAN interface",
	})
	lanL4TxBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "lan_l4_tx_bytes",
		Help: "Number of bytes sent to the LAN interface",
	})
	lanL4RxBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "lan_l4_rx_bytes",
		Help: "Number of bytes received from the LAN interface",
	})
	lanL4DeviceRxBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "lan_l4_device_rx_bytes",
		Help: "Number of bytes received from a specific device on the LAN interface",
	}, persistmetric.VariableLabels([]string{"mac_address"}))
	lanL4DeviceTxBytesCounter = persistStorage.MustNewCounter(prometheus.Opts{
		Name: "lan_l4_device_tx_bytes",
		Help: "Number of bytes sent to a specific device on the LAN interface",
	}, persistmetric.VariableLabels([]string{"mac_address"}))
)

func init() {
	prometheus.MustRegister(wanTotalBytesGauge)
	prometheus.MustRegister(l2TotalBytesCounter)
	prometheus.MustRegister(l3TotalBytesCounter)
	prometheus.MustRegister(l4TotalBytesCounter)
	prometheus.MustRegister(l4RxBytesCounter)
	prometheus.MustRegister(l4TxBytesCounter)
	prometheus.MustRegister(l4UnknownBytesCounter)
}

func initLan() {
	prometheus.MustRegister(lanL4TotalBytesCounter)
	prometheus.MustRegister(lanL4TxBytesCounter)
	prometheus.MustRegister(lanL4RxBytesCounter)
	prometheus.MustRegister(lanL4DeviceRxBytesCounter)
	prometheus.MustRegister(lanL4DeviceTxBytesCounter)
}

func main() {
	flag.Parse()
	if *lanDevice != "" {
		initLan()
	}

	http.Handle("/metrics", promhttp.Handler())

	err := persistStorage.Initialize(context.Background(), *databasePath)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		err := wanMonitoringWorker()
		log.Fatal(err)
	}()

	go func() {
		if *lanDevice != "" {
			err := lanMonitoringWorker()
			log.Fatal(err)
		}
	}()

	log.Fatal(http.ListenAndServe(*listenSpec, nil))
}
