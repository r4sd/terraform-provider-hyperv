package hyperv_winrm

import (
	"context"
	"text/template"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

type vmCheckpointArgs struct {
	VmName         string
	CheckpointName string
}

var createVmCheckpointTemplate = template.Must(template.New("CreateVmCheckpoint").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
Checkpoint-VM -VMName '{{.VmName}}' -SnapshotName '{{.CheckpointName}}'
`))

var getVmCheckpointTemplate = template.Must(template.New("GetVmCheckpoint").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
$checkpoint = Get-VMSnapshot -VMName '{{.VmName}}' -Name '{{.CheckpointName}}' -ErrorAction SilentlyContinue

if ($checkpoint) {
	$result = @{
		VmName = $checkpoint.VMName
		Name = $checkpoint.Name
		CheckpointType = $checkpoint.CheckpointType.ToString()
		Id = $checkpoint.Id.ToString()
		ParentId = if ($checkpoint.ParentSnapshotId) { $checkpoint.ParentSnapshotId.ToString() } else { '' }
		CreationTime = $checkpoint.CreationTime.ToString('o')
	}
	$result | ConvertTo-Json
} else {
	'{}'
}
`))

var deleteVmCheckpointTemplate = template.Must(template.New("DeleteVmCheckpoint").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
Remove-VMSnapshot -VMName '{{.VmName}}' -Name '{{.CheckpointName}}' -Confirm:$false
`))

var restoreVmCheckpointTemplate = template.Must(template.New("RestoreVmCheckpoint").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
Restore-VMSnapshot -VMName '{{.VmName}}' -Name '{{.CheckpointName}}' -Confirm:$false
`))

func (c *ClientConfig) CreateVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (err error) {
	err = c.WinRmClient.RunFireAndForgetScript(ctx, createVmCheckpointTemplate, vmCheckpointArgs{
		VmName:         vmName,
		CheckpointName: checkpointName,
	})
	return err
}

func (c *ClientConfig) GetVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (result api.VmCheckpoint, err error) {
	err = c.WinRmClient.RunScriptWithResult(ctx, getVmCheckpointTemplate, vmCheckpointArgs{
		VmName:         vmName,
		CheckpointName: checkpointName,
	}, &result)
	return result, err
}

func (c *ClientConfig) DeleteVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (err error) {
	err = c.WinRmClient.RunFireAndForgetScript(ctx, deleteVmCheckpointTemplate, vmCheckpointArgs{
		VmName:         vmName,
		CheckpointName: checkpointName,
	})
	return err
}

func (c *ClientConfig) RestoreVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (err error) {
	err = c.WinRmClient.RunFireAndForgetScript(ctx, restoreVmCheckpointTemplate, vmCheckpointArgs{
		VmName:         vmName,
		CheckpointName: checkpointName,
	})
	return err
}
