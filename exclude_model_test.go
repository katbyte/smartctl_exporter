// Copyright 2022 The Prometheus Authors
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
	"reflect"
	"testing"
)

// smartctlAnswering installs a shell script as smartctl. The script sees the device name as its last argument, and leaves a file named asked
// beside itself whenever it is run.
func smartctlAnswering(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "smartctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch \"$(dirname \"$0\")/asked\"\nfor last; do :; done\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	old := *smartctlPath
	*smartctlPath = path
	t.Cleanup(func() { *smartctlPath = old })
	return filepath.Join(dir, "asked")
}

func TestWithoutIgnoredModels(t *testing.T) {
	// A SCSI device names itself by vendor and product, an ATA or NVMe one by model name, a disk left asleep not at all.
	asked := smartctlAnswering(t, `case "$last" in
/dev/sda) echo '{"scsi_vendor":"QEMU","scsi_product":"QEMU HARDDISK","scsi_model_name":"QEMU QEMU HARDDISK"}' ;;
/dev/sdb) echo '{"model_name":"WDC WD200EDGZ-11B9PA0"}' ;;
/dev/nvme0n1) echo '{"model_name":"SAMSUNG MZQL27T6HBLA-00A07"}' ;;
/dev/sdc) echo '{"smartctl":{"exit_status":2,"messages":[{"string":"Device is in STANDBY mode, exit(2)","severity":"information"}]}}'; exit 2 ;;
esac`)
	devices := []Device{{Name: "/dev/sda", Type: "auto", Label: "sda"}, {Name: "/dev/sdb", Type: "auto", Label: "sdb"}, {Name: "/dev/nvme0n1", Type: "nvme", Label: "nvme0n1"}, {Name: "/dev/sdc", Type: "auto", Label: "sdc"}}
	logger := slog.New(slog.DiscardHandler)

	tests := []struct {
		name    string
		pattern string
		kept    []Device
	}{
		{"without the flag every device is kept", "", devices},
		{"the virtual disk is left out and the sleeping disk kept", "^QEMU ", []Device{devices[1], devices[2], devices[3]}},
		{"the model is what smartctl calls it, whatever kind of device", "WD200EDGZ|SAMSUNG", []Device{devices[0], devices[3]}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if kept := withoutIgnoredModels(logger, newDeviceFilter("", "", test.pattern), devices); !reflect.DeepEqual(kept, test.kept) {
				t.Errorf("pattern=%q kept=%v expected=%v", test.pattern, kept, test.kept)
			}

			if _, err := os.Stat(asked); (err == nil) != (test.pattern != "") {
				t.Errorf("pattern=%q smartctl asked=%v, expected only with a pattern", test.pattern, err == nil)
			}
		})
	}
}
