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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tidwall/gjson"
)

func TestSCSIMetricDescriptorsRegistered(t *testing.T) {
	device := Device{Name: "/dev/test-scsi", Type: "scsi", Label: "test-scsi"}
	// Keep the cache entry fresh even before command-line defaults are parsed,
	// so the manager does not invoke smartctl.
	jsonCache.Store(device, JSONCache{
		JSON: gjson.Parse(`{
			"json_format_version": [1, 0],
			"smartctl": {"version": [7, 4], "exit_status": 0},
			"device": {"type": "scsi", "protocol": "SCSI"},
			"scsi_percentage_used_endurance_indicator": 7,
			"scsi_error_counter_log": {
				"verify": {
					"errors_corrected_by_rereads_rewrites": 1,
					"errors_corrected_by_eccfast": 2,
					"errors_corrected_by_eccdelayed": 3,
					"total_uncorrected_errors": 4
				}
			}
		}`),
		LastCollect: time.Now().Add(time.Hour),
	})
	t.Cleanup(func() { jsonCache.Delete(device) })

	registry := prometheus.NewPedanticRegistry()
	collector := &SMARTctlManagerCollector{
		Devices: []Device{device},
		logger:  slog.New(slog.DiscardHandler),
	}
	if err := registry.Register(collector); err != nil {
		t.Fatalf("register collector: %v", err)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather SCSI metrics: %v", err)
	}

	want := map[string]float64{
		"smartctl_scsi_percentage_used_endurance_indicator":    7,
		"smartctl_verify_errors_corrected_by_rereads_rewrites": 1,
		"smartctl_verify_errors_corrected_by_eccfast":          2,
		"smartctl_verify_errors_corrected_by_eccdelayed":       3,
		"smartctl_verify_total_uncorrected_errors":             4,
	}
	for _, family := range families {
		value, ok := want[family.GetName()]
		if !ok {
			continue
		}
		metrics := family.GetMetric()
		if len(metrics) != 1 {
			t.Errorf("%s: got %d metrics, want 1", family.GetName(), len(metrics))
		} else if got := metrics[0].GetGauge().GetValue(); got != value {
			t.Errorf("%s: got %v, want %v", family.GetName(), got, value)
		}
		delete(want, family.GetName())
	}
	for name := range want {
		t.Errorf("missing SCSI metric %s", name)
	}
}

// TestEverySavedDiskPassesStrictCheck runs each saved smartctl answer through the strict check the exporter serves its page with.
// A reading left out of Describe fails that check, and with it the whole page, but only on a disk that reports the reading.
func TestEverySavedDiskPassesStrictCheck(t *testing.T) {
	files, err := filepath.Glob("testdata/*.json")
	if err != nil {
		t.Fatal(err)
	}

	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}

			device := Device{Name: "/dev/saved", Type: "auto", Label: "saved"}
			jsonCache.Store(device, JSONCache{JSON: parseJSON(string(data)), LastCollect: time.Now().Add(time.Hour)})
			t.Cleanup(func() { jsonCache.Delete(device) })

			registry := prometheus.NewPedanticRegistry()
			if err := registry.Register(&SMARTctlManagerCollector{Devices: []Device{device}, logger: slog.New(slog.DiscardHandler)}); err != nil {
				t.Fatal(err)
			}
			if _, err := registry.Gather(); err != nil {
				t.Error(err)
			}
		})
	}
}
