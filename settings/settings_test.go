package settings

import (
	"testing"
	"time"
)

func TestServerVideoThumbnailWorkers(t *testing.T) {
	tests := []struct {
		name     string
		value    int
		fallback int
		want     int
	}{
		{name: "uses configured value", value: 3, fallback: 1, want: 3},
		{name: "falls back when unset", value: 0, fallback: 1, want: 1},
		{name: "falls back when invalid", value: -1, fallback: 2, want: 2},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{VideoThumbnailWorkers: test.value}
			if got := server.GetVideoThumbnailWorkers(test.fallback); got != test.want {
				t.Fatalf("got %d, want %d", got, test.want)
			}
		})
	}
}

func TestServerVideoThumbnailTimeout(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		fallback time.Duration
		want     time.Duration
	}{
		{name: "uses configured value", value: "45s", fallback: 30 * time.Second, want: 45 * time.Second},
		{name: "falls back when unset", value: "", fallback: 30 * time.Second, want: 30 * time.Second},
		{name: "falls back when invalid", value: "soon", fallback: 30 * time.Second, want: 30 * time.Second},
		{name: "falls back when non-positive", value: "0s", fallback: 30 * time.Second, want: 30 * time.Second},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{VideoThumbnailTimeout: test.value}
			if got := server.GetVideoThumbnailTimeout(test.fallback); got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
		})
	}
}
