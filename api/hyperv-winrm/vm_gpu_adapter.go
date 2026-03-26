package hyperv_winrm

import (
	"context"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

type getVmGpuAdaptersArgs struct {
	VmName string
}

var getVmGpuAdaptersTemplate = template.Must(template.New("GetVmGpuAdapters").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
$vmName = '{{.VmName}}'
$adapters = @(Get-VMGpuPartitionAdapter -VMName $vmName -ErrorAction SilentlyContinue)

$result = @()
foreach ($adapter in $adapters) {
	$item = @{
		VmName = $vmName
		MinPartitionVRAM = $adapter.MinPartitionVRAM
		MaxPartitionVRAM = $adapter.MaxPartitionVRAM
		OptimalPartitionVRAM = $adapter.OptimalPartitionVRAM
		MinPartitionEncode = $adapter.MinPartitionEncode
		MaxPartitionEncode = $adapter.MaxPartitionEncode
		OptimalPartitionEncode = $adapter.OptimalPartitionEncode
		MinPartitionDecode = $adapter.MinPartitionDecode
		MaxPartitionDecode = $adapter.MaxPartitionDecode
		OptimalPartitionDecode = $adapter.OptimalPartitionDecode
		MinPartitionCompute = $adapter.MinPartitionCompute
		MaxPartitionCompute = $adapter.MaxPartitionCompute
		OptimalPartitionCompute = $adapter.OptimalPartitionCompute
	}
	$result += $item
}

if ($result) {
	ConvertTo-Json -InputObject @($result) -Depth 100
} else {
	'[]'
}
`))

func (c *ClientConfig) GetVmGpuAdapters(ctx context.Context, vmName string) (result []api.VmGpuAdapter, err error) {
	result = make([]api.VmGpuAdapter, 0)

	err = c.WinRmClient.RunScriptWithResult(ctx, getVmGpuAdaptersTemplate, getVmGpuAdaptersArgs{
		VmName: vmName,
	}, &result)

	return result, err
}

type createOrUpdateVmGpuAdaptersArgs struct {
	VmName          string
	GpuAdaptersJson string
}

var createOrUpdateVmGpuAdaptersTemplate = template.Must(template.New("CreateOrUpdateVmGpuAdapters").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
$vmName = '{{.VmName}}'
$desiredAdapters = '{{.GpuAdaptersJson}}' | ConvertFrom-Json

# Remove all existing GPU adapters
$existing = @(Get-VMGpuPartitionAdapter -VMName $vmName -ErrorAction SilentlyContinue)
foreach ($adapter in $existing) {
	Remove-VMGpuPartitionAdapter -VMName $vmName -AdapterId $adapter.Id
}

# Add and configure desired adapters
foreach ($desired in $desiredAdapters) {
	Add-VMGpuPartitionAdapter -VMName $vmName

	$setArgs = @{
		VMName = $vmName
	}

	if ($desired.MinPartitionVRAM -gt 0) { $setArgs.MinPartitionVRAM = $desired.MinPartitionVRAM }
	if ($desired.MaxPartitionVRAM -gt 0) { $setArgs.MaxPartitionVRAM = $desired.MaxPartitionVRAM }
	if ($desired.OptimalPartitionVRAM -gt 0) { $setArgs.OptimalPartitionVRAM = $desired.OptimalPartitionVRAM }
	if ($desired.MinPartitionEncode -gt 0) { $setArgs.MinPartitionEncode = $desired.MinPartitionEncode }
	if ($desired.MaxPartitionEncode -gt 0) { $setArgs.MaxPartitionEncode = $desired.MaxPartitionEncode }
	if ($desired.OptimalPartitionEncode -gt 0) { $setArgs.OptimalPartitionEncode = $desired.OptimalPartitionEncode }
	if ($desired.MinPartitionDecode -gt 0) { $setArgs.MinPartitionDecode = $desired.MinPartitionDecode }
	if ($desired.MaxPartitionDecode -gt 0) { $setArgs.MaxPartitionDecode = $desired.MaxPartitionDecode }
	if ($desired.OptimalPartitionDecode -gt 0) { $setArgs.OptimalPartitionDecode = $desired.OptimalPartitionDecode }
	if ($desired.MinPartitionCompute -gt 0) { $setArgs.MinPartitionCompute = $desired.MinPartitionCompute }
	if ($desired.MaxPartitionCompute -gt 0) { $setArgs.MaxPartitionCompute = $desired.MaxPartitionCompute }
	if ($desired.OptimalPartitionCompute -gt 0) { $setArgs.OptimalPartitionCompute = $desired.OptimalPartitionCompute }

	# Only call Set if we have partition parameters
	if ($setArgs.Count -gt 1) {
		# Get the last added adapter to set properties
		$latestAdapters = @(Get-VMGpuPartitionAdapter -VMName $vmName)
		$lastAdapter = $latestAdapters[-1]
		$setArgs.AdapterId = $lastAdapter.Id
		Set-VMGpuPartitionAdapter @setArgs
	}
}
`))

func (c *ClientConfig) CreateOrUpdateVmGpuAdapters(ctx context.Context, vmName string, gpuAdapters []api.VmGpuAdapter) (err error) {
	gpuAdaptersJson, err := json.Marshal(gpuAdapters)
	if err != nil {
		return fmt.Errorf("error converting gpu adapters to json: %s", err)
	}

	err = c.WinRmClient.RunFireAndForgetScript(ctx, createOrUpdateVmGpuAdaptersTemplate, createOrUpdateVmGpuAdaptersArgs{
		VmName:          vmName,
		GpuAdaptersJson: string(gpuAdaptersJson),
	})

	return err
}
