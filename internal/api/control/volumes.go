package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"

	"github.com/contember/edvabe/internal/api"
	"github.com/contember/edvabe/internal/runtime"
)

// volumeNameValidator checks name constraints per the E2B OpenAPI spec.
// Names must be non-empty and match a DNS_LABEL-compatible character set
// (alphanumeric + hyphens, no leading/trailing hyphen).
func validateVolumeName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// volumeStore is a runtime-backed volume registry. It tracks the E2B
// volume ID → logical name mapping in memory and delegates persistence
// (Docker named volumes with labels) to the runtime. A duplicate-name
// check is kept in-memory because the Docker API does not enforce name
// uniqueness on labels.
type volumeStore struct {
	mu     sync.Mutex
	rt     runtime.Runtime
	byID   map[string]*volumeRecord
	byName map[string]*volumeRecord
}

type volumeRecord struct {
	VolumeID string
	Name     string
}

func newVolumeStore(rt runtime.Runtime) *volumeStore {
	return &volumeStore{
		rt:     rt,
		byID:   make(map[string]*volumeRecord),
		byName: make(map[string]*volumeRecord),
	}
}

// loadFromRuntime rebuilds the in-memory index from Docker-managed
// volumes. Called once at startup so the store survives edvabe
// restarts. No-op when the runtime is nil (test paths that don't
// need volume support).
func (s *volumeStore) loadFromRuntime(ctx context.Context) error {
	if s.rt == nil {
		return nil
	}
	volumes, err := s.rt.VolumeList(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, v := range volumes {
		rec := &volumeRecord{VolumeID: v.VolumeID, Name: v.Name}
		s.byID[v.VolumeID] = rec
		if v.Name != "" {
			s.byName[v.Name] = rec
		}
	}
	return nil
}

type volumeCreateRequest struct {
	Name string `json:"name"`
}

type volumeResponse struct {
	VolumeID string `json:"volumeID"`
	Name     string `json:"name"`
}

type volumeDetailResponse struct {
	VolumeID string `json:"volumeID"`
	Name     string `json:"name"`
	Token    string `json:"token"`
}

func (s *volumeStore) list(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	out := make([]volumeResponse, 0, len(s.byID))
	for _, rec := range s.byID {
		out = append(out, volumeResponse{
			VolumeID: rec.VolumeID,
			Name:     rec.Name,
		})
	}
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, out)
}

func (s *volumeStore) create(w http.ResponseWriter, r *http.Request) {
	var req volumeCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !validateVolumeName(req.Name) {
		api.WriteError(w, http.StatusBadRequest, "invalid volume name")
		return
	}
	s.mu.Lock()
	if _, exists := s.byName[req.Name]; exists {
		s.mu.Unlock()
		api.WriteError(w, http.StatusConflict, "volume name already exists")
		return
	}
	s.mu.Unlock()

	volumeID := "vol_" + randomHex(8)
	if err := s.rt.VolumeCreate(r.Context(), volumeID, req.Name); err != nil {
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.mu.Lock()
	s.byID[volumeID] = &volumeRecord{VolumeID: volumeID, Name: req.Name}
	s.byName[req.Name] = s.byID[volumeID]
	s.mu.Unlock()

	writeJSON(w, http.StatusCreated, volumeResponse{
		VolumeID: volumeID,
		Name:     req.Name,
	})
}

func (s *volumeStore) get(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/volumes/")
	s.mu.Lock()
	rec := s.byID[id]
	if rec == nil {
		rec = s.byName[id]
	}
	s.mu.Unlock()
	if rec == nil {
		api.WriteError(w, http.StatusNotFound, "volume not found")
		return
	}
	writeJSON(w, http.StatusOK, volumeDetailResponse{
		VolumeID: rec.VolumeID,
		Name:     rec.Name,
		Token:    "edvabe-volume-token",
	})
}

func (s *volumeStore) delete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/volumes/")
	s.mu.Lock()
	rec := s.byID[id]
	if rec == nil {
		rec = s.byName[id]
	}
	s.mu.Unlock()
	if rec == nil {
		api.WriteError(w, http.StatusNotFound, "volume not found")
		return
	}
	if err := s.rt.VolumeRemove(r.Context(), rec.VolumeID); err != nil {
		if errors.Is(err, runtime.ErrVolumeInUse) || strings.Contains(err.Error(), "in use") {
			api.WriteError(w, http.StatusConflict, "volume is in use")
			return
		}
		api.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.mu.Lock()
	delete(s.byID, rec.VolumeID)
	delete(s.byName, rec.Name)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// resolveByName finds a volume record by its logical name. Used by
// the create-sandbox handler to validate volumeMounts references.
func (s *volumeStore) resolveByName(name string) (*volumeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.byName[name]
	return rec, ok
}

// dockerNameForVolumeID returns the physical Docker volume name for
// a given E2B volume ID. Used by the create-sandbox handler to build
// runtime.Mount entries.
func dockerNameForVolumeID(volumeID string) string {
	return "edvabe-vol-" + volumeID
}
