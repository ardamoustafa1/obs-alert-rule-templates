package render_test

import (
	"os"
	"strings"
	"testing"

	assets "github.com/acme/obs-alert-rule-templates"
	"github.com/acme/obs-alert-rule-templates/internal/manifest"
	"github.com/acme/obs-alert-rule-templates/internal/render"
)

func TestRenderHTTPManifest(t *testing.T) {
	data, err := os.ReadFile("../../testdata/manifests/http.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := render.New(assets.Files).Render(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)

	for _, want := range []string{
		"kind: PrometheusRule",
		"alert: CheckoutApiErrorBudgetBurnFast",
		"alert: CheckoutApiErrorBudgetBurnSlow",
		"alert: CheckoutApiLatencySLOViolation5m",
		"alert: CheckoutApiSaturationHighCpuUtilizationRatio",
		"alert: CheckoutQueueBacklogHigh",
		"runbook_url: https://runbooks.acme.com/http/CheckoutApiErrorBudgetBurnFast",
		`severity: page`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered output missing %q\n---\n%s", want, text)
		}
	}
}

func TestRenderWorkerManifestUsesProcessingDurationMetric(t *testing.T) {
	data, err := os.ReadFile("../../testdata/manifests/worker.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	out, err := render.New(assets.Files).Render(m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "payout_job_duration_seconds_bucket") {
		t.Errorf("expected processingDurationMetric to drive latency rules, got:\n%s", out)
	}
}

func TestRenderMergesExtraRulesFile(t *testing.T) {
	data, err := os.ReadFile("../../testdata/manifests/worker.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	extra := []manifest.ExtraRule{{
		Name:     "PayoutWorkerBacklogHigh",
		Expr:     "payout_queue_depth > 1000",
		Severity: "warning",
	}}
	out, err := render.New(assets.Files).Render(m, extra)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "alert: PayoutWorkerBacklogHigh") {
		t.Errorf("expected merged extra rule in output, got:\n%s", out)
	}
}
