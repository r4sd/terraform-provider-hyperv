package hyperv_winrm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"text/template"

	"github.com/taliesins/terraform-provider-hyperv/api"
)

type createOrUpdateCloudInitIsoArgs struct {
	CloudInitIsoJson string
}

var createOrUpdateCloudInitIsoTemplate = template.Must(template.New("CreateOrUpdateCloudInitIso").Parse(`
$ErrorActionPreference = 'Stop'

$cloudInitIsoJson = [System.Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('{{.CloudInitIsoJson}}'))
$cloudInit = $cloudInitIsoJson | ConvertFrom-Json

$destinationIsoFilePath = $cloudInit.DestinationIsoFilePath
$resolveDestinationIsoFilePath = $ExecutionContext.InvokeCommand.ExpandString($destinationIsoFilePath)

function New-TemporaryDirectory {
  $parent = [System.IO.Path]::GetTempPath()
  do {
    $name = [System.IO.Path]::GetRandomFileName()
    $item = New-Item -Path $parent -Name $name -ItemType "directory" -ErrorAction SilentlyContinue
  } while (-not $item)
  return $item.FullName
}

$typeDefinition = @'
    public class ISOFile  {
        public unsafe static void Create(string Path, object Stream, int BlockSize, int TotalBlocks) {
            int bytes = 0;
            byte[] buf = new byte[BlockSize];
            var ptr = (System.IntPtr)(&bytes);
            var o = System.IO.File.OpenWrite(Path);
            var i = Stream as System.Runtime.InteropServices.ComTypes.IStream;

            if (o != null) {
                while (TotalBlocks-- > 0) {
                    i.Read(buf, BlockSize, ptr); o.Write(buf, 0, bytes);
                }

                o.Flush(); o.Close();
            }
        }
    }
'@

if (!('ISOFile' -as [type])) {
    switch ($PSVersionTable.PSVersion.Major) {
        { $_ -ge 7 } {
            Add-Type -CompilerOptions "/unsafe" -TypeDefinition $typeDefinition
        }
        5 {
            $compOpts = New-Object System.CodeDom.Compiler.CompilerParameters
            $compOpts.CompilerOptions = "/unsafe"
            Add-Type -CompilerParameters $compOpts -TypeDefinition $typeDefinition
        }
        default {
            throw ("Unsupported PowerShell version.")
        }
    }
}

$tempDir = New-TemporaryDirectory
try {
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText("$tempDir\user-data", $cloudInit.UserData, $utf8NoBom)
    [System.IO.File]::WriteAllText("$tempDir\meta-data", $cloudInit.MetaData, $utf8NoBom)
    if ($cloudInit.NetworkConfig) {
        [System.IO.File]::WriteAllText("$tempDir\network-config", $cloudInit.NetworkConfig, $utf8NoBom)
    }

    try {
        $image = New-Object -ComObject IMAPI2FS.MsftFileSystemImage -Property @{VolumeName = 'cidata'} -ErrorAction Stop
        $image.ChooseImageDefaultsForMediaType(0x1)
        $image.FileSystemsToCreate = 0x3
    }
    catch {
        throw ("Failed to initialise ISO image. " + $_.exception.Message)
    }

    $targetFile = New-Item -Path $resolveDestinationIsoFilePath -ItemType File -Force -ErrorAction Stop
    if (!$targetFile) {
        throw ("Cannot create file " + $resolveDestinationIsoFilePath)
    }

    try {
        $image.Root.AddTree($tempDir, $true)
    }
    catch {
        throw ("Failed to add cloud-init files to ISO image. " + $_.exception.message)
    }

    try {
        $result = $image.CreateResultImage()
        [ISOFile]::Create($targetFile.FullName, $result.ImageStream, $result.BlockSize, $result.TotalBlocks)
    }
    catch {
        throw ("Failed to write ISO file. " + $_.exception.Message)
    }
} finally {
    Remove-Item $tempDir -Force -Recurse -ErrorAction SilentlyContinue
}

$metadata = @{}
$metadata.DestinationIsoFilePath = $cloudInit.DestinationIsoFilePath
$metadata.UserData = $cloudInit.UserData
$metadata.MetaData = $cloudInit.MetaData
$metadata.NetworkConfig = $cloudInit.NetworkConfig
$metadata.ResolveDestinationIsoFilePath = $resolveDestinationIsoFilePath
$metadata | ConvertTo-Json -Depth 100 | Out-File "$($resolveDestinationIsoFilePath).json" -Force
`))

func (c *ClientConfig) CreateOrUpdateCloudInitIso(ctx context.Context, destinationIsoFilePath string, userData string, metaData string, networkConfig string) (err error) {
	cloudInitIsoJson, err := json.Marshal(api.CloudInitIso{
		DestinationIsoFilePath: destinationIsoFilePath,
		UserData:               userData,
		MetaData:               metaData,
		NetworkConfig:          networkConfig,
	})

	if err != nil {
		return fmt.Errorf("error converting object to json: %s", err)
	}

	err = c.WinRmClient.RunFireAndForgetScript(ctx, createOrUpdateCloudInitIsoTemplate, createOrUpdateCloudInitIsoArgs{
		CloudInitIsoJson: base64.StdEncoding.EncodeToString(cloudInitIsoJson),
	})

	if err != nil {
		return fmt.Errorf("error creating or updating cloud-init iso: %v", err)
	}

	return err
}

type getCloudInitIsoArgs struct {
	DestinationIsoFilePath string
}

var getCloudInitIsoTemplate = template.Must(template.New("GetCloudInitIso").Parse(`
$ErrorActionPreference = 'Stop'
$DestinationIsoFilePath='{{.DestinationIsoFilePath}}'
$cloudInitIsoObject = $null

$expandedDestinationIsoFilePath = $ExecutionContext.InvokeCommand.ExpandString($DestinationIsoFilePath)

$metadataPath="$($expandedDestinationIsoFilePath).json"

if (Test-Path $metadataPath) {
	$metadata = Get-Content -Raw -Path $metadataPath | ConvertFrom-Json

	$cloudInitIsoObject=@{}
	$cloudInitIsoObject.DestinationIsoFilePath=$metadata.DestinationIsoFilePath
	$cloudInitIsoObject.UserData=$metadata.UserData
	$cloudInitIsoObject.MetaData=$metadata.MetaData
	$cloudInitIsoObject.NetworkConfig=$metadata.NetworkConfig
	$cloudInitIsoObject.ResolveDestinationIsoFilePath=$metadata.ResolveDestinationIsoFilePath
} else {}

if ($cloudInitIsoObject){
	$cloudInitIso = ConvertTo-Json -InputObject $cloudInitIsoObject
	$cloudInitIso
} else {
	"{}"
}
`))

func (c *ClientConfig) GetCloudInitIso(ctx context.Context, destinationIsoFilePath string) (result api.CloudInitIso, err error) {
	err = c.WinRmClient.RunScriptWithResult(ctx, getCloudInitIsoTemplate, getCloudInitIsoArgs{
		DestinationIsoFilePath: destinationIsoFilePath,
	}, &result)

	return result, err
}

type deleteCloudInitIsoArgs struct {
	DestinationIsoFilePath string
}

var deleteCloudInitIsoTemplate = template.Must(template.New("DeleteCloudInitIso").Parse(`
$ErrorActionPreference = 'Stop'
$DestinationIsoFilePath='{{.DestinationIsoFilePath}}'

$expandedDestinationIsoFilePath = $ExecutionContext.InvokeCommand.ExpandString($DestinationIsoFilePath)

if (Test-Path $expandedDestinationIsoFilePath) {
	Remove-Item $expandedDestinationIsoFilePath -Force
}

$metadataPath="$($expandedDestinationIsoFilePath).json"
if (Test-Path $metadataPath) {
	Remove-Item $metadataPath -Force
}
`))

func (c *ClientConfig) DeleteCloudInitIso(ctx context.Context, destinationIsoFilePath string) (err error) {
	err = c.WinRmClient.RunFireAndForgetScript(ctx, deleteCloudInitIsoTemplate, deleteCloudInitIsoArgs{
		DestinationIsoFilePath: destinationIsoFilePath,
	})

	if err != nil {
		return fmt.Errorf("error deleting cloud-init iso: %v", err)
	}

	return err
}
