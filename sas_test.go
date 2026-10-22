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

// sasReadings runs the collector over one saved smartctl answer and returns the SAS readings this file checks, keyed by name and labels.
func sasReadings(t *testing.T, file string, device Device) map[string]float64 {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	ch := make(chan prometheus.Metric, 1000)
	smart := NewSMARTctl(slog.New(slog.DiscardHandler), parseJSON(string(data)), ch, device)
	smart.Collect()
	close(ch)

	names := map[*prometheus.Desc]string{metricSCSILoadUnloadCycles: "smartctl_scsi_load_unload_cycles", metricSCSISASPhyInfo: "smartctl_scsi_sas_phy_info", metricSCSISASPhyLinkRateGigabits: "smartctl_scsi_sas_phy_link_rate_gigabits_per_second", metricSCSISASPhyErrors: "smartctl_scsi_sas_phy_errors_total"}
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
			labels = append(labels, label.GetName()+"="+label.GetValue())
		}
		got[name+"{"+strings.Join(labels, ",")+"}"] = pb.GetCounter().GetValue() + pb.GetGauge().GetValue()
	}

	return got
}

func TestMineSCSISASReadings(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		device Device
		want   map[string]float64
	}{
		// A WD Ultrastar SAS disk behind an expander, port 1 unused. Saved with -x, its serial and addresses replaced, and port 0's error counts set above 0.
		{"SAS disk with one port linked", "testdata/scsi-WDC_WUH722020BL5204-sdb.json", Device{Name: "/dev/sdb", Type: "scsi", Label: "sdb"}, map[string]float64{
			"smartctl_scsi_load_unload_cycles{device=sdb}": 1188,
			"smartctl_scsi_sas_phy_info{attached_device_type=expander device,attached_sas_address=5003048000000003,device=sdb,phy=0,port=0,sas_address=5000cca000000001}": 1,
			"smartctl_scsi_sas_phy_info{attached_device_type=no device attached,attached_sas_address=0,device=sdb,phy=1,port=1,sas_address=5000cca000000002}":             1,
			"smartctl_scsi_sas_phy_link_rate_gigabits_per_second{device=sdb,phy=0,port=0}":                                                                                12,
			"smartctl_scsi_sas_phy_link_rate_gigabits_per_second{device=sdb,phy=1,port=1}":                                                                                0,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=invalid_dword,phy=0,port=0}":                                                                             7,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=running_disparity,phy=0,port=0}":                                                                         5,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=loss_of_dword_sync,phy=0,port=0}":                                                                        2,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=phy_reset_problem,phy=0,port=0}":                                                                         1,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=invalid_dword,phy=1,port=1}":                                                                             0,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=running_disparity,phy=1,port=1}":                                                                         0,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=loss_of_dword_sync,phy=1,port=1}":                                                                        0,
			"smartctl_scsi_sas_phy_errors_total{device=sdb,error=phy_reset_problem,phy=1,port=1}":                                                                         0,
		}},
		{"NVMe drive has none", "testdata/nvme-null-HUSMR7632BHP301-nvme0.json", Device{Name: "/dev/nvme0", Type: "nvme", Label: "nvme0"}, map[string]float64{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sasReadings(t, tt.file, tt.device); !maps.Equal(got, tt.want) {
				t.Errorf("got %v\nwant %v", got, tt.want)
			}
		})
	}
}
