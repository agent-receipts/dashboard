package main

import (
	"errors"
	"path/filepath"
	"testing"
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
