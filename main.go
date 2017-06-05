package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/pcap"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/interarticle/bandwidth_recorder/persistmetric"
)

var (
	wanDevice    = flag.String("wan_device", "eth0", "Name of the WAN (Internet) device to monitor.")
	listenSpec   = flag.String("listen_spec", "", "Host and port on which to provide Prometheus monitoring.")
	databasePath = flag.String("database_path", "", "Path to the database used to store persistent metrics.")
)

const (
	monthDateFormat = "2006-01"
)

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
	var layer2PlusDelta uint64
	var layer3PlusDelta uint64
	var layer4PlusDelta uint64
	go func() {
		for {
			time.Sleep(time.Second)
			gauge.Set(float64(atomic.LoadUint64(&layer2PlusTotal)))
			datetimeString := time.Now().Format(monthDateFormat)
			l2TotalBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer2PlusDelta, 0)))
			l3TotalBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer3PlusDelta, 0)))
			l4TotalBytesCounter.Add(datetimeString, float64(atomic.SwapUint64(&layer4PlusDelta, 0)))
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
			remainingSize := uint64(packet.Metadata().Length)
			switch i {
			case 0:
				remainingSize -= uint64(len(layer.LayerContents()))
				atomic.AddUint64(&layer2PlusTotal, remainingSize)
				atomic.AddUint64(&layer2PlusDelta, remainingSize)
			case 1:
				remainingSize -= uint64(len(layer.LayerContents()))
				atomic.AddUint64(&layer3PlusDelta, remainingSize)
			case 2:
				remainingSize -= uint64(len(layer.LayerContents()))
				atomic.AddUint64(&layer4PlusDelta, remainingSize)
			default:
				break // Stop at the first unmatched layer.
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
)

func init() {
	prometheus.MustRegister(wanTotalBytesGauge)
	prometheus.MustRegister(l2TotalBytesCounter)
	prometheus.MustRegister(l3TotalBytesCounter)
	prometheus.MustRegister(l4TotalBytesCounter)
}

func main() {
	flag.Parse()

	http.Handle("/metrics", promhttp.Handler())

	err := persistStorage.Initialize(context.Background(), *databasePath)
	if err != nil {
		log.Fatal(err)
	}

	go func() {
		err := networkMonitoringWorker()
		log.Fatal(err)
	}()

	log.Fatal(http.ListenAndServe(*listenSpec, nil))
}
