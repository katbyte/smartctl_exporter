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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
)

// nvmeShutdownAndHeatReadings runs the collector over one smartctl answer and returns the unsafe shutdown and time-over-temperature readings, by name.
func nvmeShutdownAndHeatReadings(t *testing.T, answer string) map[string]float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 1000)
	smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(answer), ch, Device{Name: "/dev/nvme0", Type: "nvme", Label: "nvme0"})
	smart.Collect()
	close(ch)

	names := map[*prometheus.Desc]string{metricDeviceUnsafeShutdowns: "unsafe_shutdowns", metricDeviceWarningTemperatureTimeSeconds: "warning_temperature_time_seconds", metricDeviceCriticalTemperatureTimeSeconds: "critical_temperature_time_seconds"}
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

		got[name] = pb.GetCounter().GetValue()
	}

	return got
}

func TestMineNvmeUnsafeShutdownsAndTemperatureTime(t *testing.T) {
	saved := func(file string) string {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}

		return string(data)
	}

	tests := []struct {
		name   string
		answer string
		want   map[string]float64
	}{
		{"all three reported", saved("testdata/nvme-null-HUSMR7632BHP301-nvme0.json"), map[string]float64{"unsafe_shutdowns": 29, "warning_temperature_time_seconds": 0, "critical_temperature_time_seconds": 0}},
		// This drive reports no time over temperature, so those readings are left off rather than shown as 0.
		{"no time over temperature", saved("testdata/nvme-null-INTEL_MEMPEK1W032GA-nvme0.json"), map[string]float64{"unsafe_shutdowns": 173}},
		// No saved drive has run hot, so a made-up one checks that the log's minutes come out as seconds.
		{"made-up drive that has run hot", `{"device": {"type": "nvme", "protocol": "NVMe"}, "nvme_smart_health_information_log": {"unsafe_shutdowns": 3, "warning_temp_time": 15, "critical_comp_time": 2}}`, map[string]float64{"unsafe_shutdowns": 3, "warning_temperature_time_seconds": 900, "critical_temperature_time_seconds": 120}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nvmeShutdownAndHeatReadings(t, tt.answer); !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
