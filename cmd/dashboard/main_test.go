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
			name: "absolute home resolves under .agent-receipts",
			home: "/home/test",
			want: filepath.Join("/home/test", ".agent-receipts", "receipts.db"),
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
			userHomeDir = func() (string, error) { return tc.home, tc.homeErr }
			if got := defaultDBPath(); got != tc.want {
				t.Errorf("defaultDBPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
