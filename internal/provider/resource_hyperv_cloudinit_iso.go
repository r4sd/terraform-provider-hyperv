package provider

import (
	"context"
	log "log"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

const (
	ReadCloudInitIsoTimeout   = 1 * time.Minute
	CreateCloudInitIsoTimeout = 5 * time.Minute
	UpdateCloudInitIsoTimeout = 5 * time.Minute
	DeleteCloudInitIsoTimeout = 1 * time.Minute
)

func resourceHyperVCloudInitIso() *schema.Resource {
	return &schema.Resource{
		Description: "This resource allows you to manage Cloud-Init compatible ISO images on the Hyper-V host. The ISO is created with the volume label `cidata` for NoCloud datasource compatibility.",
		Timeouts: &schema.ResourceTimeout{
			Read:   schema.DefaultTimeout(ReadCloudInitIsoTimeout),
			Create: schema.DefaultTimeout(CreateCloudInitIsoTimeout),
			Update: schema.DefaultTimeout(UpdateCloudInitIsoTimeout),
			Delete: schema.DefaultTimeout(DeleteCloudInitIsoTimeout),
		},
		CreateContext: resourceHyperVCloudInitIsoCreate,
		ReadContext:   resourceHyperVCloudInitIsoRead,
		UpdateContext: resourceHyperVCloudInitIsoUpdate,
		DeleteContext: resourceHyperVCloudInitIsoDelete,
		Importer: &schema.ResourceImporter{
			StateContext: schema.ImportStatePassthroughContext,
		},
		Schema: map[string]*schema.Schema{
			"destination_iso_file_path": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "Remote path for the generated cloud-init ISO file.",
			},
			"user_data": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Cloud-init user-data content (typically #cloud-config YAML).",
			},
			"meta_data": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "Cloud-init meta-data content (YAML with instance-id, local-hostname, etc.).",
			},
			"network_config": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "",
				Description: "Cloud-init network-config content (Networking v2 YAML).",
			},
			"resolve_destination_iso_file_path": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The actual remote ISO file path that was used.",
			},
		},
	}
}

func resourceHyperVCloudInitIsoCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	log.Printf("[INFO][cloudinit-iso][create] creating cloud-init iso: %#v", d)
	c := meta.(api.Client)

	destinationIsoFilePath := (d.Get("destination_iso_file_path")).(string)
	userData := (d.Get("user_data")).(string)
	metaData := (d.Get("meta_data")).(string)
	networkConfig := (d.Get("network_config")).(string)

	if destinationIsoFilePath == "" {
		return diag.Errorf("[ERROR][cloudinit-iso][create] destination_iso_file_path argument is required")
	}

	err := c.CreateOrUpdateCloudInitIso(ctx, destinationIsoFilePath, userData, metaData, networkConfig)
	if err != nil {
		return diag.FromErr(err)
	}

	d.SetId(destinationIsoFilePath)
	log.Printf("[INFO][cloudinit-iso][create] created cloud-init iso: %#v", d)

	return resourceHyperVCloudInitIsoRead(ctx, d, meta)
}

func resourceHyperVCloudInitIsoRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	log.Printf("[INFO][cloudinit-iso][read] reading cloud-init iso: %#v", d)
	c := meta.(api.Client)

	destinationIsoFilePath := d.Id()

	cloudInitIso, err := c.GetCloudInitIso(ctx, destinationIsoFilePath)
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[INFO][cloudinit-iso][read] retrieved cloud-init iso: %+v", cloudInitIso)

	if err := d.Set("destination_iso_file_path", cloudInitIso.DestinationIsoFilePath); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("user_data", cloudInitIso.UserData); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("meta_data", cloudInitIso.MetaData); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("network_config", cloudInitIso.NetworkConfig); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("resolve_destination_iso_file_path", cloudInitIso.ResolveDestinationIsoFilePath); err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[INFO][cloudinit-iso][read] read cloud-init iso: %#v", d)

	return nil
}

func resourceHyperVCloudInitIsoUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	log.Printf("[INFO][cloudinit-iso][update] updating cloud-init iso: %#v", d)
	c := meta.(api.Client)

	destinationIsoFilePath := d.Id()
	userData := (d.Get("user_data")).(string)
	metaData := (d.Get("meta_data")).(string)
	networkConfig := (d.Get("network_config")).(string)

	err := c.CreateOrUpdateCloudInitIso(ctx, destinationIsoFilePath, userData, metaData, networkConfig)
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[INFO][cloudinit-iso][update] updated cloud-init iso: %#v", d)

	return resourceHyperVCloudInitIsoRead(ctx, d, meta)
}

func resourceHyperVCloudInitIsoDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	log.Printf("[INFO][cloudinit-iso][delete] deleting cloud-init iso: %#v", d)
	c := meta.(api.Client)

	destinationIsoFilePath := d.Id()

	err := c.DeleteCloudInitIso(ctx, destinationIsoFilePath)
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[INFO][cloudinit-iso][delete] deleted cloud-init iso: %#v", d)

	return nil
}
