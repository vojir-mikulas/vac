package adapter

import (
	"context"

	"github.com/vojir-mikulas/vac/api/internal/compose"
)

// composeAdapter uses a hand-authored compose file — the configured path when
// set (cfg.ComposePath), otherwise auto-detection over the supported filenames.
// A Dockerfile-only repo selected here still gets the generated wrapper, which
// preserves the pre-adapter "auto" behaviour.
type composeAdapter struct{}

func (composeAdapter) Kind() string { return KindCompose }

func (composeAdapter) Prepare(_ context.Context, repoDir string, cfg BuildConfig) (string, error) {
	res, err := compose.DetectAt(repoDir, cfg.ComposePath)
	if err != nil {
		return "", err
	}
	if res.Source == compose.SourceGenerated {
		return compose.Wrap(composeWrapPath(repoDir))
	}
	return res.Path, nil
}
