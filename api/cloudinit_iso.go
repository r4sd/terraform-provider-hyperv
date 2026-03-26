package api

import "context"

type CloudInitIso struct {
	DestinationIsoFilePath        string
	UserData                      string
	MetaData                      string
	NetworkConfig                 string
	ResolveDestinationIsoFilePath string
}

type HypervCloudInitIsoClient interface {
	CreateOrUpdateCloudInitIso(ctx context.Context, destinationIsoFilePath string, userData string, metaData string, networkConfig string) (err error)
	GetCloudInitIso(ctx context.Context, destinationIsoFilePath string) (result CloudInitIso, err error)
	DeleteCloudInitIso(ctx context.Context, destinationIsoFilePath string) (err error)
}
