package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/komari-monitor/komari-agent/dnsresolver"
	"github.com/komari-monitor/komari-agent/utils"
)

// AutoDiscoveryConfig 自动发现配置结构体
type AutoDiscoveryConfig struct {
	UUID  string `json:"uuid"`
	Token string `json:"token"`
}

// RegisterRequest 注册请求结构体
type RegisterRequest struct {
	Key string `json:"key"`
}

// RegisterResponse 注册响应结构体
type RegisterResponse struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    struct {
		UUID  string `json:"uuid"`
		Token string `json:"token"`
	} `json:"data"`
}

// getAutoDiscoveryFilePath 获取自动发现配置文件路径
func getAutoDiscoveryFilePath() string {
	// 获取程序运行目录
	execPath, err := os.Executable()
	if err != nil {
		log.Println("Failed to get executable path:", err)
		return "auto-discovery.json"
	}
	execDir := filepath.Dir(execPath)
	return filepath.Join(execDir, "auto-discovery.json")
}

// loadAutoDiscoveryConfig 加载自动发现配置
func loadAutoDiscoveryConfig() (*AutoDiscoveryConfig, error) {
	return readAutoDiscoveryConfig(getAutoDiscoveryFilePath())
}

func readAutoDiscoveryConfig(configPath string) (*AutoDiscoveryConfig, error) {
	file, err := os.Open(configPath)
	if os.IsNotExist(err) {
		return nil, nil // 文件不存在，返回nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read auto-discovery config: %v", err)
	}
	defer file.Close()
	// Tighten files created by earlier versions before loading credentials.
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect auto-discovery config: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("auto-discovery config must be a regular file")
	}
	if info.Mode().Perm() != 0o600 {
		if err := file.Chmod(0o600); err != nil {
			return nil, fmt.Errorf("failed to secure auto-discovery config: %w", err)
		}
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read auto-discovery config: %w", err)
	}

	// 解析JSON
	var config AutoDiscoveryConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse auto-discovery config: %v", err)
	}

	return &config, nil
}

// saveAutoDiscoveryConfig 保存自动发现配置
func saveAutoDiscoveryConfig(config *AutoDiscoveryConfig) error {
	configPath := getAutoDiscoveryFilePath()
	if err := writeAutoDiscoveryConfig(configPath, config); err != nil {
		return err
	}
	log.Printf("Auto-discovery config saved to: %s", configPath)
	return nil
}

func writeAutoDiscoveryConfig(configPath string, config *AutoDiscoveryConfig) error {
	// 序列化为JSON
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal auto-discovery config: %v", err)
	}

	file, err := os.CreateTemp(filepath.Dir(configPath), ".auto-discovery-*")
	if err != nil {
		return fmt.Errorf("failed to write auto-discovery config: %v", err)
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("failed to secure auto-discovery config: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write auto-discovery config: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("failed to sync auto-discovery config: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close auto-discovery config: %w", err)
	}
	if err := os.Rename(file.Name(), configPath); err != nil {
		return fmt.Errorf("failed to replace auto-discovery config: %w", err)
	}
	return nil
}

// registerWithAutoDiscovery 使用自动发现key注册
func registerWithAutoDiscovery() error {
	// 构造注册请求
	requestData := RegisterRequest{
		Key: flags.AutoDiscoveryKey,
	}

	hostname, _ := os.Hostname()

	jsonData, err := json.Marshal(requestData)
	if err != nil {
		return fmt.Errorf("failed to marshal register request: %v", err)
	}

	// 构造请求URL
	endpoint := flags.Endpoint
	if len(endpoint) > 0 && endpoint[len(endpoint)-1] == '/' {
		endpoint = endpoint[:len(endpoint)-1]
	}

	// 转换中文域名为 ASCII 兼容编码
	endpoint, err = utils.ConvertIDNToASCII(endpoint)
	if err != nil {
		log.Printf("Warning: Failed to convert IDN to ASCII: %v", err)
		// 继续使用原始 endpoint，可能在某些情况下仍能工作
	}

	registerURL := fmt.Sprintf("%s/api/clients/register?name=%s", endpoint, url.QueryEscape(hostname))

	// 创建HTTP请求
	req, err := http.NewRequest("POST", registerURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create register request: %v", utils.SanitizeHTTPError(err))
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", flags.AutoDiscoveryKey))

	// 发送请求
	client := dnsresolver.GetHTTPClientWithPreference(30*time.Second, flags.PreferIPVersion)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send register request: %v", utils.SanitizeHTTPError(err))
	}
	defer resp.Body.Close()

	// 检查响应状态
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("register request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var registerResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&registerResp); err != nil {
		return fmt.Errorf("failed to parse register response: %v", err)
	}

	// 检查响应状态
	if registerResp.Status != "success" {
		return fmt.Errorf("register request failed: %s", registerResp.Message)
	}

	// 保存配置
	config := &AutoDiscoveryConfig{
		UUID:  registerResp.Data.UUID,
		Token: registerResp.Data.Token,
	}

	if err := saveAutoDiscoveryConfig(config); err != nil {
		return fmt.Errorf("failed to save auto-discovery config: %v", err)
	}

	// 设置token
	flags.Token = registerResp.Data.Token
	log.Printf("Successfully registered with auto-discovery. UUID: %s", registerResp.Data.UUID)

	return nil
}

// handleAutoDiscovery 处理自动发现逻辑
func handleAutoDiscovery() error {
	// 尝试加载现有配置
	config, err := loadAutoDiscoveryConfig()
	if err != nil {
		log.Printf("Failed to load auto-discovery config: %v", err)
		// 继续尝试注册
	}

	if config != nil {
		// 配置文件存在，使用现有token
		flags.Token = config.Token
		log.Printf("Using existing auto-discovery token for UUID: %s", config.UUID)
		return nil
	}

	// 配置文件不存在，进行注册
	log.Println("Auto-discovery config not found, registering with server...")
	return registerWithAutoDiscovery()
}
