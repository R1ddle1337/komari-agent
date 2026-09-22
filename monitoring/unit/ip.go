package monitoring

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/komari-monitor/komari-agent/dnsresolver"
)

var (
	// 创建适用于IPv4和IPv6的HTTP客户端
	ipv4HTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialer := dnsresolver.GetNetDialer(15 * time.Second)
				return dialer.DialContext(ctx, "tcp4", addr) // 锁v4防止出现问题
			},
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 15 * time.Second,
	}
	ipv6HTTPClient = &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				dialer := dnsresolver.GetNetDialer(15 * time.Second)
				return dialer.DialContext(ctx, "tcp6", addr) // 锁v6防止出现问题
			},
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: 15 * time.Second,
	}
	userAgent = "curl/8.0.1"
)

const publicIPTimeout = 12 * time.Second
const publicIPSourceTimeout = 4 * time.Second
const maxPublicIPResponseBytes = 64 << 10

func GetIPv4Address() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), publicIPTimeout)
	defer cancel()
	return lookupPublicIP(ctx, ipv4HTTPClient, []string{
		"https://www.visa.cn/cdn-cgi/trace",
		"https://www.qualcomm.cn/cdn-cgi/trace",
		"https://www.toutiao.com/stream/widget/local_weather/data/",
		"https://edge-ip.html.zone/geo",
		"https://vercel-ip.html.zone/geo",
		"https://ipv4.ip.sb",
		"https://api.ipify.org?format=json",
	}, true)
}

func GetIPv6Address() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), publicIPTimeout)
	defer cancel()
	return lookupPublicIP(ctx, ipv6HTTPClient, []string{
		"https://v6.ip.zxinc.org/info.php?type=json",
		"https://api6.ipify.org?format=json",
		"https://ipv6.icanhazip.com",
		"https://api-ipv6.ip.sb/geoip",
	}, false)
}

func lookupPublicIP(ctx context.Context, client *http.Client, sources []string, ipv4 bool) (string, error) {
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		requestCtx, cancel := context.WithTimeout(ctx, publicIPSourceTimeout)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, source, nil)
		if err != nil {
			cancel()
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		response, err := client.Do(req)
		if err != nil {
			cancel()
			continue
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxPublicIPResponseBytes+1))
		_ = response.Body.Close()
		cancel()
		if err != nil || response.StatusCode != http.StatusOK || len(body) > maxPublicIPResponseBytes {
			continue
		}
		if ip := parsePublicIP(string(body), ipv4); ip != "" {
			return ip, nil
		}
	}
	return "", ctx.Err()
}

func parsePublicIP(text string, ipv4 bool) string {
	candidates := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F') || r == ':' || r == '.')
	})
	for _, candidate := range candidates {
		parsed := net.ParseIP(candidate)
		if parsed != nil && (parsed.To4() != nil) == ipv4 && parsed.IsGlobalUnicast() && !parsed.IsPrivate() {
			return parsed.String()
		}
	}
	return ""
}

func GetIPAddress() (ipv4, ipv6 string, err error) {

	if flags.GetIpAddrFromNic {
		allowNics, err := InterfaceList()
		if err != nil {
			log.Printf("Get Interface List Error: %v", err)
		} else {
			ipv4, ipv6 = getIPFromInterfaces(allowNics)
			if ipv4 != "" || ipv6 != "" {
				log.Printf("Get IP from NIC - IPv4: %s, IPv6: %s", ipv4, ipv6)
				return ipv4, ipv6, nil
			}
		}
	}

	// Families are independent. A missing IPv6 route must not delay IPv4
	// metadata by another full chain of remote provider timeouts.
	var lookups sync.WaitGroup
	ipv4, ipv6 = flags.CustomIpv4, flags.CustomIpv6
	if ipv4 == "" {
		lookups.Add(1)
		go func() { defer lookups.Done(); ipv4, _ = GetIPv4Address() }()
	}
	if ipv6 == "" {
		lookups.Add(1)
		go func() { defer lookups.Done(); ipv6, _ = GetIPv6Address() }()
	}
	lookups.Wait()

	return ipv4, ipv6, nil
}

// getIPFromInterfaces 从指定的网卡接口获取 IPv4 和 IPv6 地址
func getIPFromInterfaces(nicNames []string) (ipv4, ipv6 string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		log.Printf("Failed to get network interfaces: %v", err)
		return "", ""
	}
	for _, iface := range interfaces {
		// 检查接口是否在允许列表中
		if !func(slice []string, item string) bool {
			for _, s := range slice {
				if s == item {
					return true
				}
			}
			return false
		}(nicNames, iface.Name) {
			continue
		}

		// 跳过未启动的接口
		if iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			// 获取 IPv4 地址
			if ipv4 == "" && ip.To4() != nil {
				ipv4 = ip.String()
			}

			// 获取 IPv6 地址（排除链路本地地址）
			if ipv6 == "" && ip.To4() == nil && !ip.IsLinkLocalUnicast() {
				ipv6 = ip.String()
			}

			// 如果已经找到 IPv4 和 IPv6,提前返回
			if ipv4 != "" && ipv6 != "" {
				return ipv4, ipv6
			}
		}
	}

	return ipv4, ipv6
}
