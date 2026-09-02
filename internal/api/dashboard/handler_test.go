package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/contember/edvabe/internal/runtime"
	"github.com/contember/edvabe/internal/sandbox"
)

type overviewManager struct {
	sandboxes []*sandbox.Sandbox
}

func (m *overviewManager) List() []*sandbox.Sandbox              { return m.sandboxes }
func (m *overviewManager) Destroy(context.Context, string) error { return nil }
func (m *overviewManager) Pause(context.Context, string) error   { return nil }
func (m *overviewManager) Stop(context.Context, string) error    { return nil }
func (m *overviewManager) Resume(context.Context, string) error  { return nil }
func (m *overviewManager) PausePolicy() sandbox.PausePolicy      { return sandbox.PausePolicy{} }

type overviewStats struct {
	calls []string
}

func (s *overviewStats) StatsOneShot(_ context.Context, sandboxID string) (*runtime.Stats, error) {
	s.calls = append(s.calls, sandboxID)
	return &runtime.Stats{MemoryUsedMB: 128, MemoryLimitMB: 512}, nil
}

func TestOverviewUsesOneShotStatsOnlyForLiveContainers(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	mgr := &overviewManager{sandboxes: []*sandbox.Sandbox{
		{ID: "running", State: sandbox.StateRunning, CreatedAt: now, LastActiveAt: now, Timeout: time.Minute},
		{ID: "frozen", State: sandbox.StatePaused, PauseMode: sandbox.PauseFrozen, CreatedAt: now, LastActiveAt: now, Timeout: time.Minute},
		{ID: "stopped", State: sandbox.StatePaused, PauseMode: sandbox.PauseStopped, CreatedAt: now, LastActiveAt: now, Timeout: time.Minute},
	}}
	stats := &overviewStats{}
	h := NewHandler(HandlerOptions{Manager: mgr, Runtime: stats})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/dashboard/api/overview", nil)

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if len(stats.calls) != 2 || stats.calls[0] != "frozen" || stats.calls[1] != "running" {
		t.Fatalf("StatsOneShot calls = %v, want [frozen running]", stats.calls)
	}
	var got overviewResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Summary.TotalMemoryUsedMiB != 256 {
		t.Fatalf("total memory = %d, want 256", got.Summary.TotalMemoryUsedMiB)
	}
}
