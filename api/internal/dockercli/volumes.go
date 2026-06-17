package dockercli

import (
	"context"
	"encoding/json"
	"fmt"
)

// Mount is one entry of a container's `.Mounts` array, as read by ContainerMounts.
// For a named volume Type is "volume" and Name is the volume name; for a host bind
// Type is "bind", Name is empty, and Source is the host path. Destination is the
// path inside the container in both cases.
type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
}

// dfVolume is one element of `docker system df -v --format '{{json .Volumes}}'`.
// UsageData.Size is the volume's on-disk size in bytes; it is -1 when docker did
// not compute it (only the verbose `-v` form computes per-volume sizes).
type dfVolume struct {
	Name      string `json:"Name"`
	UsageData struct {
		Size int64 `json:"Size"`
	} `json:"UsageData"`
}

// VolumeSizes returns the on-disk size in bytes of every named volume, keyed by
// volume name, via a single `docker system df -v`. Volumes whose size docker
// could not compute (Size < 0) are omitted. This is the cheap path for named
// volumes — one daemon call covers them all, no per-volume `du`.
func (c *Compose) VolumeSizes(ctx context.Context) (map[string]int64, error) {
	cmd := c.command(ctx, "", "system", "df", "-v", "--format", "{{json .Volumes}}")
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	var vols []dfVolume
	if err := json.Unmarshal(out, &vols); err != nil {
		return nil, fmt.Errorf("dockercli: parse volume sizes: %w", err)
	}
	sizes := make(map[string]int64, len(vols))
	for _, v := range vols {
		if v.Name == "" || v.UsageData.Size < 0 {
			continue
		}
		sizes[v.Name] = v.UsageData.Size
	}
	return sizes, nil
}

// ContainerMounts returns a container's volume and bind mounts via a cheap
// `docker inspect -f '{{json .Mounts}}'`. Used by the diskusage collector to map
// each volume back to the owning service's mount path.
func (c *Compose) ContainerMounts(ctx context.Context, id string) ([]Mount, error) {
	cmd := c.command(ctx, "", "inspect", "-f", "{{json .Mounts}}", id)
	out, err := cmd.Output()
	if err != nil {
		return nil, mapCmdError(err, out)
	}
	var mounts []Mount
	if err := json.Unmarshal(out, &mounts); err != nil {
		return nil, fmt.Errorf("dockercli: parse mounts: %w", err)
	}
	return mounts, nil
}
