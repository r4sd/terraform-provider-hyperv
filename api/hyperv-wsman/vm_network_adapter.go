package hyperv_wsman

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// defaultVmNetworkAdapter は network_adaptors スキーマの既定値を持つ VmNetworkAdapter を返す。
//
// go-wsman 経路が扱わないフィールドは、Get 結果でこの既定値を返して terraform の差分を防ぎ、
// また Create/Update ではこの既定値以外が指定されたら未対応として拒否する基準に使う。
func defaultVmNetworkAdapter() api.VmNetworkAdapter {
	return api.VmNetworkAdapter{
		ManagementOs:                           false,
		IsLegacy:                               false,
		DynamicMacAddress:                      true,
		StaticMacAddress:                       "",
		MacAddressSpoofing:                     api.OnOffState_Off,
		DhcpGuard:                              api.OnOffState_Off,
		RouterGuard:                            api.OnOffState_Off,
		PortMirroring:                          api.PortMirroring_None,
		IeeePriorityTag:                        api.OnOffState_Off,
		VmqWeight:                              100,
		IovQueuePairsRequested:                 1,
		IovInterruptModeration:                 api.IovInterruptModerationValue_Off,
		IovWeight:                              100,
		IpsecOffloadMaximumSecurityAssociation: 512,
		MaximumBandwidth:                       0,
		MinimumBandwidthAbsolute:               0,
		MinimumBandwidthWeight:                 0,
		ResourcePoolName:                       "",
		TestReplicaPoolName:                    "",
		TestReplicaSwitchName:                  "",
		VirtualSubnetId:                        0,
		AllowTeaming:                           api.OnOffState_On,
		NotMonitoredInCluster:                  false,
		StormLimit:                             0,
		DynamicIpAddressLimit:                  0,
		DeviceNaming:                           api.OnOffState_Off,
		FixSpeed10G:                            api.OnOffState_Off,
		PacketDirectNumProcs:                   0,
		PacketDirectModerationCount:            0,
		PacketDirectModerationInterval:         0,
		VrssEnabled:                            true,
		VmmqEnabled:                            false,
		VmmqQueuePairs:                         16,
		VlanAccess:                             false,
		VlanId:                                 0,
		WaitForIps:                             true,
	}
}

// unsupportedNetworkAdapterOptions は go-wsman 経路が未対応の NIC オプションが既定値以外で
// 指定されていれば error を返す。
//
// go-wsman の AddNetworkAdapter は「NIC 本体 + スイッチ接続 + MAC」のみ対応する。QoS・IOV・
// セキュリティ (spoofing/guard)・VLAN・帯域・チーミング・PacketDirect 等はまだ扱えないため、
// silent drop を避けて明示的に拒否する (rules 準拠、これらは v2.1 以降)。
func unsupportedNetworkAdapterOptions(a api.VmNetworkAdapter) error {
	def := defaultVmNetworkAdapter()
	var unsupported []string
	add := func(cond bool, name string) {
		if cond {
			unsupported = append(unsupported, name)
		}
	}
	add(a.ManagementOs != def.ManagementOs, "management_os")
	add(a.IsLegacy != def.IsLegacy, "is_legacy")
	add(a.MacAddressSpoofing != def.MacAddressSpoofing, "mac_address_spoofing")
	add(a.DhcpGuard != def.DhcpGuard, "dhcp_guard")
	add(a.RouterGuard != def.RouterGuard, "router_guard")
	add(a.PortMirroring != def.PortMirroring, "port_mirroring")
	add(a.IeeePriorityTag != def.IeeePriorityTag, "ieee_priority_tag")
	add(a.VmqWeight != def.VmqWeight, "vmq_weight")
	add(a.IovQueuePairsRequested != def.IovQueuePairsRequested, "iov_queue_pairs_requested")
	add(a.IovInterruptModeration != def.IovInterruptModeration, "iov_interrupt_moderation")
	add(a.IovWeight != def.IovWeight, "iov_weight")
	add(a.IpsecOffloadMaximumSecurityAssociation != def.IpsecOffloadMaximumSecurityAssociation, "ipsec_offload_maximum_security_association")
	add(a.MaximumBandwidth != def.MaximumBandwidth, "maximum_bandwidth")
	add(a.MinimumBandwidthAbsolute != def.MinimumBandwidthAbsolute, "minimum_bandwidth_absolute")
	add(a.MinimumBandwidthWeight != def.MinimumBandwidthWeight, "minimum_bandwidth_weight")
	add(len(a.MandatoryFeatureId) > 0, "mandatory_feature_id")
	add(a.ResourcePoolName != def.ResourcePoolName, "resource_pool_name")
	add(a.TestReplicaPoolName != def.TestReplicaPoolName, "test_replica_pool_name")
	add(a.TestReplicaSwitchName != def.TestReplicaSwitchName, "test_replica_switch_name")
	add(a.VirtualSubnetId != def.VirtualSubnetId, "virtual_subnet_id")
	add(a.AllowTeaming != def.AllowTeaming, "allow_teaming")
	add(a.NotMonitoredInCluster != def.NotMonitoredInCluster, "not_monitored_in_cluster")
	add(a.StormLimit != def.StormLimit, "storm_limit")
	add(a.DynamicIpAddressLimit != def.DynamicIpAddressLimit, "dynamic_ip_address_limit")
	add(a.DeviceNaming != def.DeviceNaming, "device_naming")
	add(a.FixSpeed10G != def.FixSpeed10G, "fix_speed_10g")
	add(a.PacketDirectNumProcs != def.PacketDirectNumProcs, "packet_direct_num_procs")
	add(a.PacketDirectModerationCount != def.PacketDirectModerationCount, "packet_direct_moderation_count")
	add(a.PacketDirectModerationInterval != def.PacketDirectModerationInterval, "packet_direct_moderation_interval")
	add(a.VrssEnabled != def.VrssEnabled, "vrss_enabled")
	add(a.VmmqEnabled != def.VmmqEnabled, "vmmq_enabled")
	add(a.VmmqQueuePairs != def.VmmqQueuePairs, "vmmq_queue_pairs")
	add(a.VlanAccess != def.VlanAccess, "vlan_access")
	add(a.VlanId != def.VlanId, "vlan_id")

	if len(unsupported) > 0 {
		return fmt.Errorf(
			"hyperv-wsman: network_adaptor のオプション %s は go-wsman 経路 (HYPERV_USE_WSMAN) では未対応です。"+
				"PowerShell 経路 (HYPERV_USE_WSMAN 未設定) を使うか、これらを既定値にしてください",
			strings.Join(unsupported, ", "))
	}
	return nil
}

// CreateVmNetworkAdapter は go-wsman 経由で NIC を追加し、指定スイッチに接続する。
func (c *ClientConfig) CreateVmNetworkAdapter(
	ctx context.Context, vmName, name, switchName string, managementOs, isLegacy, dynamicMacAddress bool,
	staticMacAddress string, macAddressSpoofing, dhcpGuard, routerGuard api.OnOffState, portMirroring api.PortMirroring,
	ieeePriorityTag api.OnOffState, vmqWeight, iovQueuePairsRequested int, iovInterruptModeration api.IovInterruptModerationValue,
	iovWeight, ipsecOffloadMaximumSecurityAssociation, maximumBandwidth, minimumBandwidthAbsolute, minimumBandwidthWeight int,
	mandatoryFeatureId []string, resourcePoolName, testReplicaPoolName, testReplicaSwitchName string, virtualSubnetId int,
	allowTeaming api.OnOffState, notMonitoredInCluster bool, stormLimit, dynamicIpAddressLimit int,
	deviceNaming, fixSpeed10G api.OnOffState, packetDirectNumProcs, packetDirectModerationCount, packetDirectModerationInterval int,
	vrssEnabled, vmmqEnabled bool, vmmqQueuePairs int, vlanAccess bool, vlanId int,
) error {
	a := networkAdapterFromParams(vmName, name, switchName, managementOs, isLegacy, dynamicMacAddress, staticMacAddress,
		macAddressSpoofing, dhcpGuard, routerGuard, portMirroring, ieeePriorityTag, vmqWeight, iovQueuePairsRequested,
		iovInterruptModeration, iovWeight, ipsecOffloadMaximumSecurityAssociation, maximumBandwidth, minimumBandwidthAbsolute,
		minimumBandwidthWeight, mandatoryFeatureId, resourcePoolName, testReplicaPoolName, testReplicaSwitchName, virtualSubnetId,
		allowTeaming, notMonitoredInCluster, stormLimit, dynamicIpAddressLimit, deviceNaming, fixSpeed10G, packetDirectNumProcs,
		packetDirectModerationCount, packetDirectModerationInterval, vrssEnabled, vmmqEnabled, vmmqQueuePairs, vlanAccess, vlanId)
	return c.createNetworkAdapter(ctx, a)
}

// createNetworkAdapter は VmNetworkAdapter 構造体から NIC を追加する共通実装。
func (c *ClientConfig) createNetworkAdapter(ctx context.Context, a api.VmNetworkAdapter) error {
	if err := unsupportedNetworkAdapterOptions(a); err != nil {
		return err
	}
	opts := hyperv.NetworkAdapterOptions{
		ElementName: a.Name,
		SwitchName:  a.SwitchName,
	}
	// 静的 MAC 指定時は MAC 必須 (silent drop 禁止, M2)。区切り文字は正規化して送る。
	if !a.DynamicMacAddress {
		if a.StaticMacAddress == "" {
			return fmt.Errorf("hyperv-wsman: CreateVmNetworkAdapter %q: dynamic_mac_address=false のとき static_mac_address は必須です", a.Name)
		}
		opts.StaticMacAddress = true
		opts.MacAddress = normalizeMac(a.StaticMacAddress)
	}
	guid, err := c.resolveVMGUID(ctx, a.VmName)
	if err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmNetworkAdapter %q: %w", a.VmName, err)
	}
	if _, err := c.WsmanClient.AddNetworkAdapter(ctx, guid, opts); err != nil {
		return fmt.Errorf("hyperv-wsman: CreateVmNetworkAdapter %q: %w", a.VmName, err)
	}
	return nil
}

// networkAdapterRef は 1 つの NIC と、その削除に必要な Port InstanceID を束ねる。
type networkAdapterRef struct {
	adapter        api.VmNetworkAdapter
	portInstanceID string // Msvm_SyntheticEthernetPortSettingData の InstanceID
}

// getNetworkAdapterRefs は VM の NIC 一覧を go-wsman の逆引きで組み立てる (順序安定)。
//
// port(NIC本体: 名前・MAC) → allocation(接続先スイッチ EPR) → switch(表示名) の結合で
// VmNetworkAdapter を復元する。削除用に Port の InstanceID も保持する。
func (c *ClientConfig) getNetworkAdapterRefs(ctx context.Context, vmName string) ([]networkAdapterRef, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: get network adapters %q: %w", vmName, err)
	}
	ports, err := c.WsmanClient.ListNetworkAdapters(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list network adapters %q: %w", vmName, err)
	}
	allocs, err := c.WsmanClient.ListEthernetPortAllocations(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list port allocations %q: %w", vmName, err)
	}
	switches, err := c.WsmanClient.ListVirtualEthernetSwitches(ctx)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: list switches: %w", err)
	}
	return mapNetworkAdapterRefs(vmName, ports, allocs, switches), nil
}

// GetVmNetworkAdapters は VM の NIC 一覧を返す (go-wsman 逆引き)。
//
// networkAdaptersWaitForIps の wait_for_ips は名前突合で結果に反映する (winrm 版と同じ、M1)。
// 実際のゲスト IP 取得 (KVP/統合サービス依存) は本経路では未対応で、IpAddresses は空で返す。
func (c *ClientConfig) GetVmNetworkAdapters(ctx context.Context, vmName string, networkAdaptersWaitForIps []api.VmNetworkAdapterWaitForIp) ([]api.VmNetworkAdapter, error) {
	refs, err := c.getNetworkAdapterRefs(ctx, vmName)
	if err != nil {
		return nil, err
	}
	result := make([]api.VmNetworkAdapter, 0, len(refs))
	for _, r := range refs {
		result = append(result, r.adapter)
	}
	// config の wait_for_ips を名前突合で反映 (未設定 NIC は既定 true のまま)。
	for _, w := range networkAdaptersWaitForIps {
		for i := range result {
			if result[i].Name == w.Name {
				result[i].WaitForIps = w.WaitForIps
			}
		}
	}
	return result, nil
}

// DeleteVmNetworkAdapter は index 番目の NIC を go-wsman 経由で削除する。
func (c *ClientConfig) DeleteVmNetworkAdapter(ctx context.Context, vmName string, index int) error {
	refs, err := c.getNetworkAdapterRefs(ctx, vmName)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(refs) {
		return fmt.Errorf("hyperv-wsman: DeleteVmNetworkAdapter %q: index %d out of range (VM has %d NICs)", vmName, index, len(refs))
	}
	if _, err := c.WsmanClient.RemoveNetworkAdapter(ctx, refs[index].portInstanceID); err != nil {
		return fmt.Errorf("hyperv-wsman: DeleteVmNetworkAdapter %q: %w", vmName, err)
	}
	return nil
}

// UpdateVmNetworkAdapter は index 番目の NIC を所望の状態に置き換える (detach+attach)。
func (c *ClientConfig) UpdateVmNetworkAdapter(
	ctx context.Context, vmName string, index int, name, switchName string, managementOs, isLegacy, dynamicMacAddress bool,
	staticMacAddress string, macAddressSpoofing, dhcpGuard, routerGuard api.OnOffState, portMirroring api.PortMirroring,
	ieeePriorityTag api.OnOffState, vmqWeight, iovQueuePairsRequested int, iovInterruptModeration api.IovInterruptModerationValue,
	iovWeight, ipsecOffloadMaximumSecurityAssociation, maximumBandwidth, minimumBandwidthAbsolute, minimumBandwidthWeight int,
	mandatoryFeatureId []string, resourcePoolName, testReplicaPoolName, testReplicaSwitchName string, virtualSubnetId int,
	allowTeaming api.OnOffState, notMonitoredInCluster bool, stormLimit, dynamicIpAddressLimit int,
	deviceNaming, fixSpeed10G api.OnOffState, packetDirectNumProcs, packetDirectModerationCount, packetDirectModerationInterval int,
	vrssEnabled, vmmqEnabled bool, vmmqQueuePairs int, vlanAccess bool, vlanId int,
) error {
	a := networkAdapterFromParams(vmName, name, switchName, managementOs, isLegacy, dynamicMacAddress, staticMacAddress,
		macAddressSpoofing, dhcpGuard, routerGuard, portMirroring, ieeePriorityTag, vmqWeight, iovQueuePairsRequested,
		iovInterruptModeration, iovWeight, ipsecOffloadMaximumSecurityAssociation, maximumBandwidth, minimumBandwidthAbsolute,
		minimumBandwidthWeight, mandatoryFeatureId, resourcePoolName, testReplicaPoolName, testReplicaSwitchName, virtualSubnetId,
		allowTeaming, notMonitoredInCluster, stormLimit, dynamicIpAddressLimit, deviceNaming, fixSpeed10G, packetDirectNumProcs,
		packetDirectModerationCount, packetDirectModerationInterval, vrssEnabled, vmmqEnabled, vmmqQueuePairs, vlanAccess, vlanId)
	if err := unsupportedNetworkAdapterOptions(a); err != nil {
		return err
	}
	if err := c.DeleteVmNetworkAdapter(ctx, vmName, index); err != nil {
		return err
	}
	return c.createNetworkAdapter(ctx, a)
}

// CreateOrUpdateVmNetworkAdapters は所望の NIC 集合に収束させる (集合差分、冪等)。
func (c *ClientConfig) CreateOrUpdateVmNetworkAdapters(ctx context.Context, vmName string, networkAdapters []api.VmNetworkAdapter) error {
	for _, a := range networkAdapters {
		if err := unsupportedNetworkAdapterOptions(a); err != nil {
			return err
		}
	}
	current, err := c.getNetworkAdapterRefs(ctx, vmName)
	if err != nil {
		return err
	}
	toRemove, toAdd := planNetworkAdapterReconcile(current, networkAdapters)
	for _, portInstanceID := range toRemove {
		if _, err := c.WsmanClient.RemoveNetworkAdapter(ctx, portInstanceID); err != nil {
			return fmt.Errorf("hyperv-wsman: CreateOrUpdateVmNetworkAdapters %q: remove: %w", vmName, err)
		}
	}
	for _, a := range toAdd {
		a.VmName = vmName
		if err := c.createNetworkAdapter(ctx, a); err != nil {
			return err
		}
	}
	return nil
}

// WaitForVmNetworkAdaptersIps は全 NIC が wait_for_ips=false のとき PowerShell 呼び出しを省く。
//
// go-wsman はゲスト IP 取得 (KVP/Msvm_GuestNetworkAdapterConfiguration) を持たないため、実際に
// IP 待ちが要る場合のみ埋め込んだ winrm 実装 (PS) に委譲する。1 つでも wait_for_ips=true があれば
// 従来どおり PS で待機し、全て false なら PS を出さず即 return して strict mode (PS 0 件) を満たす。
// winrm 実装はリストが非空なら中身が false でも PS を流すため、homelab (wait_for_ips=false×2 の
// 非空リスト) を「空リスト時スキップ」ではカバーできない。「全 false ならスキップ」が正。
func (c *ClientConfig) WaitForVmNetworkAdaptersIps(
	ctx context.Context,
	vmName string,
	timeout uint32,
	pollPeriod uint32,
	vmNetworkAdaptersWaitForIps []api.VmNetworkAdapterWaitForIp,
) error {
	for _, w := range vmNetworkAdaptersWaitForIps {
		if w.WaitForIps {
			return c.ClientConfig.WaitForVmNetworkAdaptersIps(ctx, vmName, timeout, pollPeriod, vmNetworkAdaptersWaitForIps)
		}
	}
	return nil
}

// --- 純関数 (table-driven test 対象) ---

// networkAdapterFromParams は Create/Update の引数列を VmNetworkAdapter に詰める。
func networkAdapterFromParams(
	vmName, name, switchName string, managementOs, isLegacy, dynamicMacAddress bool, staticMacAddress string,
	macAddressSpoofing, dhcpGuard, routerGuard api.OnOffState, portMirroring api.PortMirroring, ieeePriorityTag api.OnOffState,
	vmqWeight, iovQueuePairsRequested int, iovInterruptModeration api.IovInterruptModerationValue,
	iovWeight, ipsecOffloadMaximumSecurityAssociation, maximumBandwidth, minimumBandwidthAbsolute, minimumBandwidthWeight int,
	mandatoryFeatureId []string, resourcePoolName, testReplicaPoolName, testReplicaSwitchName string, virtualSubnetId int,
	allowTeaming api.OnOffState, notMonitoredInCluster bool, stormLimit, dynamicIpAddressLimit int,
	deviceNaming, fixSpeed10G api.OnOffState, packetDirectNumProcs, packetDirectModerationCount, packetDirectModerationInterval int,
	vrssEnabled, vmmqEnabled bool, vmmqQueuePairs int, vlanAccess bool, vlanId int,
) api.VmNetworkAdapter {
	return api.VmNetworkAdapter{
		VmName: vmName, Name: name, SwitchName: switchName, ManagementOs: managementOs, IsLegacy: isLegacy,
		DynamicMacAddress: dynamicMacAddress, StaticMacAddress: staticMacAddress, MacAddressSpoofing: macAddressSpoofing,
		DhcpGuard: dhcpGuard, RouterGuard: routerGuard, PortMirroring: portMirroring, IeeePriorityTag: ieeePriorityTag,
		VmqWeight: vmqWeight, IovQueuePairsRequested: iovQueuePairsRequested, IovInterruptModeration: iovInterruptModeration,
		IovWeight: iovWeight, IpsecOffloadMaximumSecurityAssociation: ipsecOffloadMaximumSecurityAssociation,
		MaximumBandwidth: maximumBandwidth, MinimumBandwidthAbsolute: minimumBandwidthAbsolute, MinimumBandwidthWeight: minimumBandwidthWeight,
		MandatoryFeatureId: mandatoryFeatureId, ResourcePoolName: resourcePoolName, TestReplicaPoolName: testReplicaPoolName,
		TestReplicaSwitchName: testReplicaSwitchName, VirtualSubnetId: virtualSubnetId, AllowTeaming: allowTeaming,
		NotMonitoredInCluster: notMonitoredInCluster, StormLimit: stormLimit, DynamicIpAddressLimit: dynamicIpAddressLimit,
		DeviceNaming: deviceNaming, FixSpeed10G: fixSpeed10G, PacketDirectNumProcs: packetDirectNumProcs,
		PacketDirectModerationCount: packetDirectModerationCount, PacketDirectModerationInterval: packetDirectModerationInterval,
		VrssEnabled: vrssEnabled, VmmqEnabled: vmmqEnabled, VmmqQueuePairs: vmmqQueuePairs, VlanAccess: vlanAccess, VlanId: vlanId,
	}
}

// mapNetworkAdapterRefs は port/allocation/switch を結合して VmNetworkAdapter を復元する。
//
// port.InstanceID を allocation.Parent と突き合わせて接続先スイッチ EPR を得、switch.Name(GUID) を
// EPR から引いて表示名 (ElementName) に解決する。未対応フィールドは既定値で埋め、Index は順序で採番。
//
// 順序は ElementName でソートする (H3)。CIM に作成順を表す決定的キーが無く、port の InstanceID は
// ランダム GUID で config 順と無相関のため、表示名順にすることでユーザーが名前で state 順を制御できる。
// 制約: 同名 NIC が複数あると順序が一意に定まらない (同名複数 NIC は本経路では非対応、ドキュメント参照)。
func mapNetworkAdapterRefs(
	vmName string,
	ports []*hyperv.Msvm_SyntheticEthernetPortSettingData,
	allocs []*hyperv.Msvm_EthernetPortAllocationSettingData,
	switches []*hyperv.Msvm_VirtualEthernetSwitch,
) []networkAdapterRef {
	sort.SliceStable(ports, func(i, j int) bool {
		if ports[i].ElementName != ports[j].ElementName {
			return ports[i].ElementName < ports[j].ElementName
		}
		return ports[i].InstanceID < ports[j].InstanceID // 同名時のタイブレーク
	})

	refs := make([]networkAdapterRef, 0, len(ports))
	for i, p := range ports {
		a := defaultVmNetworkAdapter()
		a.VmName = vmName
		a.Index = i
		a.Name = p.ElementName
		a.DynamicMacAddress = !p.StaticMacAddress
		if p.StaticMacAddress {
			a.StaticMacAddress = p.Address
		}
		// 接続先スイッチを allocation 経由で解決する。
		if switchName := resolveSwitchName(p.InstanceID, allocs, switches); switchName != "" {
			a.SwitchName = switchName
		}
		refs = append(refs, networkAdapterRef{adapter: a, portInstanceID: p.InstanceID})
	}
	return refs
}

// resolveSwitchName は port に紐づく allocation を辿り、接続先スイッチの表示名を返す (無ければ空)。
//
// alloc.Parent は WMI オブジェクトパス (InstanceID 二重エスケープ) のため extractInstanceIDFromRef で
// 正規化してから port と突き合わせる。スイッチ側は Name(GUID、バックスラッシュ無し) が HostResource
// に含まれるかで判定する。
func resolveSwitchName(portInstanceID string, allocs []*hyperv.Msvm_EthernetPortAllocationSettingData, switches []*hyperv.Msvm_VirtualEthernetSwitch) string {
	for _, alloc := range allocs {
		if extractInstanceIDFromRef(alloc.Parent) != portInstanceID {
			continue
		}
		for _, sw := range switches {
			if sw.Name != "" && strings.Contains(alloc.HostResource, sw.Name) {
				return sw.ElementName
			}
		}
	}
	return ""
}

// normalizeMac は MAC アドレスの区切り文字 (: - . 空白) を除去して小文字化する。
// ユーザーが "00:15:5D:.." 等で書いても Get 結果 (12桁hex) とキーが一致するようにする (M2)。
func normalizeMac(mac string) string {
	r := strings.NewReplacer(":", "", "-", "", ".", "", " ", "")
	return strings.ToLower(r.Replace(mac))
}

// networkAdapterKey は NIC の同一性を (名前, スイッチ, MAC) で表す。
func networkAdapterKey(a api.VmNetworkAdapter) string {
	mac := "dynamic"
	if !a.DynamicMacAddress {
		mac = normalizeMac(a.StaticMacAddress)
	}
	return fmt.Sprintf("%s/%s/%s", a.Name, strings.ToLower(a.SwitchName), mac)
}

// planNetworkAdapterReconcile は現状を所望に収束させる remove(Port InstanceID)/add 計画を返す。
//
// 同一キー (名前+スイッチ+MAC) の NIC が複数あり得るため multiset 差分で過不足を計算する (H1)。
// 単純な set 差分では「現状1本・所望2本」で toAdd が空になり永遠に収束しない。
func planNetworkAdapterReconcile(current []networkAdapterRef, desired []api.VmNetworkAdapter) (toRemove []string, toAdd []api.VmNetworkAdapter) {
	currentByKey := make(map[string][]string) // key → 現状 Port InstanceID 群
	for _, r := range current {
		k := networkAdapterKey(r.adapter)
		currentByKey[k] = append(currentByKey[k], r.portInstanceID)
	}
	desiredByKey := make(map[string][]api.VmNetworkAdapter) // key → 所望 NIC 群
	for _, d := range desired {
		k := networkAdapterKey(d)
		desiredByKey[k] = append(desiredByKey[k], d)
	}
	// 現状が所望より多いキーは余剰分を remove。
	for k, ports := range currentByKey {
		for i := len(desiredByKey[k]); i < len(ports); i++ {
			toRemove = append(toRemove, ports[i])
		}
	}
	// 所望が現状より多いキーは不足分を add。
	for k, ds := range desiredByKey {
		for i := len(currentByKey[k]); i < len(ds); i++ {
			toAdd = append(toAdd, ds[i])
		}
	}
	return toRemove, toAdd
}
