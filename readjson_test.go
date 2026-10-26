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
	"testing"
)

func TestJSONIsOk(t *testing.T) {
	// An Apple SSD: smartctl cannot read its error log and says so as an error, but the health readings are all there.
	appleSSD, err := os.ReadFile("testdata/nvme-null-APPLE_SSD_AP1024R-disk0.json")
	if err != nil {
		t.Fatal(err)
	}

	withMessage := func(message string) string {
		return `{"device": {"name": "/dev/nvme0"}, "smartctl": {"messages": [{"string": "` + message + `", "severity": "error"}]}}`
	}

	tests := []struct {
		name   string
		answer string
		want   bool
	}{
		{"no messages", `{"device": {"name": "/dev/nvme0"}, "smartctl": {"exit_status": 0}}`, true},
		{"error log could not be read", string(appleSSD), true},
		{"error log cut short", withMessage("Read Error Information Log failed, 2 entries missing: NVMe Status 0x109"), true},
		{"self-test log could not be read", withMessage("Read Self-test Log failed: NVMe Status 0x109"), true},
		// Without these there is no answer worth keeping.
		{"drive would not identify itself", withMessage("Read NVMe Identify Controller failed: NVMe Status 0x109"), false},
		{"health log could not be read", withMessage("Read NVMe SMART/Health Information (NSID 0xffffffff) failed: NVMe Status 0x109"), false},
		{"device could not be opened", withMessage("Smartctl open device: /dev/nvme0 failed: No such device"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonIsOk(slog.New(slog.DiscardHandler), parseJSON(tt.answer)); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
