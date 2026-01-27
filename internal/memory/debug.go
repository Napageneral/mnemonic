package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

type DebugConfig struct {
	Dir       string
	EpisodeID string
}

type debugKey struct{}

// WithDebug attaches debug configuration to the context.
func WithDebug(ctx context.Context, cfg DebugConfig) context.Context {
	return context.WithValue(ctx, debugKey{}, cfg)
}

func getDebugConfig(ctx context.Context) (DebugConfig, bool) {
	if ctx == nil {
		return DebugConfig{}, false
	}
	cfg, ok := ctx.Value(debugKey{}).(DebugConfig)
	if !ok || cfg.Dir == "" || cfg.EpisodeID == "" {
		return DebugConfig{}, false
	}
	return cfg, true
}

func writeDebugFile(ctx context.Context, name, content string) {
	cfg, ok := getDebugConfig(ctx)
	if !ok {
		return
	}
	episodeDir := filepath.Join(cfg.Dir, sanitizePathSegment(cfg.EpisodeID))
	if err := os.MkdirAll(episodeDir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(episodeDir, name), []byte(content), 0o644)
}

func sanitizePathSegment(value string) string {
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		" ", "_",
	)
	return replacer.Replace(value)
}
