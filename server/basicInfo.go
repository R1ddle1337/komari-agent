package server

import (
	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
	monitoring "github.com/komari-monitor/komari-agent/monitoring/unit"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
	"github.com/komari-monitor/komari-agent/update"
	"log"
	"sync"
	"time"
)

var flags = pkg_flags.GlobalConfig
var basicInfoMu sync.Mutex

func DoUploadBasicInfoWorks() {
	ticker := time.NewTicker(time.Duration(max(1, flags.InfoReportInterval)) * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		err := uploadBasicInfo()
		if err != nil {
			log.Println("Error uploading basic info:", err)
		}
	}
}
func UpdateBasicInfo() {
	err := uploadBasicInfo()
	if err != nil {
		log.Println("Error uploading basic info:", err)
	} else {
		log.Println("Basic info uploaded successfully")
	}
}
func uploadBasicInfo() error {
	if !basicInfoMu.TryLock() {
		return nil
	}
	defer basicInfoMu.Unlock()
	cpu := monitoring.CpuStaticInfo()

	osname := monitoring.OSName()
	kernelVersion := monitoring.KernelVersion()
	ipv4, ipv6, _ := monitoring.GetIPAddress()

	data := map[string]interface{}{
		"cpu_name":           cpu.CPUName,
		"cpu_cores":          cpu.CPUCores,
		"cpu_physical_cores": cpu.CPUPhysicalCores,
		"arch":               cpu.CPUArchitecture,
		"os":                 osname,
		"kernel_version":     kernelVersion,
		"ipv4":               ipv4,
		"ipv6":               ipv6,
		"mem_total":          monitoring.Ram().Total,
		"swap_total":         monitoring.Swap().Total,
		"disk_total":         monitoring.Disk().Total,
		"gpu_name":           monitoring.GpuName(),
		"virtualization":     monitoring.Virtualized(),
		"version":            update.CurrentVersion,
	}

	return tryUploadData(data)
}

func tryUploadData(data map[string]interface{}) error {
	_, err := postV2Request(v2.BuildBasicInfoPayload(data))
	return err
}
