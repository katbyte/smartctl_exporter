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
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/tidwall/gjson"
)

// SMARTDevice - short info about device
type SMARTDevice struct {
	device string
	serial string
	family string
	model  string
	// These are used to select types of metrics.
	interfaceType string
	protocol      string
}

// SMARTctl object
type SMARTctl struct {
	ch     chan<- prometheus.Metric
	json   gjson.Result
	logger *slog.Logger
	device SMARTDevice
}

func buildDeviceLabel(inputName string, inputType string) string {
	// Strip /dev prefix and replace / with _ (/dev/bus/0 becomes bus_0, /dev/disk/by-id/abcd becomes abcd)
	devReg := regexp.MustCompile(`^/dev/(?:disk/by-id/|disk/by-path/|)`)
	deviceName := strings.ReplaceAll(devReg.ReplaceAllString(inputName, ""), "/", "_")

	if strings.Contains(inputType, ",") {
		return deviceName + "_" + strings.ReplaceAll(inputType, ",", "_")
	}

	return deviceName
}

// NewSMARTctl is smartctl constructor
func NewSMARTctl(logger *slog.Logger, json gjson.Result, ch chan<- prometheus.Metric, device Device) SMARTctl {
	var modelName string
	if obj := json.Get("model_name"); obj.Exists() {
		modelName = obj.String()
	} else if obj := json.Get("scsi_model_name"); obj.Exists() {
		modelName = obj.String()
	}
	// If the drive returns an empty model name, replace that with unknown.
	if modelName == "" {
		modelName = "unknown"
	}

	return SMARTctl{
		ch:     ch,
		json:   json,
		logger: logger,
		device: SMARTDevice{
			device:        device.Label,
			serial:        strings.TrimSpace(json.Get("serial_number").String()),
			family:        strings.TrimSpace(GetStringIfExists(json, "model_family", "unknown")),
			model:         strings.TrimSpace(modelName),
			interfaceType: strings.TrimSpace(json.Get("device.type").String()),
			protocol:      strings.TrimSpace(json.Get("device.protocol").String()),
		},
	}
}

// Collect metrics
func (smart *SMARTctl) Collect() {
	smart.logger.Debug("Collecting metrics from", "device", smart.device.device, "family", smart.device.family, "model", smart.device.model)
	smart.mineExitStatus()
	smart.mineDevice()
	smart.mineCapacity()
	smart.mineBlockSize()
	smart.mineInterfaceSpeed()
	smart.mineDeviceAttribute()
	smart.minePowerOnSeconds()
	smart.mineRotationRate()
	smart.mineTemperatures()
	smart.minePowerCycleCount() // ATA/SATA, NVME, SCSI, SAS
	smart.mineDeviceSCTStatus()
	smart.mineDeviceStatistics()
	smart.mineDeviceErrorLog()
	smart.mineDeviceSelfTestLog()
	smart.mineDeviceERC()
	smart.mineSATAPhyEvents()
	smart.mineTemperatureLimitCounts()
	smart.mineSmartStatus()

	if smart.device.interfaceType == "nvme" || smart.device.protocol == "NVMe" {
		smart.mineNvmePercentageUsed()
		smart.mineNvmeAvailableSpare()
		smart.mineNvmeAvailableSpareThreshold()
		smart.mineNvmeCriticalWarning()
		smart.mineNvmeUnsafeShutdowns()
		smart.mineNvmeTemperatureTime()
		smart.mineNvmeMediaErrors()
		smart.mineNvmeNumErrLogEntries()
		smart.mineNvmeVolatileMemoryBackupFailed()
		smart.mineNvmeBytesRead()
		smart.mineNvmeBytesWritten()
		smart.mineNvmeCounter(metricDeviceControllerBusyTimeSeconds, "controller_busy_time", 60)
		smart.mineNvmeCounter(metricDeviceHostReadCommands, "host_reads", 1)
		smart.mineNvmeCounter(metricDeviceHostWriteCommands, "host_writes", 1)
		smart.mineNvmeTemperatureSensors()
		smart.mineNvmeNamespaceUtilization()
	}
	// SCSI, SAS
	if smart.device.interfaceType == "scsi" {
		smart.mineSCSIUsedEnduranceIndicator()
		smart.mineSCSIGrownDefectList()
		smart.mineSCSILoadUnloadCycles()
		smart.mineSCSISASPhys()
		smart.mineSCSICommandCounts()
		smart.mineSCSITemperatureExtremes()
		smart.mineSCSIRatedCycles()
		smart.mineSCSIErrorCorrections()
		smart.mineSCSIBackgroundScan()
		smart.mineSCSIExtendedSelfTestSeconds()
		smart.mineSCSIErrorCounterLog()
		smart.mineSCSIBytesRead()
		smart.mineSCSIBytesWritten()
	}
}

func (smart *SMARTctl) mineExitStatus() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceExitStatus,
		prometheus.GaugeValue,
		smart.json.Get("smartctl.exit_status").Float(),
		smart.device.device,
	)
}

func (smart *SMARTctl) mineDevice() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceModel,
		prometheus.GaugeValue,
		1,
		smart.device.device,
		smart.device.interfaceType,
		smart.device.protocol,
		smart.device.family,
		smart.device.model,
		smart.device.serial,
		GetStringIfExists(smart.json, "ata_additional_product_id", "unknown"),
		smart.json.Get("firmware_version").String(),
		smart.json.Get("ata_version.string").String(),
		smart.json.Get("sata_version.string").String(),
		smart.json.Get("form_factor.name").String(),
		// scsi_model_name is mapped into model_name
		smart.json.Get("scsi_vendor").String(),
		smart.json.Get("scsi_product").String(),
		smart.json.Get("scsi_revision").String(),
		smart.json.Get("scsi_version").String(),
	)
}

func (smart *SMARTctl) mineCapacity() {
	// The user_capacity exists only when NVMe have single namespace. Otherwise,
	// for NVMe devices with multiple namespaces, when device name used without
	// namespace number (exporter case) user_capacity will be absent, and then
	// the drive gets no capacity reading rather than a zero.
	if capacity := smart.json.Get("user_capacity"); capacity.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(metricDeviceCapacityBlocks, prometheus.GaugeValue, capacity.Get("blocks").Float(), smart.device.device)
		smart.ch <- prometheus.MustNewConstMetric(metricDeviceCapacityBytes, prometheus.GaugeValue, capacity.Get("bytes").Float(), smart.device.device)
	}

	nvmeTotalCapacity := smart.json.Get("nvme_total_capacity")
	if nvmeTotalCapacity.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceTotalCapacityBytes,
			prometheus.GaugeValue,
			nvmeTotalCapacity.Float(),
			smart.device.device,
		)
	}
}

// mineBlockSize sends the logical and physical block sizes, leaving off any the drive does not report rather than sending a zero.
func (smart *SMARTctl) mineBlockSize() {
	for _, blockType := range []string{"logical", "physical"} {
		blockSize := smart.json.Get(fmt.Sprintf("%s_block_size", blockType))
		if !blockSize.Exists() {
			continue
		}

		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceBlockSize,
			prometheus.GaugeValue,
			blockSize.Float(),
			smart.device.device,
			blockType,
		)
	}
}

func (smart *SMARTctl) mineInterfaceSpeed() {
	iSpeed := smart.json.Get("interface_speed")
	if iSpeed.Exists() {
		for _, speedType := range []string{"max", "current"} {
			tSpeed := iSpeed.Get(speedType)
			if tSpeed.Exists() {
				smart.ch <- prometheus.MustNewConstMetric(
					metricDeviceInterfaceSpeed,
					prometheus.GaugeValue,
					tSpeed.Get("units_per_second").Float()*tSpeed.Get("bits_per_unit").Float(),
					smart.device.device,
					speedType,
				)
			}
		}
	}
}

func (smart *SMARTctl) mineDeviceAttribute() {
	for _, attribute := range smart.json.Get("ata_smart_attributes.table").Array() {
		name := strings.TrimSpace(attribute.Get("name").String())
		flagsShort := strings.TrimSpace(attribute.Get("flags.string").String())
		flagsLong := smart.mineLongFlags(attribute.Get("flags"), []string{
			"prefailure",
			"updated_online",
			"performance",
			"error_rate",
			"event_count",
			"auto_keep",
		})
		id := attribute.Get("id").String()
		for key, path := range map[string]string{
			"value":  "value",
			"worst":  "worst",
			"thresh": "thresh",
			"raw":    "raw.value",
		} {
			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceAttribute,
				prometheus.GaugeValue,
				attribute.Get(path).Float(),
				smart.device.device,
				name,
				flagsShort,
				flagsLong,
				key,
				id,
			)
		}
	}
}

func (smart *SMARTctl) minePowerOnSeconds() {
	pot := smart.json.Get("power_on_time")
	// If the power_on_time is NOT present, do not report as 0.
	if pot.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDevicePowerOnSeconds,
			prometheus.CounterValue,
			GetFloatIfExists(pot, "hours", 0)*60*60+GetFloatIfExists(pot, "minutes", 0)*60,
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineRotationRate() {
	rRate := GetFloatIfExists(smart.json, "rotation_rate", 0)
	// TODO: what should be done if this is absent vs really zero (for
	// solid-state drives)?
	if rRate > 0 {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceRotationRate,
			prometheus.GaugeValue,
			rRate,
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineTemperatures() {
	temperatures := smart.json.Get("temperature")
	// TODO: Implement scsi_environmental_reports
	if temperatures.Exists() {
		temperatures.ForEach(func(key, value gjson.Result) bool {
			// Time over and under the limits, from the device statistics, is in minutes, and is sent with the rest of those statistics.
			if strings.HasSuffix(key.String(), "_minutes") {
				return true
			}

			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceTemperature,
				prometheus.GaugeValue,
				value.Float(),
				smart.device.device,
				key.String(),
			)
			return true
		})
	}
}

func (smart *SMARTctl) minePowerCycleCount() {
	// ATA & NVME
	powerCycleCount := smart.json.Get("power_cycle_count")
	if powerCycleCount.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDevicePowerCycleCount,
			prometheus.CounterValue,
			powerCycleCount.Float(),
			smart.device.device,
		)
		return
	}

	// SCSI
	powerCycleCount = smart.json.Get("scsi_start_stop_cycle_counter.accumulated_start_stop_cycles")
	if powerCycleCount.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDevicePowerCycleCount,
			prometheus.CounterValue,
			powerCycleCount.Float(),
			smart.device.device,
		)
		return
	}
}

func (smart *SMARTctl) mineDeviceSCTStatus() {
	status := smart.json.Get("ata_sct_status")
	if status.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceState,
			prometheus.GaugeValue,
			status.Get("device_state").Float(),
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineNvmePercentageUsed() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDevicePercentageUsed,
		prometheus.CounterValue,
		smart.json.Get("nvme_smart_health_information_log.percentage_used").Float(),
		smart.device.device,
	)
}

func (smart *SMARTctl) mineNvmeAvailableSpare() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceAvailableSpare,
		prometheus.CounterValue,
		smart.json.Get("nvme_smart_health_information_log.available_spare").Float(),
		smart.device.device,
	)
}

func (smart *SMARTctl) mineNvmeAvailableSpareThreshold() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceAvailableSpareThreshold,
		prometheus.CounterValue,
		smart.json.Get("nvme_smart_health_information_log.available_spare_threshold").Float(),
		smart.device.device,
	)
}

func (smart *SMARTctl) mineNvmeCriticalWarning() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceCriticalWarning,
		prometheus.CounterValue,
		smart.json.Get("nvme_smart_health_information_log.critical_warning").Float(),
		smart.device.device,
	)
}

// mineNvmeUnsafeShutdowns sends how many times the drive lost power without a shutdown notice (CC.SHN) first, so a clean shutdown does not count.
// A drive that does not report it gets no reading rather than a zero.
func (smart *SMARTctl) mineNvmeUnsafeShutdowns() {
	value := smart.json.Get("nvme_smart_health_information_log.unsafe_shutdowns")
	if !value.Exists() {
		return
	}

	smart.ch <- prometheus.MustNewConstMetric(metricDeviceUnsafeShutdowns, prometheus.CounterValue, value.Float(), smart.device.device)
}

// nvmeTemperatureTimes are the health log's two time-over-temperature fields, counted in minutes, and the readings they become.
var nvmeTemperatureTimes = []struct {
	field string
	desc  *prometheus.Desc
}{{"warning_temp_time", metricDeviceWarningTemperatureTimeSeconds}, {"critical_comp_time", metricDeviceCriticalTemperatureTimeSeconds}}

// mineNvmeTemperatureTime sends how long the drive has run at or above its own warning and critical temperatures, in seconds.
// A drive that does not report a field gets no reading rather than a zero.
func (smart *SMARTctl) mineNvmeTemperatureTime() {
	for _, reading := range nvmeTemperatureTimes {
		value := smart.json.Get("nvme_smart_health_information_log." + reading.field)
		if !value.Exists() {
			continue
		}

		smart.ch <- prometheus.MustNewConstMetric(reading.desc, prometheus.CounterValue, value.Float()*60, smart.device.device)
	}
}

func (smart *SMARTctl) mineNvmeMediaErrors() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceMediaErrors,
		prometheus.CounterValue,
		smart.json.Get("nvme_smart_health_information_log.media_errors").Float(),
		smart.device.device,
	)
}

func (smart *SMARTctl) mineNvmeNumErrLogEntries() {
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceNumErrLogEntries,
		prometheus.CounterValue,
		smart.json.Get("nvme_smart_health_information_log.num_err_log_entries").Float(),
		smart.device.device,
	)
}

func (smart *SMARTctl) mineNvmeVolatileMemoryBackupFailed() {
	nvmeStatus := smart.json.Get("smart_status.nvme")
	if nvmeStatus.Exists() {
		volatileMemoryBackupFailed := nvmeStatus.Get("volatile_memory_backup_failed")
		if volatileMemoryBackupFailed.Exists() {
			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceVolatileMemoryBackupFailed,
				prometheus.CounterValue,
				volatileMemoryBackupFailed.Float(),
				smart.device.device,
			)
		}
	}
}

// https://nvmexpress.org/wp-content/uploads/NVM-Express-NVM-Command-Set-Specification-1.0d-2023.12.28-Ratified.pdf
// 4.1.4.2 SMART / Health Information (02h)
// The SMART / Health Information log page is as defined in the NVM Express Base Specification. For the
// Data Units Read and Data Units Written fields, when the logical block size is a value other than 512 bytes,
// the controller shall convert the amount of data read to 512 byte units.

// https://nvmexpress.org/wp-content/uploads/NVM-Express-Base-Specification-2.0d-2024.01.11-Ratified.pdf
// Figure 208: SMART / Health Information Log Page
// Bytes 47:32
// Data Units Read: Contains the number of 512 byte data units the host has read from the
// controller as part of processing a SMART Data Units Read Command; this value does not
// include metadata. This value is reported in thousands (i.e., a value of 1 corresponds to 1,000
// units of 512 bytes read) and is rounded up (e.g., one indicates that the number of 512 byte
// data units read is from 1 to 1,000, three indicates that the number of 512 byte data units read
// is from 2,001 to 3,000).
//
// A value of 0h in this field indicates that the number of SMART Data Units Read is not reported.
//
// Bytes 63:48
//
// Data Units Written: Contains the number of 512 byte data units the host has written to the ...
// (the same as Data Units Read)

// mineNvmeCounter sends one count from the NVMe health log, multiplied by scale; 60 turns the log's minutes into seconds.
// A drive that does not report the field gets no reading rather than a zero.
func (smart *SMARTctl) mineNvmeCounter(desc *prometheus.Desc, field string, scale float64) {
	value := smart.json.Get("nvme_smart_health_information_log." + field)
	if !value.Exists() {
		return
	}

	smart.ch <- prometheus.MustNewConstMetric(desc, prometheus.CounterValue, value.Float()*scale, smart.device.device)
}

// mineNvmeTemperatureSensors sends each of the drive's own temperature sensors as temperature_type="sensor_N", numbered from 1 as the NVMe spec does.
// smartctl leaves a gap for a sensor the drive does not have, so a gap gets no reading rather than 0 degrees.
func (smart *SMARTctl) mineNvmeTemperatureSensors() {
	for i, value := range smart.json.Get("nvme_smart_health_information_log.temperature_sensors").Array() {
		if value.Type != gjson.Number {
			continue
		}

		smart.ch <- prometheus.MustNewConstMetric(metricDeviceTemperature, prometheus.GaugeValue, value.Float(), smart.device.device, fmt.Sprintf("sensor_%d", i+1))
	}
}

// mineNvmeNamespaceUtilization sends how many bytes of each namespace hold data, labelled with the namespace's number.
func (smart *SMARTctl) mineNvmeNamespaceUtilization() {
	for _, namespace := range smart.json.Get("nvme_namespaces").Array() {
		utilization := namespace.Get("utilization.bytes")
		if !utilization.Exists() {
			continue
		}

		smart.ch <- prometheus.MustNewConstMetric(metricDeviceNamespaceUtilizationBytes, prometheus.GaugeValue, utilization.Float(), smart.device.device, namespace.Get("id").String())
	}
}

func (smart *SMARTctl) mineNvmeBytesRead() {
	value := smart.json.Get("nvme_smart_health_information_log.data_units_read")
	// 0 => not reported by underlying hardware
	if !value.Exists() || value.Int() == 0 {
		return
	}
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceBytesRead,
		prometheus.CounterValue,
		// WARNING: Float64 will lose precision when drives reach ~32EiB read/write
		// The underlying data_units_written,data_units_read are 128-bit integers
		value.Float()*1000.0*512.0,
		smart.device.device,
	)
}

func (smart *SMARTctl) mineNvmeBytesWritten() {
	value := smart.json.Get("nvme_smart_health_information_log.data_units_written")
	// 0 => not reported by underlying hardware
	if !value.Exists() || value.Int() == 0 {
		return
	}
	smart.ch <- prometheus.MustNewConstMetric(
		metricDeviceBytesWritten,
		prometheus.CounterValue,
		// WARNING: Float64 will lose precision when drives reach ~32EiB read/write
		// The underlying data_units_written,data_units_read are 128-bit integers
		value.Float()*1000.0*512.0,
		smart.device.device,
	)
}

func (smart *SMARTctl) mineSCSIBytesRead() {
	SCSIHealth := smart.json.Get("scsi_error_counter_log")
	if SCSIHealth.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceBytesRead,
			prometheus.CounterValue,
			// This value is reported by SMARTctl in GB [10^9].
			// It is possible that some drives mis-report the value, but
			// that is not the responsibility of the exporter or smartctl
			SCSIHealth.Get("read.gigabytes_processed").Float()*1e9,
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineSCSIBytesWritten() {
	SCSIHealth := smart.json.Get("scsi_error_counter_log")
	if SCSIHealth.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceBytesWritten,
			prometheus.CounterValue,
			// This value is reported by SMARTctl in GB [10^9].
			// It is possible that some drives mis-report the value, but
			// that is not the responsibility of the exporter or smartctl
			SCSIHealth.Get("write.gigabytes_processed").Float()*1e9,
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineSmartStatus() {
	smartStatus := smart.json.Get("smart_status")
	if smartStatus.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceSmartStatus,
			prometheus.GaugeValue,
			smartStatus.Get("passed").Float(),
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineDeviceStatistics() {
	for _, page := range smart.json.Get("ata_device_statistics.pages").Array() {
		table := strings.TrimSpace(page.Get("name").String())
		// skip vendor-specific statistics (they lead to duplicate metric labels on Seagate Exos drives,
		// see https://github.com/Sheridan/smartctl_exporter/issues/3 for details)
		if table == "Vendor Specific Statistics" {
			continue
		}
		for _, statistic := range page.Get("table").Array() {
			// A statistic the disk does not keep has no value, and is left off rather than shown as 0.
			value := statistic.Get("value")
			if value.Type != gjson.Number {
				continue
			}

			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceStatistics,
				prometheus.GaugeValue,
				value.Float(),
				smart.device.device,
				table,
				strings.TrimSpace(statistic.Get("name").String()),
				strings.TrimSpace(statistic.Get("flags.string").String()),
				smart.mineLongFlags(statistic.Get("flags"), []string{
					"valid",
					"normalized",
					"supports_dsn",
					"monitored_condition_met",
				}),
			)
		}
	}

	for _, statistic := range smart.json.Get("sata_phy_event_counters.table").Array() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceStatistics,
			prometheus.GaugeValue,
			statistic.Get("value").Float(),
			smart.device.device,
			"SATA PHY Event Counters",
			strings.TrimSpace(statistic.Get("name").String()),
			"V---",
			"valid",
		)
	}
}

func (smart *SMARTctl) mineLongFlags(json gjson.Result, flags []string) string {
	var result []string
	for _, flag := range flags {
		jFlag := json.Get(flag)
		if jFlag.Exists() && jFlag.Bool() {
			result = append(result, flag)
		}
	}
	return strings.Join(result, ",")
}

func (smart *SMARTctl) mineDeviceErrorLog() {
	for logType, status := range smart.json.Get("ata_smart_error_log").Map() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceErrorLogCount,
			prometheus.GaugeValue,
			status.Get("count").Float(),
			smart.device.device,
			logType,
		)
	}
}

func (smart *SMARTctl) mineDeviceSelfTestLog() {
	// For SCSI devices
	if smart.device.interfaceType == "scsi" {
		var lastTest gjson.Result
		var maxHours int64

		// Find the most recent test by power_on_time.hours
		smart.json.ForEach(func(key, entry gjson.Result) bool {
			if strings.HasPrefix(key.String(), "scsi_self_test_") {
				hours := entry.Get("power_on_time.hours").Int()
				if hours > maxHours {
					maxHours = hours
					lastTest = entry
				}
			}
			return true
		})

		if lastTest.Exists() {
			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceLastSelfTest,
				prometheus.GaugeValue,
				lastTest.Get("result.value").Float(),
				smart.device.device,
				lastTest.Get("code.string").String(),
				fmt.Sprintf("%d", maxHours),
			)

			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceLastSelfTestInfo,
				prometheus.GaugeValue,
				1.0,
				smart.device.device,
				lastTest.Get("code.string").String(),
				fmt.Sprintf("%d", maxHours),
				fmt.Sprintf("%d", lastTest.Get("result.value").Int()),
				lastTest.Get("result.string").String(),
			)
		}
		return
	}

	// Original ATA/SAS handling
	for logType, status := range smart.json.Get("ata_smart_self_test_log").Map() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceSelfTestLogCount,
			prometheus.GaugeValue,
			status.Get("count").Float(),
			smart.device.device,
			logType,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceSelfTestLogErrorCount,
			prometheus.GaugeValue,
			status.Get("error_count_total").Float(),
			smart.device.device,
			logType,
		)

		// Get the first (most recent) test from the table if it exists
		table := status.Get("table")
		if table.Exists() && len(table.Array()) > 0 {
			lastTest := table.Array()[0]
			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceLastSelfTest,
				prometheus.GaugeValue,
				lastTest.Get("status.value").Float(),
				smart.device.device,
				lastTest.Get("type.string").String(),
				fmt.Sprintf("%d", lastTest.Get("lifetime_hours").Int()),
			)

			smart.ch <- prometheus.MustNewConstMetric(
				metricDeviceLastSelfTestInfo,
				prometheus.GaugeValue,
				1,
				smart.device.device,
				lastTest.Get("type.string").String(),
				fmt.Sprintf("%d", lastTest.Get("lifetime_hours").Int()),
				fmt.Sprintf("%d", lastTest.Get("status.value").Int()),
				lastTest.Get("status.string").String(),
			)
		}
	}
}

func (smart *SMARTctl) mineDeviceERC() {
	for ercType, status := range smart.json.Get("ata_sct_erc").Map() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricDeviceERCSeconds,
			prometheus.GaugeValue,
			status.Get("deciseconds").Float()/10.0,
			smart.device.device,
			ercType,
		)
	}
}

// mineSATAPhyEvents sends the events the disk has counted on its SATA link, such as CRC errors and resets, by smartctl's name for each.
// smartctl only has them when asked for the sataphy log.
func (smart *SMARTctl) mineSATAPhyEvents() {
	for _, event := range smart.json.Get("sata_phy_event_counters.table").Array() {
		smart.ch <- prometheus.MustNewConstMetric(metricDeviceSATAPhyEvents, prometheus.CounterValue, event.Get("value").Float(), smart.device.device, event.Get("name").String())
	}
}

// mineTemperatureLimitCounts sends how many times the disk has gone above its maximum and below its minimum operating temperature, from its
// SCT status. smartctl only has that when asked for the scttempsts log.
func (smart *SMARTctl) mineTemperatureLimitCounts() {
	status := smart.json.Get("ata_sct_status.temperature")
	if over := status.Get("over_limit_count"); over.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(metricDeviceTemperatureOverLimitCount, prometheus.CounterValue, over.Float(), smart.device.device)
	}
	if under := status.Get("under_limit_count"); under.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(metricDeviceTemperatureUnderLimitCount, prometheus.CounterValue, under.Float(), smart.device.device)
	}
}

func (smart *SMARTctl) mineSCSIGrownDefectList() {
	value := smart.json.Get("scsi_grown_defect_list")
	if value.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricSCSIGrownDefectList,
			prometheus.GaugeValue,
			value.Float(),
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineSCSIUsedEnduranceIndicator() {
	value := smart.json.Get("scsi_percentage_used_endurance_indicator")
	if value.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricSCSIUsedEnduranceIndicator,
			prometheus.GaugeValue,
			value.Float(),
			smart.device.device,
		)
	}
}

func (smart *SMARTctl) mineSCSIErrorCounterLog() {
	SCSIHealth := smart.json.Get("scsi_error_counter_log")
	if SCSIHealth.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(
			metricReadErrorsCorrectedByRereadsRewrites,
			prometheus.GaugeValue,
			SCSIHealth.Get("read.errors_corrected_by_rereads_rewrites").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricReadErrorsCorrectedByEccFast,
			prometheus.GaugeValue,
			SCSIHealth.Get("read.errors_corrected_by_eccfast").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricReadErrorsCorrectedByEccDelayed,
			prometheus.GaugeValue,
			SCSIHealth.Get("read.errors_corrected_by_eccdelayed").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricReadTotalUncorrectedErrors,
			prometheus.GaugeValue,
			SCSIHealth.Get("read.total_uncorrected_errors").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricWriteErrorsCorrectedByRereadsRewrites,
			prometheus.GaugeValue,
			SCSIHealth.Get("write.errors_corrected_by_rereads_rewrites").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricWriteErrorsCorrectedByEccFast,
			prometheus.GaugeValue,
			SCSIHealth.Get("write.errors_corrected_by_eccfast").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricWriteErrorsCorrectedByEccDelayed,
			prometheus.GaugeValue,
			SCSIHealth.Get("write.errors_corrected_by_eccdelayed").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricWriteTotalUncorrectedErrors,
			prometheus.GaugeValue,
			SCSIHealth.Get("write.total_uncorrected_errors").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricVerifyErrorsCorrectedByRereadsRewrites,
			prometheus.GaugeValue,
			SCSIHealth.Get("verify.errors_corrected_by_rereads_rewrites").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricVerifyErrorsCorrectedByEccFast,
			prometheus.GaugeValue,
			SCSIHealth.Get("verify.errors_corrected_by_eccfast").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricVerifyErrorsCorrectedByEccDelayed,
			prometheus.GaugeValue,
			SCSIHealth.Get("verify.errors_corrected_by_eccdelayed").Float(),
			smart.device.device,
		)
		smart.ch <- prometheus.MustNewConstMetric(
			metricVerifyTotalUncorrectedErrors,
			prometheus.GaugeValue,
			SCSIHealth.Get("verify.total_uncorrected_errors").Float(),
			smart.device.device,
		)
	}
}

// mineSCSILoadUnloadCycles sends how many times the disk has parked and unparked its heads, one of the counts a disk is rated for.
func (smart *SMARTctl) mineSCSILoadUnloadCycles() {
	value := smart.json.Get("scsi_start_stop_cycle_counter.accumulated_load_unload_cycles")
	if !value.Exists() {
		return
	}

	smart.ch <- prometheus.MustNewConstMetric(metricSCSILoadUnloadCycles, prometheus.CounterValue, value.Float(), smart.device.device)
}

// sasLinkRate is the speed in smartctl's "phy enabled; 12 Gbps" (1.5, 3, 6, 12 or 22.5). Anything without one, such as "phy enabled; unknown", is a phy with no link.
var sasLinkRate = regexp.MustCompile(`([0-9.]+) Gbps`)

// sasPhyErrors are the four error counts a SAS phy keeps, by smartctl's name and the name sas-exporter gives the same count.
var sasPhyErrors = [][2]string{{"invalid_dword_count", "invalid_dword"}, {"running_disparity_error_count", "running_disparity"}, {"loss_of_dword_synchronization_count", "loss_of_dword_sync"}, {"phy_reset_problem_count", "phy_reset_problem"}}

// mineSCSISASPhys sends, for each phy on each of a SAS disk's ports, what it is linked to, its link speed and the errors the disk has counted on that link.
// smartctl only has these when asked for the sasphy log.
func (smart *SMARTctl) mineSCSISASPhys() {
	smart.json.ForEach(func(key, port gjson.Result) bool {
		portNumber, isPort := strings.CutPrefix(key.String(), "scsi_sas_port_")
		if !isPort {
			return true
		}

		port.ForEach(func(key, phy gjson.Result) bool {
			phyNumber, isPhy := strings.CutPrefix(key.String(), "phy_")
			if !isPhy {
				return true
			}
			if id := phy.Get("identifier"); id.Exists() {
				phyNumber = id.String()
			}

			smart.ch <- prometheus.MustNewConstMetric(metricSCSISASPhyInfo, prometheus.GaugeValue, 1, smart.device.device, portNumber, phyNumber, sasAddress(phy.Get("sas_address").String()), sasAddress(phy.Get("attached_sas_address").String()), phy.Get("attached_device_type").String())

			if rate := phy.Get("negotiated_logical_link_rate"); rate.Exists() {
				bitsPerSecond := 0.0
				if match := sasLinkRate.FindStringSubmatch(rate.String()); match != nil {
					gigabits, _ := strconv.ParseFloat(match[1], 64)
					bitsPerSecond = gigabits * 1e9
				}
				smart.ch <- prometheus.MustNewConstMetric(metricSCSISASPhyLinkRateBitsPerSecond, prometheus.GaugeValue, bitsPerSecond, smart.device.device, portNumber, phyNumber)
			}

			for _, names := range sasPhyErrors {
				if count := phy.Get(names[0]); count.Exists() {
					smart.ch <- prometheus.MustNewConstMetric(metricSCSISASPhyErrors, prometheus.CounterValue, count.Float(), smart.device.device, portNumber, phyNumber, names[1])
				}
			}

			return true
		})

		return true
	})
}

// sasAddress writes a SAS address the way sas-exporter does, lowercase hex with no 0x, so the two join on it.
func sasAddress(address string) string {
	return strings.TrimPrefix(strings.ToLower(address), "0x")
}

// scsiReading is one field of a smartctl answer and the reading it is sent as.
type scsiReading struct {
	field string
	desc  *prometheus.Desc
}

// mineSCSIReadings sends each field found under the given part of the answer as its reading. A field the disk does not report is left off.
func (smart *SMARTctl) mineSCSIReadings(path string, kind prometheus.ValueType, readings []scsiReading) {
	for _, reading := range readings {
		if value := smart.json.Get(path + "." + reading.field); value.Exists() {
			smart.ch <- prometheus.MustNewConstMetric(reading.desc, kind, value.Float(), smart.device.device)
		}
	}
}

// mineSCSICommandCounts sends how many read and write commands the disk has completed, from its general statistics log. smartctl only has
// that log when asked for genstats.
func (smart *SMARTctl) mineSCSICommandCounts() {
	smart.mineSCSIReadings("scsi_general_statistics_and_performance_log.general_access", prometheus.CounterValue, []scsiReading{{"number_of_read_commands", metricSCSIReadCommands}, {"number_of_write_commands", metricSCSIWriteCommands}})
}

// scsiTemperatureExtremes are the highs and lows a SAS disk keeps for each of its temperature sensors, by smartctl's name and the
// temperature_type they are sent as.
var scsiTemperatureExtremes = [][2]string{{"lifetime_maximum", "lifetime_max"}, {"lifetime_minimum", "lifetime_min"}, {"maximum_since_power_on", "since_power_on_max"}, {"minimum_since_power_on", "since_power_on_min"}}

// mineSCSITemperatureExtremes sends each sensor's lifetime and since-power-on highs and lows as more types of the temperature reading.
// The first sensor's current temperature is already sent as "current", so only a further sensor's is sent, as "sensor_N", and that sensor's
// extremes carry the same prefix. smartctl writes an extreme the disk has not recorded as a word, and that is left off. It only has the
// environmental report when asked for envrep.
func (smart *SMARTctl) mineSCSITemperatureExtremes() {
	smart.json.Get("scsi_environmental_reports").ForEach(func(key, sensor gjson.Result) bool {
		number, isTemperature := strings.CutPrefix(key.String(), "temperature_")
		if !isTemperature {
			return true
		}

		prefix := ""
		if number != "1" {
			prefix = "sensor_" + number + "_"
			if current := sensor.Get("current"); current.Type == gjson.Number {
				smart.ch <- prometheus.MustNewConstMetric(metricDeviceTemperature, prometheus.GaugeValue, current.Float(), smart.device.device, "sensor_"+number)
			}
		}
		for _, names := range scsiTemperatureExtremes {
			if value := sensor.Get(names[0]); value.Type == gjson.Number {
				smart.ch <- prometheus.MustNewConstMetric(metricDeviceTemperature, prometheus.GaugeValue, value.Float(), smart.device.device, prefix+names[1])
			}
		}

		return true
	})
}

// mineSCSIRatedCycles sends how many start/stop cycles and head parks the disk is rated for over its life, so the counts it has reached can be
// read as a share of that.
func (smart *SMARTctl) mineSCSIRatedCycles() {
	smart.mineSCSIReadings("scsi_start_stop_cycle_counter", prometheus.GaugeValue, []scsiReading{{"specified_cycle_count_over_device_lifetime", metricSCSIRatedStartStopCycles}, {"specified_load_unload_count_over_device_lifetime", metricSCSIRatedLoadUnloadCycles}})
}

// mineSCSIErrorCorrections sends the two error counters per direction that mineSCSIErrorCounterLog leaves out: how many errors the disk
// corrected in all, and how many times it ran its correction algorithm.
func (smart *SMARTctl) mineSCSIErrorCorrections() {
	smart.mineSCSIReadings("scsi_error_counter_log.read", prometheus.GaugeValue, []scsiReading{{"total_errors_corrected", metricReadTotalErrorsCorrected}, {"correction_algorithm_invocations", metricReadCorrectionAlgorithmInvocations}})
	smart.mineSCSIReadings("scsi_error_counter_log.write", prometheus.GaugeValue, []scsiReading{{"total_errors_corrected", metricWriteTotalErrorsCorrected}, {"correction_algorithm_invocations", metricWriteCorrectionAlgorithmInvocations}})
	smart.mineSCSIReadings("scsi_error_counter_log.verify", prometheus.GaugeValue, []scsiReading{{"total_errors_corrected", metricVerifyTotalErrorsCorrected}, {"correction_algorithm_invocations", metricVerifyCorrectionAlgorithmInvocations}})
}

// mineSCSIBackgroundScan sends how many background scans the disk has run on its own, how far the current one has got, and its status.
// smartctl writes the progress as a percentage with a sign ("0.00%"), sent here as a ratio. It only has this log when asked for background.
func (smart *SMARTctl) mineSCSIBackgroundScan() {
	status := smart.json.Get("scsi_background_scan.status")
	if !status.Exists() {
		return
	}

	smart.mineSCSIReadings("scsi_background_scan.status", prometheus.CounterValue, []scsiReading{{"number_scans_performed", metricSCSIBackgroundScans}, {"number_medium_scans_performed", metricSCSIBackgroundMediumScans}})
	if progress, err := strconv.ParseFloat(strings.TrimSuffix(status.Get("scan_progress").String(), "%"), 64); err == nil {
		smart.ch <- prometheus.MustNewConstMetric(metricSCSIBackgroundScanProgressRatio, prometheus.GaugeValue, progress/100, smart.device.device)
	}
	if value := status.Get("value"); value.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(metricSCSIBackgroundScanStatus, prometheus.GaugeValue, value.Float(), smart.device.device, status.Get("string").String())
	}
}

// mineSCSIExtendedSelfTestSeconds sends how long the disk says its long self-test takes.
func (smart *SMARTctl) mineSCSIExtendedSelfTestSeconds() {
	if value := smart.json.Get("scsi_extended_self_test_seconds"); value.Exists() {
		smart.ch <- prometheus.MustNewConstMetric(metricSCSIExtendedSelfTestSeconds, prometheus.GaugeValue, value.Float(), smart.device.device)
	}
}
