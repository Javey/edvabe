package docker

import (
	"slices"
	"testing"
)

func TestParseSecurityOpt(t *testing.T) {
	cases := []struct {
		name      string
		raw       string
		wantOpts  []string
		wantSysUC bool
	}{
		{"empty", "", nil, false},
		{"single", "seccomp=unconfined", []string{"seccomp=unconfined"}, false},
		{"blanks trimmed", " seccomp=unconfined , , apparmor=unconfined ", []string{"seccomp=unconfined", "apparmor=unconfined"}, false},
		{"systempaths consumed", "seccomp=unconfined,apparmor=unconfined,systempaths=unconfined", []string{"seccomp=unconfined", "apparmor=unconfined"}, true},
		{"systempaths only", "systempaths=unconfined", nil, true},
		{"systempaths case-insensitive", "SystemPaths=Unconfined", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, sysUC := parseSecurityOpt(tc.raw)
			if !slices.Equal(opts, tc.wantOpts) {
				t.Errorf("opts = %#v, want %#v", opts, tc.wantOpts)
			}
			if sysUC != tc.wantSysUC {
				t.Errorf("systempathsUnconfined = %v, want %v", sysUC, tc.wantSysUC)
			}
		})
	}
}

func TestNormalizeCapAdd(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"trims and dedupes", []string{" NET_ADMIN ", "NET_ADMIN", "", "SYS_PTRACE"}, []string{"NET_ADMIN", "SYS_PTRACE"}},
		{"all blank", []string{"", "  "}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeCapAdd(tc.in); !slices.Equal(got, tc.want) {
				t.Errorf("normalizeCapAdd(%#v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDiscoverHostHonorsDockerHostEnv(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://example.invalid:2375")
	host, err := DiscoverHost()
	if err != nil {
		t.Fatalf("DiscoverHost: %v", err)
	}
	if host != "tcp://example.invalid:2375" {
		t.Errorf("DiscoverHost = %q, want tcp://example.invalid:2375", host)
	}
}

func TestPauseUnpauseCommitArgValidation(t *testing.T) {
	// Empty-arg validation runs before any Docker client call, so
	// these assertions exercise the validation path without needing a
	// daemon connection.
	r := &Runtime{}
	ctx := t.Context()
	if err := r.Pause(ctx, ""); err == nil {
		t.Error("Pause(\"\") should be rejected")
	}
	if err := r.Unpause(ctx, ""); err == nil {
		t.Error("Unpause(\"\") should be rejected")
	}
	if err := r.Commit(ctx, "", "edvabe/snap:v1"); err == nil {
		t.Error("Commit(\"\", tag) should be rejected")
	}
	if err := r.Commit(ctx, "isb_x", ""); err == nil {
		t.Error("Commit(id, \"\") should be rejected")
	}
}
