package docker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

const (
	// LabelVolumeManaged is the truthy marker on Docker volumes edvabe owns.
	LabelVolumeManaged = "edvabe.volume.managed"
	// LabelVolumeID stores the E2B-style vol_ ID on the Docker volume's labels.
	LabelVolumeID = "edvabe.volume.id"
	// LabelVolumeName stores the user-supplied logical name.
	LabelVolumeName = "edvabe.volume.name"
	// DockerVolumePrefix is the physical Docker volume name prefix.
	DockerVolumePrefix = "edvabe-vol-"
)

// VolumeCreate creates a managed Docker named volume with edvabe labels.
// The physical Docker volume name is DockerVolumePrefix + volumeID so
// it cannot collide with user-created Docker volumes or other edvabe
// volumes that share the same logical name.
func (r *Runtime) VolumeCreate(ctx context.Context, volumeID, name string) error {
	if volumeID == "" {
		return fmt.Errorf("docker runtime: VolumeCreate: volumeID is required")
	}
	dockerName := DockerVolumePrefix + volumeID
	labels := map[string]string{
		LabelVolumeManaged: "true",
		LabelVolumeID:      volumeID,
		LabelVolumeName:    name,
	}
	if _, err := r.cli.VolumeCreate(ctx, client.VolumeCreateOptions{
		Name:   dockerName,
		Labels: labels,
	}); err != nil {
		return fmt.Errorf("docker runtime: volume create %q: %w", volumeID, err)
	}
	return nil
}

// VolumeList returns all edvabe-managed Docker volumes.
func (r *Runtime) VolumeList(ctx context.Context) ([]runtime.VolumeInfo, error) {
	filters := make(client.Filters).Add("label", LabelVolumeManaged+"=true")
	result, err := r.cli.VolumeList(ctx, client.VolumeListOptions{Filters: filters})
	if err != nil {
		return nil, fmt.Errorf("docker runtime: volume list: %w", err)
	}
	out := make([]runtime.VolumeInfo, 0, len(result.Items))
	for _, v := range result.Items {
		info, ok := volumeToInfo(v)
		if ok {
			out = append(out, info)
		}
	}
	return out, nil
}

// VolumeInspect resolves a managed volume by its E2B volume ID.
func (r *Runtime) VolumeInspect(ctx context.Context, volumeID string) (*runtime.VolumeInfo, error) {
	if volumeID == "" {
		return nil, fmt.Errorf("docker runtime: VolumeInspect: volumeID is required")
	}
	dockerName := DockerVolumePrefix + volumeID
	result, err := r.cli.VolumeInspect(ctx, dockerName, client.VolumeInspectOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker runtime: volume inspect %q: %w", volumeID, err)
	}
	info, ok := volumeToInfo(result.Volume)
	if !ok {
		return nil, fmt.Errorf("docker runtime: volume %q is not edvabe-managed", volumeID)
	}
	return &info, nil
}

// VolumeRemove deletes a managed volume. Returns an error if the volume
// is in use by a running container (Docker returns a conflict error).
func (r *Runtime) VolumeRemove(ctx context.Context, volumeID string) error {
	if volumeID == "" {
		return fmt.Errorf("docker runtime: VolumeRemove: volumeID is required")
	}
	dockerName := DockerVolumePrefix + volumeID
	if _, err := r.cli.VolumeRemove(ctx, dockerName, client.VolumeRemoveOptions{}); err != nil {
		if strings.Contains(err.Error(), "in use") {
			return fmt.Errorf("%w: %s", runtime.ErrVolumeInUse, err.Error())
		}
		return fmt.Errorf("docker runtime: volume remove %q: %w", volumeID, err)
	}
	return nil
}

// volumeToInfo converts a Docker volume.Volume to runtime.VolumeInfo.
// Returns ok=false if the volume lacks edvabe labels.
func volumeToInfo(v volume.Volume) (runtime.VolumeInfo, bool) {
	if v.Labels == nil || v.Labels[LabelVolumeManaged] != "true" {
		return runtime.VolumeInfo{}, false
	}
	createdAt, _ := time.Parse(time.RFC3339Nano, v.CreatedAt)
	return runtime.VolumeInfo{
		VolumeID:   v.Labels[LabelVolumeID],
		Name:       v.Labels[LabelVolumeName],
		DockerName: v.Name,
		CreatedAt:  createdAt,
		Labels:     v.Labels,
	}, true
}
