package hyperv_wsman

import (
	"context"
	"fmt"

	"github.com/r4sd/go-wsman/hyperv"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

// GetVmGpuAdapters は VM に割り当てられた GPU パーティションを go-wsman 経由で取得する。
//
// PS 版 (Get-VMGpuPartitionAdapter) をシャドウイングし、Read の無条件 PowerShell 実行を解消する。
// GPU 未割当の VM (homelab など) では空スライスを返す (go-wsman ListGpuAdapters がホスト能力定義を
// 除外して空を返す)。
func (c *ClientConfig) GetVmGpuAdapters(ctx context.Context, vmName string) ([]api.VmGpuAdapter, error) {
	guid, err := c.resolveVMGUID(ctx, vmName)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: GetVmGpuAdapters %q: %w", vmName, err)
	}
	gpus, err := c.WsmanClient.ListGpuAdapters(ctx, guid)
	if err != nil {
		return nil, fmt.Errorf("hyperv-wsman: GetVmGpuAdapters %q: %w", vmName, err)
	}
	result := make([]api.VmGpuAdapter, 0, len(gpus))
	for _, g := range gpus {
		result = append(result, gpuAdapterFromSettingData(vmName, g))
	}
	return result, nil
}

// gpuAdapterFromSettingData は go-wsman の Msvm_GpuPartitionSettingData を provider の
// api.VmGpuAdapter に変換する純関数。全プロパティ uint64 で 1:1 に写す。
func gpuAdapterFromSettingData(vmName string, g *hyperv.Msvm_GpuPartitionSettingData) api.VmGpuAdapter {
	return api.VmGpuAdapter{
		VmName:                  vmName,
		MinPartitionVRAM:        g.MinPartitionVRAM,
		MaxPartitionVRAM:        g.MaxPartitionVRAM,
		OptimalPartitionVRAM:    g.OptimalPartitionVRAM,
		MinPartitionEncode:      g.MinPartitionEncode,
		MaxPartitionEncode:      g.MaxPartitionEncode,
		OptimalPartitionEncode:  g.OptimalPartitionEncode,
		MinPartitionDecode:      g.MinPartitionDecode,
		MaxPartitionDecode:      g.MaxPartitionDecode,
		OptimalPartitionDecode:  g.OptimalPartitionDecode,
		MinPartitionCompute:     g.MinPartitionCompute,
		MaxPartitionCompute:     g.MaxPartitionCompute,
		OptimalPartitionCompute: g.OptimalPartitionCompute,
	}
}

// CreateOrUpdateVmGpuAdapters は GPU パーティションの割当を反映する。
//
// 空リスト (GPU 割当なし) の時は PowerShell を流さず no-op で返す (空リストガード)。PS 版は空でも
// Get-VMGpuPartitionAdapter + Remove を無条件実行するため、GPU 未使用の VM (homelab など) で毎回
// PS が走っていた。この非対称を解消する。
//
// 非空 (GPU 割当あり) の時は go-wsman 側に Add プリミティブが未実装 (#59 の Add 部分) のため、
// 埋め込んだ PowerShell 実装にフォールバックする。go-wsman で Add を実装したらここを差し替える。
func (c *ClientConfig) CreateOrUpdateVmGpuAdapters(ctx context.Context, vmName string, gpuAdapters []api.VmGpuAdapter) error {
	if len(gpuAdapters) == 0 {
		return nil
	}
	return c.ClientConfig.CreateOrUpdateVmGpuAdapters(ctx, vmName, gpuAdapters)
}
