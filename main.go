package main

import (
	"flag"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var wanDevice = flag.String("wan_device", "eth0", "Name of the WAN (Internet) device to monitor.")
var listenSpec = flag.String("listen_spec", "", "Host and port on which to provide Prometheus monitoring.")

var globalWanTotal uint64

func networkMonitoringWorker() error {
	startTime := time.Now()
	jobBaseLabel := prometheus.Labels{"job_start_time": startTime.Format(time.RFC3339)}
	gauge := wanTotalBytesGauge.With(jobBaseLabel)

	log.Printf("Starting bandwidth monitoring on wanDevice %s", *wanDevice)
	handle, err := pcap.OpenLive(*wanDevice, 500, false, pcap.BlockForever)
	if err != nil {
		return err
	}
	packets := gopacket.NewPacketSource(handle, handle.LinkType())
	var layer2PlusTotal uint64
	go func() {
		for {
			time.Sleep(time.Second)
			gauge.Set(float64(atomic.LoadUint64(&layer2PlusTotal)))
		}
	}()
	for {
		packet, err := packets.NextPacket()
		if err != nil {
			return err
		}
		for i, layer := range packet.Layers() {
			if _, ok := layer.(gopacket.ErrorLayer); ok {
				continue // Ignore error layer.
			}
			switch i {
			case 0:
				packetSize := uint64(packet.Metadata().Length - len(layer.LayerContents()))
				atomic.AddUint64(&layer2PlusTotal, packetSize)
				atomic.AddUint64(&globalWanTotal, packetSize)
			default:
				break // Stop at the first unmatched layer.
			}
		}
	}
}

func globalWanTotalGaugeFunc() float64 {
	delta := atomic.SwapUint64(&globalWanTotal, 0)
	return float64(delta)
}

var (
	wanTotalBytesGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "wan_total_bytes",
			Help: "Total number of bytes sent and received from the Internet as recorded by this recorder job instance",
		}, []string{
			"job_start_time",
		})
	wanDeltaBytesGauge = prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "wan_delta_bytes",
			Help: "Number of bytes sent and received from the Internet as recorded by this recorder job instance, since last collection",
		}, globalWanTotalGaugeFunc)
)

func init() {
	prometheus.MustRegister(wanTotalBytesGauge)
	prometheus.MustRegister(wanDeltaBytesGauge)
}

func main() {
	flag.Parse()

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		err := networkMonitoringWorker()
		log.Fatal(err)
	}()

	log.Fatal(http.ListenAndServe(*listenSpec, nil))
}
