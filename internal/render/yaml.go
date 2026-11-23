package render

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

func toYAML(v any) ([]byte, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode YAML: %w", err)
	}
	return out, nil
}
