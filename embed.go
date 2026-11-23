package assets

import "embed"

// Files embeds every released alert-rule Jsonnet template so gen-alerts
// renders PrometheusRule manifests without network or runtime dependencies.
//
//go:embed templates
var Files embed.FS
