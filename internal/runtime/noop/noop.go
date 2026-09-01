// Package noop is an in-memory Runtime used by unit tests of higher
// layers (sandbox manager, control plane). It does not actually start
// anything — it just tracks sandbox IDs and serves back the handles it
// was given.
package noop

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/contember/edvabe/internal/runtime"
)

type entry struct {
	handle   *runtime.SandboxHandle
	image    string
	envs     map[string]string
	labels   map[string]string
	startCmd string
	readyCmd string
	cpuCount int
	memoryMB int
	paused   bool
	stopped  bool
	mounts   []runtime.Mount
}

type volumeEntry struct {
	info  runtime.VolumeInfo
	inUse bool
}

type Runtime struct {
	mu        sync.RWMutex
	sandboxes map[string]*entry
	images    map[string]bool
	volumes   map[string]*volumeEntry
}

func New() *Runtime {
	return &Runtime{
		sandboxes: make(map[string]*entry),
		images:    make(map[string]bool),
		volumes:   make(map[string]*volumeEntry),
	}
}

func (r *Runtime) Name() string { return "noop" }

func (r *Runtime) Create(ctx context.Context, req runtime.CreateRequest) (*runtime.SandboxHandle, error) {
	if req.SandboxID == "" {
		return nil, fmt.Errorf("noop: SandboxID is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sandboxes[req.SandboxID]; exists {
		return nil, fmt.Errorf("noop: sandbox %q already exists", req.SandboxID)
	}
	port := req.AgentPort
	if port == 0 {
		port = 49983
	}
	h := &runtime.SandboxHandle{
		ContainerID: "noop-" + req.SandboxID,
		AgentHost:   "127.0.0.1",
		AgentPort:   port,
		CreatedAt:   time.Now(),
	}
	// Mirror the docker runtime's label-stamping: caller labels plus
	// edvabe.meta.* for user metadata. Keeps the noop runtime faithful
	// so ListManaged-based tests round-trip the same data that a real
	// restart would.
	labels := copyStrMap(req.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range req.Metadata {
		labels[runtime.LabelMetaPrefix+k] = v
	}

	r.sandboxes[req.SandboxID] = &entry{
		handle:   h,
		image:    req.Image,
		envs:     copyStrMap(req.EnvVars),
		labels:   labels,
		startCmd: req.StartCmd,
		readyCmd: req.ReadyCmd,
		cpuCount: req.CPUCount,
		memoryMB: req.MemoryMB,
		mounts:   copyMounts(req.Mounts),
	}
	return h, nil
}

// CPUCount / MemoryMB return the requested cgroup caps so tests can
// assert the resource-limit plumbing reached the runtime. Zero for
// unknown sandboxes — same convention as Image().
func (r *Runtime) CPUCount(sandboxID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.cpuCount
	}
	return 0
}

func (r *Runtime) MemoryMB(sandboxID string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.memoryMB
	}
	return 0
}

// Image returns the image tag the named sandbox was launched with,
// or "" if the sandbox is unknown. Exposed for tests.
func (r *Runtime) Image(sandboxID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.image
	}
	return ""
}

// StartCmd / ReadyCmd return the per-sandbox template commands that
// were forwarded through CreateRequest. Exposed for tests that
// exercise the template resolver path.
func (r *Runtime) StartCmd(sandboxID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.startCmd
	}
	return ""
}

func (r *Runtime) ReadyCmd(sandboxID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return e.readyCmd
	}
	return ""
}

func (r *Runtime) Destroy(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sandboxes[sandboxID]; !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	delete(r.sandboxes, sandboxID)
	return nil
}

func (r *Runtime) Pause(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.paused = true
	return nil
}

func (r *Runtime) Unpause(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.paused = false
	return nil
}

func (r *Runtime) Stop(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.stopped = true
	e.paused = false
	return nil
}

func (r *Runtime) Start(ctx context.Context, sandboxID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	e.stopped = false
	return nil
}

func (r *Runtime) Commit(ctx context.Context, sandboxID, imageTag string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sandboxes[sandboxID]; !ok {
		return fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	r.images[imageTag] = true
	return nil
}

func (r *Runtime) Stats(ctx context.Context, sandboxID string) (*runtime.Stats, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.sandboxes[sandboxID]; !ok {
		return nil, fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	return &runtime.Stats{}, nil
}

func (r *Runtime) BuildImage(ctx context.Context, req runtime.BuildRequest) error {
	if req.Tag == "" {
		return fmt.Errorf("noop: BuildRequest.Tag is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.images[req.Tag] = true
	return nil
}

// ListManaged returns every tracked sandbox as a ManagedContainer.
// Unit tests use this path to exercise Rehydrate logic without a real
// Docker daemon — seed entries with Create first, optionally
// transition them via Pause/Stop.
func (r *Runtime) ListManaged(ctx context.Context) ([]runtime.ManagedContainer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]runtime.ManagedContainer, 0, len(r.sandboxes))
	for id, e := range r.sandboxes {
		state := runtime.ContainerStateRunning
		switch {
		case e.stopped:
			state = runtime.ContainerStateStopped
		case e.paused:
			state = runtime.ContainerStatePaused
		}
		host := e.handle.AgentHost
		if state == runtime.ContainerStateStopped {
			host = ""
		}
		out = append(out, runtime.ManagedContainer{
			SandboxID:   id,
			ContainerID: e.handle.ContainerID,
			Image:       e.image,
			Labels:      copyStrMap(e.labels),
			EnvVars:     copyStrMap(e.envs),
			State:       state,
			CPUCount:    e.cpuCount,
			MemoryMB:    e.memoryMB,
			CreatedAt:   e.handle.CreatedAt,
			AgentHost:   host,
			AgentPort:   e.handle.AgentPort,
		})
	}
	return out, nil
}

func (r *Runtime) AgentEndpoint(sandboxID string) (host string, port int, err error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sandboxes[sandboxID]
	if !ok {
		return "", 0, fmt.Errorf("noop: sandbox %q not found", sandboxID)
	}
	return e.handle.AgentHost, e.handle.AgentPort, nil
}

// HasImage reports whether an image tag has been registered via BuildImage
// or Commit. Exposed for tests that want to assert build plumbing.
func (r *Runtime) HasImage(tag string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.images[tag]
}

// IsPaused is exposed for tests that exercise Pause/Unpause plumbing.
func (r *Runtime) IsPaused(sandboxID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sandboxes[sandboxID]
	return ok && e.paused
}

// IsStopped is exposed for tests that exercise Stop/Start plumbing.
func (r *Runtime) IsStopped(sandboxID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.sandboxes[sandboxID]
	return ok && e.stopped
}

func copyStrMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyMounts(mounts []runtime.Mount) []runtime.Mount {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]runtime.Mount, len(mounts))
	copy(out, mounts)
	return out
}

// Mounts returns the mounts passed to Create for the named sandbox.
func (r *Runtime) Mounts(sandboxID string) []runtime.Mount {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if e, ok := r.sandboxes[sandboxID]; ok {
		return copyMounts(e.mounts)
	}
	return nil
}

func (r *Runtime) VolumeCreate(_ context.Context, volumeID, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.volumes[volumeID]; exists {
		return fmt.Errorf("noop: volume %q already exists", volumeID)
	}
	r.volumes[volumeID] = &volumeEntry{
		info: runtime.VolumeInfo{
			VolumeID:   volumeID,
			Name:       name,
			DockerName: "edvabe-vol-" + volumeID,
			CreatedAt:  time.Now(),
		},
	}
	return nil
}

func (r *Runtime) VolumeList(_ context.Context) ([]runtime.VolumeInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]runtime.VolumeInfo, 0, len(r.volumes))
	for _, v := range r.volumes {
		out = append(out, v.info)
	}
	return out, nil
}

func (r *Runtime) VolumeInspect(_ context.Context, volumeID string) (*runtime.VolumeInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.volumes[volumeID]
	if !ok {
		return nil, fmt.Errorf("noop: volume %q not found", volumeID)
	}
	info := v.info
	return &info, nil
}

func (r *Runtime) VolumeRemove(_ context.Context, volumeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.volumes[volumeID]
	if !ok {
		return fmt.Errorf("noop: volume %q not found", volumeID)
	}
	if v.inUse {
		return fmt.Errorf("noop: volume %q is in use", volumeID)
	}
	delete(r.volumes, volumeID)
	return nil
}

// VolumeSetInUse marks a volume as in-use (for testing conflict scenarios).
func (r *Runtime) VolumeSetInUse(volumeID string, inUse bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if v, ok := r.volumes[volumeID]; ok {
		v.inUse = inUse
	}
}

var _ runtime.Runtime = (*Runtime)(nil)
