package main

import (
	"encoding/json"
	"flag"
	"log"

	"github.com/interarticle/bandwidth_recorder/persistgauge"
)

var (
	dbPath          = flag.String("db_path", "", "Path to database file")
	setMetric       = flag.String("set_metric", "", "Name of the metric to write")
	newMetricValues = flag.String("new_metric_values", "", "JSON value of the new metric")
)

func main() {
	flag.Parse()

	storage, err := persistgauge.New(*dbPath)

	if err != nil {
		log.Fatal(err)
	}

	metrics, err := storage.ListMetrics()
	if err != nil {
		log.Fatal(err)
	}

	for _, metric := range metrics {
		log.Printf("Metric %s:", metric)
		value, err := storage.ReadMetric(metric)
		if err != nil {
			log.Fatal(err)
		}

		json, err := json.Marshal(&value)
		if err != nil {
			log.Fatal(err)
		}
		log.Print(string(json))
	}

	if *setMetric != "" {
		if *newMetricValues == "" {
			log.Fatal("Both --set_metric and --new_metric_values must be set")
		}

		var newValues persistgauge.MetricValues
		err := json.Unmarshal([]byte(*newMetricValues), &newValues)
		if err != nil {
			log.Fatal(err)
		}
		storage.WriteMetric(*setMetric, newValues)
	}
}
