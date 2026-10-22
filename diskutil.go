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
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"os/exec"

	"github.com/tidwall/gjson"
)

// diskutilPath is macOS's own disk tool, named in full so a service started with a short PATH still finds it.
const diskutilPath = "/usr/sbin/diskutil"

// readDiskutilDevices lists a Mac's physical whole disks (disk0, disk4 and so on) in the same shape as smartctl's scan.
// smartctl's scan names a Mac's disk by its long IOService path, but smartctl answers to disk0 just as well, and disk0 is the name people know.
// It returns nothing when diskutil fails, so the caller falls back to smartctl's scan.
func readDiskutilDevices(logger *slog.Logger) gjson.Result {
	out, err := exec.Command(diskutilPath, "list", "-plist", "physical").Output()
	if err != nil {
		logger.Warn("diskutil could not list disks, using smartctl's scan instead", "err", err)
		return gjson.Result{}
	}

	disks, err := parseDiskutilWholeDisks(out)
	if err != nil {
		logger.Warn("diskutil's disk list could not be read, using smartctl's scan instead", "err", err)
		return gjson.Result{}
	}

	devices := make([]map[string]string, 0, len(disks))
	for _, disk := range disks {
		devices = append(devices, map[string]string{"name": disk, "type": "auto"})
	}

	data, err := json.Marshal(map[string]any{"devices": devices})
	if err != nil {
		logger.Warn("diskutil's disk list could not be passed on, using smartctl's scan instead", "err", err)
		return gjson.Result{}
	}

	return gjson.ParseBytes(data)
}

// parseDiskutilWholeDisks returns the WholeDisks list from the plist that diskutil list -plist prints.
func parseDiskutilWholeDisks(plist []byte) ([]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(plist))
	var lastKey string
	var inWholeDisks bool
	var disks []string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return disks, nil
		}
		if err != nil {
			return nil, err
		}

		if end, ok := token.(xml.EndElement); ok && end.Name.Local == "array" && inWholeDisks {
			return disks, nil
		}

		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}

		switch start.Name.Local {
		case "key":
			if err := decoder.DecodeElement(&lastKey, &start); err != nil {
				return nil, err
			}
		case "array":
			inWholeDisks = lastKey == "WholeDisks"
		case "string":
			if !inWholeDisks {
				continue
			}

			var disk string
			if err := decoder.DecodeElement(&disk, &start); err != nil {
				return nil, err
			}
			disks = append(disks, disk)
		}
	}
}
