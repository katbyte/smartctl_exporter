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

// sataReadings runs the collector over one saved smartctl answer and returns the readings this file checks, keyed by name and labels.
// Of the device statistics only three are kept, enough to show the table is read and a statistic with no value is left off.
func sataReadings(t *testing.T, file string, device Device) map[string]float64 {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan prometheus.Metric, 2000)
	smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(string(data)), ch, device)
	smart.Collect()
	close(ch)

	names := map[*prometheus.Desc]string{metricDeviceTemperature: "temperature", metricDeviceSATAPhyEvents: "sata_phy_events", metricDeviceTemperatureOverLimitCount: "over_limit", metricDeviceTemperatureUnderLimitCount: "under_limit", metricDeviceStatistics: "statistics"}
	kept := map[string]bool{"Head Flying Hours": true, "Number of Interface CRC Errors": true, "Date and Time TimeStamp": true}
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
			switch label.GetName() {
			case "device", "statistic_flags_short", "statistic_flags_long":
				continue
			case "statistic_name":
				if !kept[label.GetValue()] {
					name = ""
				}
			}
			labels = append(labels, label.GetValue())
		}
		if name != "" {
			got[name+"{"+strings.Join(labels, "/")+"}"] = pb.GetCounter().GetValue() + pb.GetGauge().GetValue()
		}
	}

	return got
}

func TestMineSATAReadings(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		device Device
		want   map[string]float64
	}{
		// A WD Red Pro behind a SAS card, saved with -x: temperatures from the SCT status and the device statistics, twelve link event counters.
		{"WD disk", "testdata/sat-WDC_WD200EDGZ-11B9PA0-sdc.json", Device{Name: "/dev/sdc", Type: "sat", Label: "sdc"}, map[string]float64{
			"temperature{current}":                                                  41,
			"temperature{lifetime_max}":                                             45,
			"temperature{lifetime_min}":                                             22,
			"temperature{op_limit_max}":                                             65,
			"temperature{op_limit_min}":                                             0,
			"temperature{power_cycle_max}":                                          45,
			"temperature{power_cycle_min}":                                          33,
			"sata_phy_events{Command failed due to ICRC error}":                     0,
			"sata_phy_events{R_ERR response for data FIS}":                          0,
			"sata_phy_events{R_ERR response for device-to-host data FIS}":           0,
			"sata_phy_events{R_ERR response for host-to-device data FIS}":           0,
			"sata_phy_events{R_ERR response for non-data FIS}":                      0,
			"sata_phy_events{R_ERR response for device-to-host non-data FIS}":       0,
			"sata_phy_events{R_ERR response for host-to-device non-data FIS}":       0,
			"sata_phy_events{Device-to-host non-data FIS retries}":                  0,
			"sata_phy_events{Transition from drive PhyRdy to drive PhyNRdy}":        0,
			"sata_phy_events{Device-to-host register FISes sent due to a COMRESET}": 1,
			"sata_phy_events{CRC errors within host-to-device FIS}":                 0,
			"sata_phy_events{Non-CRC errors within host-to-device FIS}":             0,
			"over_limit{}":  0,
			"under_limit{}": 0,
			"statistics{Date and Time TimeStamp/General Statistics}":          28043125450,
			"statistics{Head Flying Hours/Rotating Media Statistics}":         7740,
			"statistics{Number of Interface CRC Errors/Transport Statistics}": 0,
		}},
		// A Seagate behind the same card: eight link event counters, 654 times over its temperature limit, and no timestamp statistic (smartctl gives it no value).
		{"Seagate disk", "testdata/sat-ST20000DM001-3Y3103-sdq.json", Device{Name: "/dev/sdq", Type: "sat", Label: "sdq"}, map[string]float64{
			"temperature{current}":         44,
			"temperature{lifetime_max}":    48,
			"temperature{lifetime_min}":    28,
			"temperature{op_limit_max}":    60,
			"temperature{op_limit_min}":    10,
			"temperature{power_cycle_max}": 48,
			"temperature{power_cycle_min}": 35,
			"sata_phy_events{Device-to-host register FISes sent due to a COMRESET}": 1,
			"sata_phy_events{Command failed due to ICRC error}":                     0,
			"sata_phy_events{R_ERR response for device-to-host data FIS}":           0,
			"sata_phy_events{R_ERR response for host-to-device data FIS}":           0,
			"sata_phy_events{R_ERR response for device-to-host non-data FIS}":       0,
			"sata_phy_events{R_ERR response for host-to-device non-data FIS}":       0,
			"sata_phy_events{CRC errors within host-to-device FIS}":                 0,
			"sata_phy_events{Non-CRC errors within host-to-device FIS}":             0,
			"over_limit{}":  654,
			"under_limit{}": 0,
			"statistics{Head Flying Hours/Rotating Media Statistics}":         4536,
			"statistics{Number of Interface CRC Errors/Transport Statistics}": 0,
		}},
		{"NVMe drive has none", "testdata/nvme-null-INTEL_MEMPEK1W032GA-nvme0.json", Device{Name: "/dev/nvme0", Type: "nvme", Label: "nvme0"}, map[string]float64{
			"temperature{current}": 31,
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sataReadings(t, tt.file, tt.device); !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
