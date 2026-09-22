package server

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari-agent/dnsresolver"
	monitoring "github.com/komari-monitor/komari-agent/monitoring/unit"
	"github.com/komari-monitor/komari-agent/protocol/transport"
	v2 "github.com/komari-monitor/komari-agent/protocol/v2"
	"github.com/komari-monitor/komari-agent/update"

	pkg_flags "github.com/komari-monitor/komari-agent/cmd/flags"
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
	protocolVersion := uploadProtocolVersion()
	err := tryUploadDataWithProtocol(data, protocolVersion)
	if protocolVersion == 2 && shouldFallbackToV1(err) {
		if err = tryUploadDataWithProtocol(data, 1); err == nil {
			setConnectionProtocolVersion(1)
			log.Println("Basic info uploaded using v1 protocol")
		}
	}
	return err
}

func tryUploadDataWithProtocol(data map[string]interface{}, protocolVersion int) error {
	path := "/api/clients/uploadBasicInfo?token="
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if protocolVersion >= 2 {
		path = "/api/clients/v2/rpc?token="
		payload = v2.BuildBasicInfoPayload(data)
	}
	endpoint := strings.TrimSuffix(flags.Endpoint, "/") + path + flags.Token
	body := payload
	compressed := false
	if protocolVersion >= 2 && !flags.DisableCompression {
		if gz, err := transport.GzipBytes(payload); err == nil {
			body = gz
			compressed = true
		}
	}

	req, err := http.NewRequest("POST", endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	}

	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := readControlResponse(resp.Body)
	if err != nil {
		return err
	}
	message := string(respBody)

	if resp.StatusCode != http.StatusOK {
		// 早期 v1 面板会拒绝后来增加的字段，按旧协议重试一次且不修改原数据。
		if protocolVersion == 1 && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusInternalServerError) {
			legacyData := make(map[string]interface{}, len(data))
			for key, value := range data {
				if key != "kernel_version" && key != "cpu_physical_cores" {
					legacyData[key] = value
				}
			}
			if len(legacyData) != len(data) {
				return tryUploadDataWithProtocol(legacyData, 1)
			}
		}
		return &httpStatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: message}
	}
	if protocolVersion >= 2 && len(bytes.TrimSpace(respBody)) > 0 {
		if _, err := parseV2Response(respBody); err != nil {
			return err
		}
	}

	return nil
}
