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
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"time"

	"github.com/tidwall/gjson"
)

// diskutilPath is macOS's own disk tool, named in full so a service started with a short PATH still finds it.
const diskutilPath = "/usr/sbin/diskutil"

// diskutilTimeout is how long one diskutil call may take. It answers in well under a second; this only stops a stuck one from holding up the scan.
const diskutilTimeout = 30 * time.Second

// readDiskutilDevices lists a Mac's physical whole disks (disk0, disk4 and so on) in the same shape as smartctl's scan.
// smartctl's scan names a Mac's disk by its long IOService path, but smartctl answers to disk0 just as well, and disk0 is the name people know.
// Disks macOS says have no SMART, such as USB disks and SD cards, are left out: smartctl cannot read those either and would log an error on every try.
// It returns nothing when diskutil fails, so the caller falls back to smartctl's scan.
func readDiskutilDevices(logger *slog.Logger) gjson.Result {
	out, err := runDiskutil("list", "-plist", "physical")
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
		if !diskutilDiskHasSMART(logger, disk) {
			continue
		}

		devices = append(devices, map[string]string{"name": disk, "type": "auto"})
	}

	data, err := json.Marshal(map[string]any{"devices": devices})
	if err != nil {
		logger.Warn("diskutil's disk list could not be passed on, using smartctl's scan instead", "err", err)
		return gjson.Result{}
	}

	return gjson.ParseBytes(data)
}

// runDiskutil runs one diskutil command and returns what it printed.
func runDiskutil(args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), diskutilTimeout)
	defer cancel()

	return exec.CommandContext(ctx, diskutilPath, args...).Output()
}

// diskutilDiskHasSMART reports whether macOS can read SMART from the disk. When diskutil cannot say, the disk is kept and smartctl gets to try.
func diskutilDiskHasSMART(logger *slog.Logger, disk string) bool {
	out, err := runDiskutil("info", "-plist", disk)
	if err != nil {
		logger.Warn("diskutil could not describe disk, keeping it", "disk", disk, "err", err)
		return true
	}

	status, err := parseDiskutilSMARTStatus(out)
	if err != nil {
		logger.Warn("diskutil's description of disk could not be read, keeping it", "disk", disk, "err", err)
		return true
	}
	if status == "Not Supported" {
		logger.Debug("Skipping disk macOS says has no SMART", "disk", disk)
		return false
	}

	return true
}

// parseDiskutilWholeDisks returns the WholeDisks list from the plist that diskutil list -plist prints.
func parseDiskutilWholeDisks(plist []byte) ([]string, error) {
	decoder, start, err := plistValue(plist, "WholeDisks")
	if err != nil || start == nil {
		return nil, err
	}

	var list struct {
		Disks []string `xml:"string"`
	}
	if err := decoder.DecodeElement(&list, start); err != nil {
		return nil, err
	}

	return list.Disks, nil
}

// parseDiskutilSMARTStatus returns the SMARTStatus from the plist that diskutil info -plist prints, such as "Verified" or "Not Supported", or "" when there is none.
func parseDiskutilSMARTStatus(plist []byte) (string, error) {
	decoder, start, err := plistValue(plist, "SMARTStatus")
	if err != nil || start == nil {
		return "", err
	}

	var status string
	if err := decoder.DecodeElement(&status, start); err != nil {
		return "", err
	}

	return status, nil
}

// plistValue finds one key in a plist's top-level dict and returns the opening element of its value, ready to decode, or nil when the key is not there.
// The same key inside a nested dict is not it, so this keeps count of how deep it is.
func plistValue(plist []byte, key string) (*xml.Decoder, *xml.StartElement, error) {
	decoder := xml.NewDecoder(bytes.NewReader(plist))
	depth := 0 // dicts and arrays we are inside; the top-level dict is 1
	var lastKey string
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil, nil, nil
		}
		if err != nil {
			return nil, nil, err
		}

		switch t := token.(type) {
		case xml.EndElement:
			if t.Name.Local == "dict" || t.Name.Local == "array" {
				depth--
			}
		case xml.StartElement:
			if depth == 1 && lastKey == key && t.Name.Local != "key" {
				return decoder, &t, nil
			}

			switch {
			case t.Name.Local == "dict" || t.Name.Local == "array":
				depth++
			case t.Name.Local == "key" && depth == 1:
				if err := decoder.DecodeElement(&lastKey, &t); err != nil {
					return nil, nil, err
				}
			case depth == 0:
				// The plist element itself, around the top-level dict.
			default:
				if err := decoder.Skip(); err != nil {
					return nil, nil, err
				}
			}
		}
	}
}
