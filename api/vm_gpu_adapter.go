package api

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

type VmGpuAdapter struct {
	VmName                  string
	MinPartitionVRAM        uint64
	MaxPartitionVRAM        uint64
	OptimalPartitionVRAM    uint64
	MinPartitionEncode      uint64
	MaxPartitionEncode      uint64
	OptimalPartitionEncode  uint64
	MinPartitionDecode      uint64
	MaxPartitionDecode      uint64
	OptimalPartitionDecode  uint64
	MinPartitionCompute     uint64
	MaxPartitionCompute     uint64
	OptimalPartitionCompute uint64
}

type HypervVmGpuAdapterClient interface {
	GetVmGpuAdapters(ctx context.Context, vmName string) (result []VmGpuAdapter, err error)
	CreateOrUpdateVmGpuAdapters(ctx context.Context, vmName string, gpuAdapters []VmGpuAdapter) (err error)
}

func ExpandGpuAdapters(d *schema.ResourceData) ([]VmGpuAdapter, error) {
	expandedGpuAdapters := make([]VmGpuAdapter, 0)

	if v, ok := d.GetOk("gpu_adapters"); ok {
		gpuAdapters := v.([]interface{})

		for _, gpuAdapter := range gpuAdapters {
			gpuAdapter, ok := gpuAdapter.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("[ERROR][hyperv] gpu_adapters should be a Hash - was '%+v'", gpuAdapter)
			}

			conv := NewIntConverter()
			expandedGpuAdapter := VmGpuAdapter{
				MinPartitionVRAM:        conv.Uint64(gpuAdapter["min_partition_vram"].(int)),
				MaxPartitionVRAM:        conv.Uint64(gpuAdapter["max_partition_vram"].(int)),
				OptimalPartitionVRAM:    conv.Uint64(gpuAdapter["optimal_partition_vram"].(int)),
				MinPartitionEncode:      conv.Uint64(gpuAdapter["min_partition_encode"].(int)),
				MaxPartitionEncode:      conv.Uint64(gpuAdapter["max_partition_encode"].(int)),
				OptimalPartitionEncode:  conv.Uint64(gpuAdapter["optimal_partition_encode"].(int)),
				MinPartitionDecode:      conv.Uint64(gpuAdapter["min_partition_decode"].(int)),
				MaxPartitionDecode:      conv.Uint64(gpuAdapter["max_partition_decode"].(int)),
				OptimalPartitionDecode:  conv.Uint64(gpuAdapter["optimal_partition_decode"].(int)),
				MinPartitionCompute:     conv.Uint64(gpuAdapter["min_partition_compute"].(int)),
				MaxPartitionCompute:     conv.Uint64(gpuAdapter["max_partition_compute"].(int)),
				OptimalPartitionCompute: conv.Uint64(gpuAdapter["optimal_partition_compute"].(int)),
			}
			if conv.Err() != nil {
				return nil, conv.Err()
			}

			expandedGpuAdapters = append(expandedGpuAdapters, expandedGpuAdapter)
		}
	}

	return expandedGpuAdapters, nil
}

func FlattenGpuAdapters(gpuAdapters *[]VmGpuAdapter) []interface{} {
	if gpuAdapters == nil || len(*gpuAdapters) < 1 {
		return nil
	}

	flattenedGpuAdapters := make([]interface{}, 0)

	for _, gpuAdapter := range *gpuAdapters {
		flattenedGpuAdapter := make(map[string]interface{})
		flattenedGpuAdapter["min_partition_vram"] = gpuAdapter.MinPartitionVRAM
		flattenedGpuAdapter["max_partition_vram"] = gpuAdapter.MaxPartitionVRAM
		flattenedGpuAdapter["optimal_partition_vram"] = gpuAdapter.OptimalPartitionVRAM
		flattenedGpuAdapter["min_partition_encode"] = gpuAdapter.MinPartitionEncode
		flattenedGpuAdapter["max_partition_encode"] = gpuAdapter.MaxPartitionEncode
		flattenedGpuAdapter["optimal_partition_encode"] = gpuAdapter.OptimalPartitionEncode
		flattenedGpuAdapter["min_partition_decode"] = gpuAdapter.MinPartitionDecode
		flattenedGpuAdapter["max_partition_decode"] = gpuAdapter.MaxPartitionDecode
		flattenedGpuAdapter["optimal_partition_decode"] = gpuAdapter.OptimalPartitionDecode
		flattenedGpuAdapter["min_partition_compute"] = gpuAdapter.MinPartitionCompute
		flattenedGpuAdapter["max_partition_compute"] = gpuAdapter.MaxPartitionCompute
		flattenedGpuAdapter["optimal_partition_compute"] = gpuAdapter.OptimalPartitionCompute
		flattenedGpuAdapters = append(flattenedGpuAdapters, flattenedGpuAdapter)
	}

	return flattenedGpuAdapters
}
