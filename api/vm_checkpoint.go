package api

import "context"

type VmCheckpoint struct {
	VmName         string
	Name           string
	CheckpointType string
	Id             string
	ParentId       string
	CreationTime   string
}

type HypervVmCheckpointClient interface {
	CreateVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (err error)
	GetVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (result VmCheckpoint, err error)
	DeleteVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (err error)
	RestoreVmCheckpoint(ctx context.Context, vmName string, checkpointName string) (err error)
}
