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

func TestMineBlockSize(t *testing.T) {
	tests := []struct {
		name string
		file string
		want map[string]float64
	}{
		{"both sizes", "testdata/sat-null-WDC__WUS721010ALE6L4-sda.json", map[string]float64{"logical": 512, "physical": 4096}},
		// NVMe drives have no physical block size, and some give no block size at all, so those are left off rather than shown as 0.
		{"no physical size", "testdata/SAMSUNG_MZQLB1T9HAJR-00007_19.json", map[string]float64{"logical": 512}},
		{"no sizes", "testdata/nvme-null-HUSMR7632BHP301-nvme0.json", map[string]float64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.file)
			if err != nil {
				t.Fatal(err)
			}

			ch := make(chan prometheus.Metric, 10)
			smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(string(data)), ch, Device{Name: "/dev/sda", Label: "sda"})
			smart.mineBlockSize()
			close(ch)

			got := map[string]float64{}
			for m := range ch {
				var pb io_prometheus_client.Metric
				if err := m.Write(&pb); err != nil {
					t.Fatal(err)
				}

				for _, label := range pb.GetLabel() {
					if label.GetName() == "blocks_type" {
						got[label.GetValue()] = pb.GetGauge().GetValue()
					}
				}
			}

			if !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
