// Copyright The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"log/slog"
	"maps"
	"os"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
)

// nvmeReadings runs the collector over one saved smartctl answer and returns the NVMe health readings this file checks, by name.
// Temperatures are keyed by their temperature_type, and only the drive's own sensors are kept.
func nvmeReadings(t *testing.T, file string) map[string]float64 {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan prometheus.Metric, 1000)
	smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(string(data)), ch, Device{Name: "/dev/nvme0", Type: "nvme", Label: "nvme0"})
	smart.Collect()
	close(ch)

	names := map[*prometheus.Desc]string{metricDeviceControllerBusyTimeSeconds: "controller_busy_time_seconds", metricDeviceHostReadCommands: "host_read_commands", metricDeviceHostWriteCommands: "host_write_commands", metricDeviceTemperature: "temperature", metricDeviceNamespaceUtilizationBytes: "namespace_utilization_bytes"}
	got := map[string]float64{}
	for m := range ch {
		name, ok := names[m.Desc()]
		if !ok {
			continue
		}

		var pb io_prometheus_client.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}

		for _, label := range pb.GetLabel() {
			if label.GetName() == "temperature_type" || label.GetName() == "namespace_id" {
				name += "/" + label.GetValue()
			}
		}
		if name == "temperature" || (strings.HasPrefix(name, "temperature/") && !strings.HasPrefix(name, "temperature/sensor_")) {
			continue
		}

		got[name] = pb.GetCounter().GetValue() + pb.GetGauge().GetValue()
	}

	return got
}

func TestMineNvmeHealthReadings(t *testing.T) {
	tests := []struct {
		name string
		file string
		want map[string]float64
	}{
		{"four sensors, no namespace list", "testdata/nvme-null-HUSMR7632BHP301-nvme0.json", map[string]float64{"controller_busy_time_seconds": 1184621 * 60, "host_read_commands": 9757701763, "host_write_commands": 40892765082, "temperature/sensor_1": 40, "temperature/sensor_2": 32, "temperature/sensor_3": 36, "temperature/sensor_4": 38}},
		// This drive reports no sensors of its own, so that reading is left off rather than shown as 0 degrees.
		{"no sensors of its own", "testdata/nvme-null-INTEL_MEMPEK1W032GA-nvme0.json", map[string]float64{"controller_busy_time_seconds": 0, "host_read_commands": 26487271, "host_write_commands": 210021083, "namespace_utilization_bytes/1": 29260513280}},
		{"a namespace partly in use", "testdata/SAMSUNG_MZQLB1T9HAJR-00007_19.json", map[string]float64{"controller_busy_time_seconds": 223 * 60, "host_read_commands": 81115611, "host_write_commands": 508465316, "temperature/sensor_1": 40, "temperature/sensor_2": 43, "temperature/sensor_3": 48, "namespace_utilization_bytes/1": 145503502336}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nvmeReadings(t, tt.file); !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
