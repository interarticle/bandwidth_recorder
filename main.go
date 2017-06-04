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

	"github.com/interarticle/bandwidth_recorder/persistgauge"
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
	go func() {
		for {
			time.Sleep(time.Second)
			gauge.Set(float64(atomic.LoadUint64(&layer2PlusTotal)))
			datetimeString := time.Now().Format(monthDateFormat)
			l2TotalBytesGauge.Add(datetimeString, float64(atomic.SwapUint64(&layer2PlusDelta, 0)))
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
				atomic.AddUint64(&layer2PlusDelta, packetSize)
			default:
				break // Stop at the first unmatched layer.
			}
		}
	}
}

var (
	wanTotalBytesGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "wan_total_bytes",
			Help: "Total number of bytes sent and received from the Internet as recorded by this recorder job instance",
		}, []string{
			"job_start_time",
		})
	l2TotalBytesGauge *persistgauge.Gauge
)

var persistStorage *persistgauge.Storage

func init() {
	prometheus.MustRegister(wanTotalBytesGauge)
}

func main() {
	flag.Parse()

	http.Handle("/metrics", promhttp.Handler())

	var err error
	persistStorage, err = persistgauge.New(*databasePath)
	persistStorage.StartAutoSave(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	l2TotalBytesGauge, err = persistStorage.NewGauge(prometheus.GaugeOpts{
		Name: "l2_total_bytes",
		Help: "Total number of bytes sent and received from the Internet on Layer 2",
	})
	if err != nil {
		log.Fatal(err)
	}
	prometheus.MustRegister(l2TotalBytesGauge)

	go func() {
		err := networkMonitoringWorker()
		log.Fatal(err)
	}()

	log.Fatal(http.ListenAndServe(*listenSpec, nil))
}
