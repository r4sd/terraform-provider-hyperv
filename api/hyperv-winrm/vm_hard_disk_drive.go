package hyperv_winrm

import (
	"context"
	"encoding/json"
	"text/template"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

type createVmHardDiskDriveArgs struct {
	VmHardDiskDriveJson string
}

var createVmHardDiskDriveTemplate = template.Must(template.New("CreateVmHardDiskDrive").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
$vmHardDiskDrive = '{{.VmHardDiskDriveJson}}' | ConvertFrom-Json

$NewVmHardDiskDriveArgs = @{
	VmName=$vmHardDiskDrive.VmName
	ControllerType=$vmHardDiskDrive.ControllerType
	ControllerNumber=$vmHardDiskDrive.ControllerNumber
	ControllerLocation=$vmHardDiskDrive.ControllerLocation
	Path=$vmHardDiskDrive.Path
	ResourcePoolName=$vmHardDiskDrive.ResourcePoolName
	SupportPersistentReservations=$vmHardDiskDrive.SupportPersistentReservations
	MaximumIops=$_.MaximumIops;
	MinimumIops=$_.MinimumIops;
	QosPolicyId=$_.QosPolicyId;
	OverrideCacheAttributes=$vmHardDiskDrive.OverrideCacheAttributes
	AllowUnverifiedPaths=$true
}

if ($vmHardDiskDrive.DiskNumber -lt 4294967295){
	$NewVmHardDiskDriveArgs.DiskNumber=$vmHardDiskDrive.DiskNumber
}

Add-VmHardDiskDrive @NewVmHardDiskDriveArgs
`))

func (c *ClientConfig) CreateVmHardDiskDrive(
	ctx context.Context,
	vmName string,
	controllerType api.ControllerType,
	controllerNumber int32,
	controllerLocation int32,
	path string,
	diskNumber uint32,
	resourcePoolName string,
	supportPersistentReservations bool,
	maximumIops uint64,
	minimumIops uint64,
	qosPolicyId string,
	overrideCacheAttributes api.CacheAttributes,

) (err error) {
	vmHardDiskDriveJson, err := json.Marshal(api.VmHardDiskDrive{
		VmName:                        vmName,
		ControllerType:                controllerType,
		ControllerNumber:              controllerNumber,
		ControllerLocation:            controllerLocation,
		Path:                          path,
		DiskNumber:                    diskNumber,
		ResourcePoolName:              resourcePoolName,
		SupportPersistentReservations: supportPersistentReservations,
		MaximumIops:                   maximumIops,
		MinimumIops:                   minimumIops,
		QosPolicyId:                   qosPolicyId,
		OverrideCacheAttributes:       overrideCacheAttributes,
	})

	if err != nil {
		return err
	}

	err = c.WinRmClient.RunFireAndForgetScript(ctx, createVmHardDiskDriveTemplate, createVmHardDiskDriveArgs{
		VmHardDiskDriveJson: string(vmHardDiskDriveJson),
	})

	return err
}

type getVmHardDiskDrivesArgs struct {
	VmName string
}

var getVmHardDiskDrivesTemplate = template.Must(template.New("GetVmHardDiskDrives").Parse(`
$ErrorActionPreference = 'Stop'
$vmHardDiskDrivesObject = @(Get-VM -Name '{{.VmName}}*' | ?{$_.Name -eq '{{.VmName}}' } | Get-VMHardDiskDrive | %{ @{
	ControllerType=$_.ControllerType;
	ControllerNumber=$_.ControllerNumber;
	ControllerLocation=$_.ControllerLocation;
	Path=$_.Path;
	DiskNumber=if ($_.DiskNumber -eq $null) { 4294967295 } else { $_.DiskNumber };
	ResourcePoolName=$_.PoolName;
	SupportPersistentReservations=$_.SupportPersistentReservations;
	MaximumIops=$_.MaximumIops;
	MinimumIops=$_.MinimumIops;
	QosPolicyId=$_.QosPolicyId;	
	OverrideCacheAttributes=$_.WriteHardeningMethod;
}})

if ($vmHardDiskDrivesObject) {
	$vmHardDiskDrives = ConvertTo-Json -InputObject $vmHardDiskDrivesObject
	$vmHardDiskDrives
} else {
	"[]"
}
`))

func (c *ClientConfig) GetVmHardDiskDrives(ctx context.Context, vmName string) (result []api.VmHardDiskDrive, err error) {
	result = make([]api.VmHardDiskDrive, 0)

	err = c.WinRmClient.RunScriptWithResult(ctx, getVmHardDiskDrivesTemplate, getVmHardDiskDrivesArgs{
		VmName: vmName,
	}, &result)

	return result, err
}

type updateVmHardDiskDriveArgs struct {
	VmName              string
	Index               int
	VmHardDiskDriveJson string
}

var updateVmHardDiskDriveTemplate = template.Must(template.New("UpdateVmHardDiskDrive").Parse(`
$ErrorActionPreference = 'Stop'
Import-Module Hyper-V
$vmHardDiskDrive = '{{.VmHardDiskDriveJson}}' | ConvertFrom-Json

$vmHardDiskDrivesObject = @(Get-VM -Name '{{.VmName}}*' | ?{$_.Name -eq '{{.VmName}}' } | Get-VMHardDiskDrive)[{{.Index}}]

if (!$vmHardDiskDrivesObject){
	throw "VM hard disk drive does not exist at index {{.Index}}"
}

$SetVmHardDiskDriveArgs = @{}
$SetVmHardDiskDriveArgs.VmName=$vmHardDiskDrivesObject.VmName
$SetVmHardDiskDriveArgs.ControllerType=$vmHardDiskDrivesObject.ControllerType
$SetVmHardDiskDriveArgs.ControllerLocation=$vmHardDiskDrivesObject.ControllerLocation
$SetVmHardDiskDriveArgs.ControllerNumber=$vmHardDiskDrivesObject.ControllerNumber
$SetVmHardDiskDriveArgs.ToControllerLocation=$vmHardDiskDrive.ControllerLocation
$SetVmHardDiskDriveArgs.ToControllerNumber=$vmHardDiskDrive.ControllerNumber
$SetVmHardDiskDriveArgs.Path=$vmHardDiskDrive.Path
if ($vmHardDiskDrive.DiskNumber -lt 4294967295){
	$SetVmHardDiskDriveArgs.DiskNumber=$vmHardDiskDrive.DiskNumber
}
if ($vmHardDiskDrivesObject.ResourcePoolName -ne $vmHardDiskDrive.ResourcePoolName) {
	if ($vmHardDiskDrive.ResourcePoolName) {
		$SetVmHardDiskDriveArgs.ResourcePoolName=$vmHardDiskDrive.ResourcePoolName
	} else {
		throw "Unable to remove resource pool $($vmHardDiskDrive.ResourcePoolName) from hard disk drive $(ConvertTo-Json -InputObject $vmHardDiskDrivesObject)"
	}
}
$SetVmHardDiskDriveArgs.SupportPersistentReservations=$vmHardDiskDrive.SupportPersistentReservations
$SetVmHardDiskDriveArgs.MaximumIops=$vmHardDiskDrive.MaximumIops
$SetVmHardDiskDriveArgs.MinimumIops=$vmHardDiskDrive.MinimumIops
$SetVmHardDiskDriveArgs.QosPolicyId=$vmHardDiskDrive.QosPolicyId
$SetVmHardDiskDriveArgs.OverrideCacheAttributes=$vmHardDiskDrive.OverrideCacheAttributes
$SetVmHardDiskDriveArgs.AllowUnverifiedPaths=$true

Set-VMHardDiskDrive @SetVmHardDiskDriveArgs

`))

func (c *ClientConfig) UpdateVmHardDiskDrive(
	ctx context.Context,
	vmName string,
	index int,
	controllerType api.ControllerType,
	controllerNumber int32,
	controllerLocation int32,
	path string,
	diskNumber uint32,
	resourcePoolName string,
	supportPersistentReservations bool,
	maximumIops uint64,
	minimumIops uint64,
	qosPolicyId string,
	overrideCacheAttributes api.CacheAttributes,
) (err error) {
	vmHardDiskDriveJson, err := json.Marshal(api.VmHardDiskDrive{
		VmName:                        vmName,
		ControllerType:                controllerType,
		ControllerNumber:              controllerNumber,
		ControllerLocation:            controllerLocation,
		Path:                          path,
		DiskNumber:                    diskNumber,
		ResourcePoolName:              resourcePoolName,
		SupportPersistentReservations: supportPersistentReservations,
		MaximumIops:                   maximumIops,
		MinimumIops:                   minimumIops,
		QosPolicyId:                   qosPolicyId,
		OverrideCacheAttributes:       overrideCacheAttributes,
	})

	if err != nil {
		return err
	}

	err = c.WinRmClient.RunFireAndForgetScript(ctx, updateVmHardDiskDriveTemplate, updateVmHardDiskDriveArgs{
		VmName:              vmName,
		Index:               index,
		VmHardDiskDriveJson: string(vmHardDiskDriveJson),
	})

	return err
}

type deleteVmHardDiskDriveArgs struct {
	VmName string
	Index  int
}

var deleteVmHardDiskDriveTemplate = template.Must(template.New("DeleteVmHardDiskDrive").Parse(`
$ErrorActionPreference = 'Stop'

@(Get-VM -Name '{{.VmName}}*' | ?{$_.Name -eq '{{.VmName}}' } | Get-VMHardDiskDrive)[{{.Index}}] | Remove-VMHardDiskDrive
`))

func (c *ClientConfig) DeleteVmHardDiskDrive(ctx context.Context, vmName string, index int) (err error) {
	err = c.WinRmClient.RunFireAndForgetScript(ctx, deleteVmHardDiskDriveTemplate, deleteVmHardDiskDriveArgs{
		VmName: vmName,
		Index:  index,
	})

	return err
}

func (c *ClientConfig) CreateOrUpdateVmHardDiskDrives(ctx context.Context, vmName string, hardDiskDrives []api.VmHardDiskDrive) (err error) {
	currentHardDiskDrives, err := c.GetVmHardDiskDrives(ctx, vmName)
	if err != nil {
		return err
	}

	currentHardDiskDrivesLength := len(currentHardDiskDrives)
	desiredHardDiskDrivesLength := len(hardDiskDrives)

	// Step 1: 余剰ドライブを末尾から削除（インデックスシフトを回避）
	for i := currentHardDiskDrivesLength - 1; i > desiredHardDiskDrivesLength-1; i-- {
		err = c.DeleteVmHardDiskDrive(ctx, vmName, i)
		if err != nil {
			return err
		}
	}

	if currentHardDiskDrivesLength > desiredHardDiskDrivesLength {
		currentHardDiskDrivesLength = desiredHardDiskDrivesLength
	}

	// Step 2: コントローラタイプ変更を検知
	// Set-VMHardDiskDrive には -ToControllerType パラメータがないため、
	// コントローラタイプ変更時は Delete + Create で対応する必要がある。
	// インデックスシフト問題を回避するため、変更がある場合は全ドライブを再作成する。
	hasControllerTypeChange := false
	for i := 0; i < currentHardDiskDrivesLength; i++ {
		if currentHardDiskDrives[i].ControllerType != hardDiskDrives[i].ControllerType {
			hasControllerTypeChange = true
			break
		}
	}

	if hasControllerTypeChange {
		// Step 3a: 全既存ドライブを末尾から削除し、全 desired を作成
		for i := currentHardDiskDrivesLength - 1; i >= 0; i-- {
			err = c.DeleteVmHardDiskDrive(ctx, vmName, i)
			if err != nil {
				return err
			}
		}
		for i := 0; i < desiredHardDiskDrivesLength; i++ {
			hardDiskDrive := hardDiskDrives[i]
			err = c.CreateVmHardDiskDrive(
				ctx,
				vmName,
				hardDiskDrive.ControllerType,
				hardDiskDrive.ControllerNumber,
				hardDiskDrive.ControllerLocation,
				hardDiskDrive.Path,
				hardDiskDrive.DiskNumber,
				hardDiskDrive.ResourcePoolName,
				hardDiskDrive.SupportPersistentReservations,
				hardDiskDrive.MaximumIops,
				hardDiskDrive.MinimumIops,
				hardDiskDrive.QosPolicyId,
				hardDiskDrive.OverrideCacheAttributes,
			)
			if err != nil {
				return err
			}
		}
	} else {
		// Step 3b: 通常パス（インデックスベースで Update + 新規 Create）
		for i := 0; i < currentHardDiskDrivesLength; i++ {
			hardDiskDrive := hardDiskDrives[i]
			err = c.UpdateVmHardDiskDrive(
				ctx,
				vmName,
				i,
				hardDiskDrive.ControllerType,
				hardDiskDrive.ControllerNumber,
				hardDiskDrive.ControllerLocation,
				hardDiskDrive.Path,
				hardDiskDrive.DiskNumber,
				hardDiskDrive.ResourcePoolName,
				hardDiskDrive.SupportPersistentReservations,
				hardDiskDrive.MaximumIops,
				hardDiskDrive.MinimumIops,
				hardDiskDrive.QosPolicyId,
				hardDiskDrive.OverrideCacheAttributes,
			)
			if err != nil {
				return err
			}
		}

		for i := currentHardDiskDrivesLength; i < desiredHardDiskDrivesLength; i++ {
			hardDiskDrive := hardDiskDrives[i]
			err = c.CreateVmHardDiskDrive(
				ctx,
				vmName,
				hardDiskDrive.ControllerType,
				hardDiskDrive.ControllerNumber,
				hardDiskDrive.ControllerLocation,
				hardDiskDrive.Path,
				hardDiskDrive.DiskNumber,
				hardDiskDrive.ResourcePoolName,
				hardDiskDrive.SupportPersistentReservations,
				hardDiskDrive.MaximumIops,
				hardDiskDrive.MinimumIops,
				hardDiskDrive.QosPolicyId,
				hardDiskDrive.OverrideCacheAttributes,
			)
			if err != nil {
				return err
			}
		}
	}

	return nil
}
