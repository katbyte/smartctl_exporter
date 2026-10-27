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

// scsiMoreReadings runs the collector over one smartctl answer, saved or given inline, and returns the readings this file checks, keyed by name
// and labels. Of the temperature reading only the types this change adds are kept.
func scsiMoreReadings(t *testing.T, answer string, device Device) map[string]float64 {
	t.Helper()
	if strings.HasPrefix(answer, "testdata/") {
		data, err := os.ReadFile(answer)
		if err != nil {
			t.Fatal(err)
		}
		answer = string(data)
	}

	ch := make(chan prometheus.Metric, 1000)
	smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(answer), ch, device)
	smart.Collect()
	close(ch)

	names := map[*prometheus.Desc]string{
		metricSCSIReadCommands: "read_commands", metricSCSIWriteCommands: "write_commands", metricDeviceTemperature: "temperature",
		metricSCSIRatedStartStopCycles: "rated_start_stop_cycles", metricSCSIRatedLoadUnloadCycles: "rated_load_unload_cycles",
		metricReadTotalErrorsCorrected: "read_total_errors_corrected", metricReadCorrectionAlgorithmInvocations: "read_correction_algorithm_invocations",
		metricWriteTotalErrorsCorrected: "write_total_errors_corrected", metricWriteCorrectionAlgorithmInvocations: "write_correction_algorithm_invocations",
		metricVerifyTotalErrorsCorrected: "verify_total_errors_corrected", metricVerifyCorrectionAlgorithmInvocations: "verify_correction_algorithm_invocations",
		metricSCSIBackgroundScans: "background_scans", metricSCSIBackgroundMediumScans: "background_medium_scans", metricSCSIBackgroundScanProgressRatio: "background_scan_progress_ratio", metricSCSIBackgroundScanStatus: "background_scan_status",
		metricSCSIExtendedSelfTestSeconds: "extended_self_test_seconds",
	}
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

		var labels []string
		for _, label := range pb.GetLabel() {
			if label.GetName() == "temperature_type" && !strings.Contains(label.GetValue(), "lifetime") && !strings.Contains(label.GetValue(), "since_power_on") && !strings.HasPrefix(label.GetValue(), "sensor_") {
				name = "" // the temperature types that were already there
			}
			if label.GetName() != "device" {
				labels = append(labels, label.GetName()+"="+label.GetValue())
			}
		}
		if name != "" {
			got[name+"{"+strings.Join(labels, ",")+"}"] = pb.GetCounter().GetValue() + pb.GetGauge().GetValue()
		}
	}

	return got
}

func TestMineSCSIMoreReadings(t *testing.T) {
	// Two sensors, one of them with a low the disk never recorded, which smartctl writes as a word. The humidity sensor is not a temperature.
	twoSensors := `{"device": {"name": "/dev/sdz", "type": "scsi", "protocol": "SCSI"}, "scsi_environmental_reports": {
		"temperature_1": {"current": 30, "lifetime_maximum": 50, "lifetime_minimum": "unknown", "maximum_since_power_on": 40, "minimum_since_power_on": 25},
		"temperature_2": {"current": 35, "lifetime_maximum": 55, "lifetime_minimum": 20, "maximum_since_power_on": 45, "minimum_since_power_on": 30},
		"relative_humidity_1": {"current": 40, "lifetime_maximum": 60}}}`

	tests := []struct {
		name   string
		answer string
		device Device
		want   map[string]float64
	}{
		// A WD Ultrastar SAS disk saved with -x, so every log is there.
		{"SAS disk with every log", "testdata/scsi-WDC_WUH722020BL5204-sdb.json", Device{Name: "/dev/sdb", Type: "scsi", Label: "sdb"}, map[string]float64{
			"read_commands{}":  4190301740,
			"write_commands{}": 1440645205,
			"temperature{temperature_type=lifetime_max}":                              59,
			"temperature{temperature_type=lifetime_min}":                              23,
			"temperature{temperature_type=since_power_on_max}":                        49,
			"temperature{temperature_type=since_power_on_min}":                        27,
			"rated_start_stop_cycles{}":                                               50000,
			"rated_load_unload_cycles{}":                                              600000,
			"read_total_errors_corrected{}":                                           12,
			"read_correction_algorithm_invocations{}":                                 407707,
			"write_total_errors_corrected{}":                                          1,
			"write_correction_algorithm_invocations{}":                                52689,
			"verify_total_errors_corrected{}":                                         0,
			"verify_correction_algorithm_invocations{}":                               2042,
			"background_scans{}":                                                      39,
			"background_medium_scans{}":                                               39,
			"background_scan_progress_ratio{}":                                        0,
			"background_scan_status{status=waiting until BMS interval timer expires}": 8,
			"extended_self_test_seconds{}":                                            132060,
		}},
		{"two temperature sensors", twoSensors, Device{Name: "/dev/sdz", Type: "scsi", Label: "sdz"}, map[string]float64{
			"temperature{temperature_type=lifetime_max}":                50,
			"temperature{temperature_type=since_power_on_max}":          40,
			"temperature{temperature_type=since_power_on_min}":          25,
			"temperature{temperature_type=sensor_2}":                    35,
			"temperature{temperature_type=sensor_2_lifetime_max}":       55,
			"temperature{temperature_type=sensor_2_lifetime_min}":       20,
			"temperature{temperature_type=sensor_2_since_power_on_max}": 45,
			"temperature{temperature_type=sensor_2_since_power_on_min}": 30,
		}},
		{"NVMe drive has none", "testdata/nvme-null-INTEL_MEMPEK1W032GA-nvme0.json", Device{Name: "/dev/nvme0", Type: "nvme", Label: "nvme0"}, map[string]float64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scsiMoreReadings(t, tt.answer, tt.device); !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
