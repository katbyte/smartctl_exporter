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
	"os"
	"slices"
	"testing"
)

func TestParseDiskutilWholeDisks(t *testing.T) {
	// Saved from a Mac with one internal SSD, with a USB disk added and the disk IDs blanked.
	saved, err := os.ReadFile("testdata/diskutil-list-physical.plist")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		plist []byte
		want  []string
	}{
		// Only whole disks, never their partitions (disk0s1) from the other lists.
		{"internal SSD and a USB disk", saved, []string{"disk0", "disk4"}},
		{"no physical disks", []byte(`<plist version="1.0"><dict><key>AllDisks</key><array/><key>WholeDisks</key><array/></dict></plist>`), nil},
		{"not a plist", []byte("diskutil: unknown option"), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDiskutilWholeDisks(tt.plist)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseDiskutilSMARTStatus(t *testing.T) {
	// Saved from a Mac's internal SSD. The dict of SMART values just before SMARTStatus checks that keys inside a nested dict are passed over.
	saved, err := os.ReadFile("testdata/diskutil-info-disk0.plist")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		plist []byte
		want  string
	}{
		{"internal SSD", saved, "Verified"},
		{"USB disk", []byte(`<plist version="1.0"><dict><key>BusProtocol</key><string>USB</string><key>SMARTStatus</key><string>Not Supported</string></dict></plist>`), "Not Supported"},
		{"not a plist", []byte("diskutil: unknown option"), ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDiskutilSMARTStatus(tt.plist)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
