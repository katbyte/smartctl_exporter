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

func TestMineCapacity(t *testing.T) {
	tests := []struct {
		name string
		file string
		want map[string]float64
	}{
		{"a disk", "testdata/sat-null-WDC__WUS721010ALE6L4-sda.json", map[string]float64{"blocks": 19532873728, "bytes": 10000831348736}},
		{"an NVMe drive with one namespace", "testdata/SAMSUNG_MZQLB1T9HAJR-00007_19.json", map[string]float64{"blocks": 3750748848, "bytes": 1920383410176, "nvme": 1920383410176}},
		// Read as a whole, a drive that supports several namespaces gives no user capacity, so it gets no reading rather than a zero.
		{"an NVMe drive with 32 namespaces, read as a whole", "testdata/nvme-null-SAMSUNG_MZQL21T9HCJR-00A07-nvme0.json", map[string]float64{"nvme": 1920383410176}},
	}

	names := map[*prometheus.Desc]string{metricDeviceCapacityBlocks: "blocks", metricDeviceCapacityBytes: "bytes", metricDeviceTotalCapacityBytes: "nvme"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}

			ch := make(chan prometheus.Metric, 10)
			smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(string(data)), ch, Device{Name: "/dev/sda", Label: "sda"})
			smart.mineCapacity()
			close(ch)

			got := map[string]float64{}
			for m := range ch {
				var pb io_prometheus_client.Metric
				if err := m.Write(&pb); err != nil {
					t.Fatal(err)
				}

				got[names[m.Desc()]] = pb.GetGauge().GetValue()
			}

			if !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
