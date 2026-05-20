package main

import (
	"math"
	"testing"
)

func TestListenerInitialMaxRunners(t *testing.T) {
	tests := []struct {
		name            string
		configMax       int
		capacityEnabled bool
		want            int
	}{
		{
			name:            "monitor disabled passes configMax through",
			configMax:       5,
			capacityEnabled: false,
			want:            5,
		},
		{
			name:            "monitor disabled with 0 configMax stays 0",
			configMax:       0,
			capacityEnabled: false,
			want:            0,
		},
		{
			name:            "monitor disabled passes MaxInt32 through",
			configMax:       math.MaxInt32,
			capacityEnabled: false,
			want:            math.MaxInt32,
		},
		{
			name:            "monitor enabled seeds at 0 regardless of configMax",
			configMax:       5,
			capacityEnabled: true,
			want:            0,
		},
		{
			name:            "monitor enabled seeds at 0 even when configMax is MaxInt32",
			configMax:       math.MaxInt32,
			capacityEnabled: true,
			want:            0,
		},
		{
			name:            "monitor enabled seeds at 0 even when configMax is already 0",
			configMax:       0,
			capacityEnabled: true,
			want:            0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := listenerInitialMaxRunners(tt.configMax, tt.capacityEnabled)
			if got != tt.want {
				t.Errorf(
					"listenerInitialMaxRunners(configMax=%d, capacityEnabled=%v) = %d, want %d",
					tt.configMax, tt.capacityEnabled, got, tt.want,
				)
			}
		})
	}
}
