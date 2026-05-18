package main

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-receipts/dashboard/internal/server"
)

func TestDefaultDBPath(t *testing.T) {
	original := userHomeDir
	t.Cleanup(func() { userHomeDir = original })

	tests := []struct {
		name    string
		home    string
		homeErr error
		want    string
	}{
		{
			name: "absolute home resolves under .local/share/agent-receipts",
			home: "/home/test",
			want: filepath.Join("/home/test", ".local", "share", "agent-receipts", "receipts.db"),
		},
		{
			name:    "home lookup error returns empty",
			homeErr: errors.New("no home"),
			want:    "",
		},
		{
			name: "empty home returns empty",
			home: "",
			want: "",
		},
		{
			name: "relative home returns empty",
			home: "relative/path",
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", "") // ensure env var doesn't interfere
			userHomeDir = func() (string, error) { return tc.home, tc.homeErr }
			if got := defaultDBPath(); got != tc.want {
				t.Errorf("defaultDBPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestChoosePollInterval(t *testing.T) {
	envErr := errors.New("boom")
	good := func(d time.Duration) func() (time.Duration, error) {
		return func() (time.Duration, error) { return d, nil }
	}
	bad := func() (time.Duration, error) { return 0, envErr }

	cases := []struct {
		name      string
		flagValue time.Duration
		flagSet   bool
		fromEnv   func() (time.Duration, error)
		want      time.Duration
		wantErr   bool
	}{
		{
			name:      "flag wins over env even when env is broken",
			flagValue: 7 * time.Second,
			flagSet:   true,
			fromEnv:   bad,
			want:      7 * time.Second,
		},
		{
			name:      "flag wins over env when both are valid",
			flagValue: 7 * time.Second,
			flagSet:   true,
			fromEnv:   good(2 * time.Second),
			want:      7 * time.Second,
		},
		{
			name:      "non-positive flag is rejected",
			flagValue: 0,
			flagSet:   true,
			fromEnv:   good(2 * time.Second),
			wantErr:   true,
		},
		{
			name:    "env is used when flag was not set",
			flagSet: false,
			fromEnv: good(3 * time.Second),
			want:    3 * time.Second,
		},
		{
			name:    "env error surfaces when flag was not set",
			flagSet: false,
			fromEnv: bad,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := choosePollInterval(tc.flagValue, tc.flagSet, tc.fromEnv)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestResolvePollInterval(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		want    time.Duration
		wantErr bool
	}{
		{"unset uses server default", "", server.DefaultPollInterval, false},
		{"valid duration is honoured", "2s", 2 * time.Second, false},
		{"sub-second duration is honoured", "250ms", 250 * time.Millisecond, false},
		{"unparseable rejected", "five seconds", 0, true},
		{"zero rejected", "0s", 0, true},
		{"negative rejected", "-1s", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(pollIntervalEnv, tc.env)
			got, err := resolvePollInterval()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %s", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDefaultDBPath_XDGDataHome(t *testing.T) {
	original := userHomeDir
	t.Cleanup(func() { userHomeDir = original })
	userHomeDir = func() (string, error) { return "/home/test", nil }

	tests := []struct {
		name        string
		xdgDataHome string
		want        string
	}{
		{
			name:        "absolute XDG_DATA_HOME is used",
			xdgDataHome: "/custom/data",
			want:        filepath.Join("/custom/data", "agent-receipts", "receipts.db"),
		},
		{
			name:        "relative XDG_DATA_HOME is ignored",
			xdgDataHome: "relative/data",
			want:        filepath.Join("/home/test", ".local", "share", "agent-receipts", "receipts.db"),
		},
		{
			name:        "empty XDG_DATA_HOME falls back to home",
			xdgDataHome: "",
			want:        filepath.Join("/home/test", ".local", "share", "agent-receipts", "receipts.db"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_DATA_HOME", tc.xdgDataHome)
			if got := defaultDBPath(); got != tc.want {
				t.Errorf("defaultDBPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
