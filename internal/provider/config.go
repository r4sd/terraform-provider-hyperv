package provider

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	gowsman "github.com/r4sd/go-wsman/hyperv"
	gowsmanproto "github.com/r4sd/go-wsman/wsman"
	"github.com/taliesins/terraform-provider-hyperv/api"
	hyperv_winrm "github.com/taliesins/terraform-provider-hyperv/api/hyperv-winrm"
	hyperv_wsman "github.com/taliesins/terraform-provider-hyperv/api/hyperv-wsman"

	"github.com/dylanmei/iso8601"
	pool "github.com/jolestar/go-commons-pool/v2"
	winrm "github.com/masterzen/winrm"
	winrm_helper "github.com/taliesins/terraform-provider-hyperv/api/winrm-helper"
)

// useWsmanEnabled は go-wsman 経由のクライアントを使うかを判定する。
//
// Phase A 段階では環境変数 HYPERV_USE_WSMAN=1 のみで切替。Phase E 直前に
// Provider schema へ昇格するか、デフォルト切替するかを判断する。
func useWsmanEnabled() bool {
	v := os.Getenv("HYPERV_USE_WSMAN")
	return v == "1" || strings.EqualFold(v, "true")
}

// strictNoPSEnabled は strict モード (PS 呼び出し 0 件検証) の有効/無効を返す。
// HYPERV_WSMAN_STRICT=1 かつ HYPERV_USE_WSMAN 有効時にのみ意味を持つ (呼び出し側で and 判定)。
func strictNoPSEnabled() bool {
	v := os.Getenv("HYPERV_WSMAN_STRICT")
	return v == "1" || strings.EqualFold(v, "true")
}

type Config struct {
	Version          string
	Commit           string
	TerraformVersion string
	User             string
	Password         string
	Host             string
	Port             int
	HTTPS            bool
	Insecure         bool

	KrbRealm  string
	KrbSpn    string
	KrbConfig string
	KrbCCache string

	NTLM bool

	TLSServerName string
	CACert        []byte
	Cert          []byte
	Key           []byte

	ScriptPath string
	Timeout    string
}

// HypervWinRmClient() returns a new client for configuring hyperv.
func (c *Config) Client() (comm api.Client, err error) {
	log.Printf("[INFO][hyperv] HyperV HypervWinRmClient configured for HyperV API operations using:\n"+
		"  Host: %s\n"+
		"  Port: %d\n"+
		"  User: %s\n"+
		"  Password: %t\n"+
		"  HTTPS: %t\n"+
		"  Insecure: %t\n"+
		"  NTLM: %t\n"+
		"  KrbRealm: %s\n"+
		"  KrbSpn: %s\n"+
		"  KrbConfig: %s\n"+
		"  KrbCCache: %s\n"+
		"  TLSServerName: %s\n"+
		"  CACert: %t\n"+
		"  Cert: %t\n"+
		"  Key: %t\n"+
		"  ScriptPath: %s\n"+
		"  Timeout: %s",
		c.Host,
		c.Port,
		c.User,
		c.Password != "",
		c.HTTPS,
		c.Insecure,
		c.NTLM,
		c.KrbRealm,
		c.KrbSpn,
		c.KrbConfig,
		c.KrbCCache,
		c.TLSServerName,
		c.CACert != nil,
		c.Cert != nil,
		c.Key != nil,
		c.ScriptPath,
		c.Timeout,
	)

	hyperVProvider, err := getHypervProvider(c)

	if err != nil {
		return nil, err
	}

	return hyperVProvider.Client, nil
}

// New creates a new communicator implementation over WinRM.
func GetWinrmClient(config *Config) (winrmClient *winrm.Client, err error) {
	addr := fmt.Sprintf("%s:%d", config.Host, config.Port)
	endpoint, err := parseEndpoint(addr, config.HTTPS, config.Insecure, config.TLSServerName, config.CACert, config.Cert, config.Key, config.Timeout)
	if err != nil {
		return nil, err
	}

	params := winrm.DefaultParameters

	if config.KrbRealm != "" {
		proto := "http"
		if config.HTTPS {
			proto = "https"
		}

		params.TransportDecorator = func() winrm.Transporter {
			return &winrm.ClientKerberos{
				Username:  config.User,
				Password:  config.Password,
				Hostname:  config.Host,
				Port:      config.Port,
				Proto:     proto,
				Realm:     config.KrbRealm,
				SPN:       config.KrbSpn,
				KrbConf:   config.KrbConfig,
				KrbCCache: config.KrbCCache,
			}
		}
	} else if config.NTLM {
		params.TransportDecorator = func() winrm.Transporter { return &winrm.ClientNTLM{} }
	}

	if endpoint.Timeout.Seconds() > 0 {
		params.Timeout = iso8601.FormatDuration(endpoint.Timeout)
	}

	winrmClient, err = winrm.NewClientWithParameters(
		endpoint, config.User, config.Password, params)

	if err != nil {
		return nil, err
	}

	return winrmClient, nil
}

func parseEndpoint(addr string, https bool, insecure bool, tlsServerName string, caCert []byte, cert []byte, key []byte, timeout string) (*winrm.Endpoint, error) {
	var host string
	var port int

	if addr == "" {
		return nil, fmt.Errorf("couldn't convert \"\" to an address")
	}
	if !strings.Contains(addr, ":") || (strings.HasPrefix(addr, "[") && strings.HasSuffix(addr, "]")) {
		host = addr
		port = 5985
	} else {
		shost, sport, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("couldn't convert \"%s\" to an address", addr)
		}
		// Check for IPv6 addresses and reformat appropriately
		host = ipFormat(shost)
		port, err = strconv.Atoi(sport)
		if err != nil {
			return nil, fmt.Errorf("couldn't convert \"%s\" to a port number", sport)
		}
	}

	timeoutDuration, err := time.ParseDuration(timeout)
	if err != nil {
		return nil, fmt.Errorf("couldn't convert \"%s\" to a duration", timeout)
	}

	return &winrm.Endpoint{
		Host:          host,
		Port:          port,
		HTTPS:         https,
		Insecure:      insecure,
		TLSServerName: tlsServerName,
		Cert:          cert,
		Key:           key,
		CACert:        caCert,
		Timeout:       timeoutDuration,
	}, nil
}

// ipFormat formats the IP correctly, so we don't provide IPv6 address in an IPv4 format during node communication.
// We return the ip parameter as is if it's an IPv4 address or a hostname.
func ipFormat(ip string) string {
	ipObj := net.ParseIP(ip)
	// Return the ip/host as is if it's either a hostname or an IPv4 address.
	if ipObj == nil || ipObj.To4() != nil {
		return ip
	}

	return fmt.Sprintf("[%s]", ip)
}

func getHypervProvider(config *Config) (hypervProvider *api.Provider, err error) {
	ctx := context.Background()
	factory := pool.NewPooledObjectFactorySimple(
		func(context.Context) (interface{}, error) {
			winrmClient, err := GetWinrmClient(config)

			if err != nil {
				return nil, err
			}

			return winrmClient, nil
		})

	winRmClientPool := pool.NewObjectPoolWithDefaultConfig(ctx, factory)
	winRmClientPool.Config.BlockWhenExhausted = true
	winRmClientPool.Config.MinIdle = 0
	winRmClientPool.Config.MaxIdle = 2
	winRmClientPool.Config.MaxTotal = 5
	winRmClientPool.Config.TimeBetweenEvictionRuns = 10 * time.Second

	winrmHelperProvider, err := winrm_helper.New(&winrm_helper.ClientConfig{
		WinRmClientPool:  winRmClientPool,
		Vars:             "",
		ElevatedUser:     config.User,
		ElevatedPassword: config.Password,
	})

	if err != nil {
		return nil, err
	}

	winrmConfig := &hyperv_winrm.ClientConfig{
		WinRmClient: winrmHelperProvider.Client,
	}

	// feature flag: HYPERV_USE_WSMAN=1 で go-wsman 経由のクライアントに切替。
	// 未移行メソッドは winrmConfig 経由で従来の PowerShell 実装にフォールバックする。
	if useWsmanEnabled() {
		log.Printf("[INFO][hyperv] HYPERV_USE_WSMAN enabled. Using go-wsman client for migrated resources.")

		// strict モード: HYPERV_WSMAN_STRICT=1 で PS フォールバックを fail-fast スタブに差し替える。
		// go-wsman シャドウ未実装の経路が 1 件でも呼ばれたら即エラーにし、v2.0「Home-env PS-free」の
		// 陽性証明(homelab が使う経路の PS 呼び出し 0 件)を検証できるようにする。
		//
		// 現状で PS-0 になる範囲: Gen1 VM かつ全 network_adapter が wait_for_ips=false の refresh/plan
		// (vm_processor/integration_services の create/update 書き込みは差分なしガードで既定 config
		// なら PS-0、#88/#95)。Gen2 の firmware read はスカラーフィールドはシャドウ済みだが、
		// BootSourceOrder が非空 (ブート可能デバイスが付いている VM) なら PS へ委譲する (#96、
		// BootSourceOrder→デバイス相関ロジックは未実装のため)。
		// PS フォールバックが残る経路(strict でエラーになる): 上記 BootSourceOrder 非空の firmware
		// read、wait_for_ips=true(WaitForVmNetworkAdaptersIps が PS 委譲、#76)、vm_firmware の
		// create/update 書き込み(write は未着手)。
		if strictNoPSEnabled() {
			log.Printf("[WARN][hyperv] HYPERV_WSMAN_STRICT enabled. PowerShell フォールバックは全て fail-fast エラーになります。")
			winrmConfig.WinRmClient = &hyperv_wsman.StrictNoPSClient{}
		}

		wsmanClient, err := newWsmanClient(config)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize go-wsman client: %w", err)
		}
		return hyperv_wsman.New(winrmConfig, wsmanClient)
	}

	return hyperv_winrm.New(winrmConfig)
}

// newWsmanClient は config から go-wsman の hyperv.Client を構築する。
//
// 認証: 現行の Provider 設定 (NTLM / Basic / Cert) のうち、Phase A では NTLM のみサポート。
// Kerberos / Cert は Phase B 以降で必要になったタイミングで対応する。
//
// TLS 証明書検証:
//   - provider 設定 insecure=false (デフォルト): 証明書検証あり
//   - provider 設定 insecure=true: WithInsecureSkipVerify() で検証スキップ
//     (homelab のように自己署名証明書を使う環境向け)
func newWsmanClient(config *Config) (*gowsman.Client, error) {
	scheme := "http"
	if config.HTTPS {
		scheme = "https"
	}
	endpoint := fmt.Sprintf("%s://%s:%d/wsman", scheme, config.Host, config.Port)

	opts := []gowsmanproto.ClientOption{}
	if config.NTLM {
		opts = append(opts, gowsmanproto.WithNTLM(config.User, config.Password))
	}
	if config.Insecure {
		opts = append(opts, gowsmanproto.WithInsecureSkipVerify())
	}

	return gowsman.NewClient(endpoint, opts...)
}
