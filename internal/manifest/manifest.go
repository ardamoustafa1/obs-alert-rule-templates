package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// Manifest mirrors the subset of the ServiceObservability schema (v1 and v2,
// see obs-manifest-schema) that alert-rule generation needs. Fields the
// dashboard templates use but alerting does not (e.g. dashboards.template)
// are intentionally left out.
type Manifest struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       Spec     `json:"spec" yaml:"spec"`
}

type Metadata struct {
	Name             string `json:"name" yaml:"name"`
	Owner            string `json:"owner,omitempty" yaml:"owner,omitempty"`
	SlackChannel     string `json:"slack_channel,omitempty" yaml:"slack_channel,omitempty"`
	PagerDutyService string `json:"pagerduty_service,omitempty" yaml:"pagerduty_service,omitempty"`
}

type Spec struct {
	ServiceType   string        `json:"serviceType" yaml:"serviceType"`
	Environment   string        `json:"environment" yaml:"environment"`
	SLO           SLO           `json:"slo" yaml:"slo"`
	GoldenSignals GoldenSignals `json:"goldenSignals" yaml:"goldenSignals"`
	Alerting      Alerting      `json:"alerting,omitempty" yaml:"alerting,omitempty"`
	Ownership     *Ownership    `json:"ownership,omitempty" yaml:"ownership,omitempty"`
}

type SLO struct {
	AvailabilityTarget float64 `json:"availability_target" yaml:"availability_target"`
	LatencyTargetMS    int     `json:"latency_target_ms" yaml:"latency_target_ms"`
	LatencyPercentile  int     `json:"latency_percentile" yaml:"latency_percentile"`
}

type GoldenSignals struct {
	TrafficMetric            string   `json:"trafficMetric" yaml:"trafficMetric"`
	ErrorMetric              string   `json:"errorMetric" yaml:"errorMetric"`
	LatencyMetric            string   `json:"latencyMetric,omitempty" yaml:"latencyMetric,omitempty"`
	ProcessingDurationMetric string   `json:"processingDurationMetric,omitempty" yaml:"processingDurationMetric,omitempty"`
	SaturationMetrics        []string `json:"saturationMetrics" yaml:"saturationMetrics"`
}

type Alerting struct {
	BurnRateAlerts bool        `json:"burnRateAlerts" yaml:"burnRateAlerts"`
	ExtraRules     []ExtraRule `json:"extraRules,omitempty" yaml:"extraRules,omitempty"`
}

type ExtraRule struct {
	Name     string `json:"name" yaml:"name"`
	Expr     string `json:"expr" yaml:"expr"`
	For      string `json:"for,omitempty" yaml:"for,omitempty"`
	Severity string `json:"severity" yaml:"severity"`
	Summary  string `json:"summary,omitempty" yaml:"summary,omitempty"`
}

type Ownership struct {
	Team             string `json:"team" yaml:"team"`
	SlackChannel     string `json:"slackChannel" yaml:"slackChannel"`
	PagerDutyService string `json:"pagerDutyService" yaml:"pagerDutyService"`
}

// Team returns the owning team name regardless of manifest version:
// v1 carries it as metadata.owner, v2 as spec.ownership.team.
func (m *Manifest) Team() string {
	if m.Spec.Ownership != nil && m.Spec.Ownership.Team != "" {
		return m.Spec.Ownership.Team
	}
	return m.Metadata.Owner
}

// PagerDutyService returns the PagerDuty routing key regardless of manifest version.
func (m *Manifest) PagerDutyService() string {
	if m.Spec.Ownership != nil && m.Spec.Ownership.PagerDutyService != "" {
		return m.Spec.Ownership.PagerDutyService
	}
	return m.Metadata.PagerDutyService
}

// SlackChannel returns the ticket/notification channel regardless of manifest version.
func (m *Manifest) SlackChannel() string {
	if m.Spec.Ownership != nil && m.Spec.Ownership.SlackChannel != "" {
		return m.Spec.Ownership.SlackChannel
	}
	return m.Metadata.SlackChannel
}

func Parse(data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(false) // alerting/dashboards carry fields this repo does not model
	var m Manifest
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest YAML: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, errors.New("manifest must contain exactly one YAML document")
	} else if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse trailing YAML: %w", err)
	}
	if err := validate(&m); err != nil {
		return nil, err
	}
	return &m, nil
}

func validate(m *Manifest) error {
	if m.APIVersion != "obs.acme.com/v1" && m.APIVersion != "obs.acme.com/v2" {
		return fmt.Errorf("unsupported apiVersion %q", m.APIVersion)
	}
	if m.Kind != "ServiceObservability" {
		return fmt.Errorf("kind must be ServiceObservability, got %q", m.Kind)
	}
	if m.Metadata.Name == "" {
		return errors.New("metadata.name is required")
	}
	if m.Team() == "" {
		return errors.New("owning team is required (metadata.owner for v1, spec.ownership.team for v2)")
	}
	if m.Spec.Environment == "" {
		return errors.New("spec.environment is required")
	}
	if m.Spec.SLO.AvailabilityTarget <= 0 || m.Spec.SLO.AvailabilityTarget >= 1 {
		return errors.New("spec.slo.availability_target must be in (0, 1)")
	}
	if m.Spec.SLO.LatencyTargetMS <= 0 {
		return errors.New("spec.slo.latency_target_ms must be positive")
	}
	validPercentile := map[int]bool{50: true, 90: true, 95: true, 99: true}
	if !validPercentile[m.Spec.SLO.LatencyPercentile] {
		return errors.New("spec.slo.latency_percentile must be one of 50, 90, 95, or 99")
	}
	if m.Spec.GoldenSignals.TrafficMetric == "" || m.Spec.GoldenSignals.ErrorMetric == "" {
		return errors.New("goldenSignals.trafficMetric and goldenSignals.errorMetric are required")
	}
	if len(m.Spec.GoldenSignals.SaturationMetrics) == 0 {
		return errors.New("at least one goldenSignals.saturationMetrics entry is required")
	}
	workerLike := m.Spec.ServiceType == "worker" || m.Spec.ServiceType == "batch"
	if workerLike && m.Spec.GoldenSignals.ProcessingDurationMetric == "" {
		return errors.New("processingDurationMetric is required for worker and batch service types")
	}
	if !workerLike && m.Spec.GoldenSignals.LatencyMetric == "" {
		return errors.New("latencyMetric is required for http, grpc, and database service types")
	}
	for i, r := range m.Spec.Alerting.ExtraRules {
		if r.Name == "" || r.Expr == "" || r.Severity == "" {
			return fmt.Errorf("spec.alerting.extraRules[%d]: name, expr and severity are required", i)
		}
	}
	return nil
}

// LatencyOrProcessingMetric returns whichever of latencyMetric /
// processingDurationMetric the manifest's serviceType populates.
func (m *Manifest) LatencyOrProcessingMetric() string {
	if m.Spec.GoldenSignals.LatencyMetric != "" {
		return m.Spec.GoldenSignals.LatencyMetric
	}
	return m.Spec.GoldenSignals.ProcessingDurationMetric
}
