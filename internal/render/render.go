package render

import (
	"encoding/json"
	"fmt"
	"io/fs"

	jsonnet "github.com/google/go-jsonnet"

	"github.com/acme/obs-alert-rule-templates/internal/manifest"
)

const templatePath = "templates/alert-rules.libsonnet"

// input is the flattened shape the Jsonnet template consumes. It exists
// separately from manifest.Manifest so the template's contract stays fixed
// even if the manifest schema grows fields alerting does not need.
type input struct {
	Name               string               `json:"name"`
	ServiceType        string               `json:"serviceType"`
	Environment        string               `json:"environment"`
	Team               string               `json:"team"`
	AvailabilityTarget float64              `json:"availability_target"`
	LatencyTargetMS    int                  `json:"latency_target_ms"`
	LatencyPercentile  int                  `json:"latency_percentile"`
	ErrorMetric        string               `json:"errorMetric"`
	TrafficMetric      string               `json:"trafficMetric"`
	LatencyMetric      string               `json:"latencyMetric"`
	SaturationMetrics  []string             `json:"saturationMetrics"`
	ExtraRules         []manifest.ExtraRule `json:"extraRules"`
}

// ExtraRulesFile is the shape of an optional standalone extra-rules file
// (--extra-rules), for teams that would rather not embed spec.alerting.extraRules
// directly in the manifest.
type ExtraRulesFile struct {
	ExtraRules []manifest.ExtraRule `json:"extraRules" yaml:"extraRules"`
}

type Renderer struct {
	files fs.FS
}

func New(files fs.FS) *Renderer {
	return &Renderer{files: files}
}

// Render turns a manifest (plus optional extra alert rules merged from a
// standalone file) into a Kubernetes PrometheusRule YAML document.
func (r *Renderer) Render(m *manifest.Manifest, extraFromFile []manifest.ExtraRule) ([]byte, error) {
	in := input{
		Name:               m.Metadata.Name,
		ServiceType:        m.Spec.ServiceType,
		Environment:        m.Spec.Environment,
		Team:               m.Team(),
		AvailabilityTarget: m.Spec.SLO.AvailabilityTarget,
		LatencyTargetMS:    m.Spec.SLO.LatencyTargetMS,
		LatencyPercentile:  m.Spec.SLO.LatencyPercentile,
		ErrorMetric:        m.Spec.GoldenSignals.ErrorMetric,
		TrafficMetric:      m.Spec.GoldenSignals.TrafficMetric,
		LatencyMetric:      m.LatencyOrProcessingMetric(),
		SaturationMetrics:  m.Spec.GoldenSignals.SaturationMetrics,
		ExtraRules:         append(append([]manifest.ExtraRule{}, m.Spec.Alerting.ExtraRules...), extraFromFile...),
	}
	inputJSON, err := json.Marshal(in)
	if err != nil {
		return nil, fmt.Errorf("encode template input: %w", err)
	}

	template, err := fs.ReadFile(r.files, templatePath)
	if err != nil {
		return nil, fmt.Errorf("read embedded template: %w", err)
	}

	vm := jsonnet.MakeVM()
	vm.Importer(newEmbeddedImporter(r.files, templatePath))
	vm.ExtCode("input", string(inputJSON))
	rendered, err := vm.EvaluateAnonymousSnippet(templatePath, string(template)+".render(std.extVar('input'))")
	if err != nil {
		return nil, fmt.Errorf("evaluate %s: %w", templatePath, err)
	}

	var rule map[string]any
	if err := json.Unmarshal([]byte(rendered), &rule); err != nil {
		return nil, fmt.Errorf("decode rendered PrometheusRule: %w", err)
	}
	return toYAML(rule)
}
