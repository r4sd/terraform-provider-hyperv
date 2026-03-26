package provider

import (
	"context"
	"fmt"
	log "log"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/taliesins/terraform-provider-hyperv/api"
)

const (
	ReadVmCheckpointTimeout   = 1 * time.Minute
	CreateVmCheckpointTimeout = 5 * time.Minute
	DeleteVmCheckpointTimeout = 5 * time.Minute
)

func resourceHyperVVmCheckpoint() *schema.Resource {
	return &schema.Resource{
		Description: "Manages Hyper-V VM checkpoints (snapshots). Supports optional restore on destroy for chaos engineering workflows.",
		Timeouts: &schema.ResourceTimeout{
			Read:   schema.DefaultTimeout(ReadVmCheckpointTimeout),
			Create: schema.DefaultTimeout(CreateVmCheckpointTimeout),
			Delete: schema.DefaultTimeout(DeleteVmCheckpointTimeout),
		},
		CreateContext: resourceHyperVVmCheckpointCreate,
		ReadContext:   resourceHyperVVmCheckpointRead,
		DeleteContext: resourceHyperVVmCheckpointDelete,
		Schema: map[string]*schema.Schema{
			"vm_name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the virtual machine to checkpoint.",
			},
			"checkpoint_name": {
				Type:        schema.TypeString,
				Required:    true,
				ForceNew:    true,
				Description: "The name of the checkpoint to create.",
			},
			"restore_on_destroy": {
				Type:        schema.TypeBool,
				Optional:    true,
				Default:     false,
				ForceNew:    true,
				Description: "If true, the VM will be restored to this checkpoint before it is deleted.",
			},
			"checkpoint_type": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The type of the checkpoint (Production, Standard, etc.).",
			},
			"checkpoint_id": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The unique ID of the checkpoint.",
			},
			"parent_id": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The ID of the parent checkpoint, if any.",
			},
			"creation_time": {
				Type:        schema.TypeString,
				Computed:    true,
				Description: "The creation time of the checkpoint in ISO 8601 format.",
			},
		},
	}
}

func parseCheckpointId(id string) (vmName, checkpointName string, err error) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected format of ID (%s), expected vmName/checkpointName", id)
	}
	return parts[0], parts[1], nil
}

func resourceHyperVVmCheckpointCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	c := meta.(api.Client)

	vmName := d.Get("vm_name").(string)
	checkpointName := d.Get("checkpoint_name").(string)

	log.Printf("[INFO][vm-checkpoint][create] creating checkpoint %q for VM %q", checkpointName, vmName)

	err := c.CreateVmCheckpoint(ctx, vmName, checkpointName)
	if err != nil {
		return diag.FromErr(fmt.Errorf("creating checkpoint %q for VM %q: %w", checkpointName, vmName, err))
	}

	d.SetId(fmt.Sprintf("%s/%s", vmName, checkpointName))
	log.Printf("[INFO][vm-checkpoint][create] created checkpoint %q for VM %q", checkpointName, vmName)

	return resourceHyperVVmCheckpointRead(ctx, d, meta)
}

func resourceHyperVVmCheckpointRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	c := meta.(api.Client)

	vmName, checkpointName, err := parseCheckpointId(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[INFO][vm-checkpoint][read] reading checkpoint %q for VM %q", checkpointName, vmName)

	checkpoint, err := c.GetVmCheckpoint(ctx, vmName, checkpointName)
	if err != nil {
		return diag.FromErr(fmt.Errorf("reading checkpoint %q for VM %q: %w", checkpointName, vmName, err))
	}

	// チェックポイントが外部で削除された場合
	if checkpoint.Name == "" {
		log.Printf("[INFO][vm-checkpoint][read] checkpoint %q for VM %q not found, removing from state", checkpointName, vmName)
		d.SetId("")
		return nil
	}

	if err := d.Set("vm_name", checkpoint.VmName); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("checkpoint_name", checkpoint.Name); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("checkpoint_type", checkpoint.CheckpointType); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("checkpoint_id", checkpoint.Id); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("parent_id", checkpoint.ParentId); err != nil {
		return diag.FromErr(err)
	}
	if err := d.Set("creation_time", checkpoint.CreationTime); err != nil {
		return diag.FromErr(err)
	}

	log.Printf("[INFO][vm-checkpoint][read] read checkpoint %q for VM %q", checkpointName, vmName)

	return nil
}

func resourceHyperVVmCheckpointDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	c := meta.(api.Client)

	vmName, checkpointName, err := parseCheckpointId(d.Id())
	if err != nil {
		return diag.FromErr(err)
	}

	restoreOnDestroy := d.Get("restore_on_destroy").(bool)

	if restoreOnDestroy {
		log.Printf("[INFO][vm-checkpoint][delete] restoring VM %q to checkpoint %q before deletion", vmName, checkpointName)
		err := c.RestoreVmCheckpoint(ctx, vmName, checkpointName)
		if err != nil {
			return diag.FromErr(fmt.Errorf("restoring VM %q to checkpoint %q: %w", vmName, checkpointName, err))
		}
		log.Printf("[INFO][vm-checkpoint][delete] restored VM %q to checkpoint %q", vmName, checkpointName)
	}

	log.Printf("[INFO][vm-checkpoint][delete] deleting checkpoint %q for VM %q", checkpointName, vmName)
	err = c.DeleteVmCheckpoint(ctx, vmName, checkpointName)
	if err != nil {
		return diag.FromErr(fmt.Errorf("deleting checkpoint %q for VM %q: %w", checkpointName, vmName, err))
	}
	log.Printf("[INFO][vm-checkpoint][delete] deleted checkpoint %q for VM %q", checkpointName, vmName)

	return nil
}
