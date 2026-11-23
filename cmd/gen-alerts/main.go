// Command gen-alerts renders a Kubernetes PrometheusRule from a
// ServiceObservability manifest, using multi-window multi-burn-rate SLO
// alerting (see README.md).
package main

import (
	"flag"
	"fmt"
	"os"

	assets "github.com/acme/obs-alert-rule-templates"
	"github.com/acme/obs-alert-rule-templates/internal/manifest"
	"github.com/acme/obs-alert-rule-templates/internal/render"
	"go.yaml.in/yaml/v3"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gen-alerts:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("gen-alerts", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "path to the ServiceObservability manifest (required)")
	outputPath := fs.String("output", "", "path to write the generated PrometheusRule YAML (defaults to stdout)")
	extraRulesPath := fs.String("extra-rules", "", "optional standalone extraRules YAML file to merge in addition to spec.alerting.extraRules")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *manifestPath == "" {
		return fmt.Errorf("--manifest is required")
	}

	manifestData, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	m, err := manifest.Parse(manifestData)
	if err != nil {
		return fmt.Errorf("invalid manifest: %w", err)
	}

	var extra []manifest.ExtraRule
	if *extraRulesPath != "" {
		extraData, err := os.ReadFile(*extraRulesPath)
		if err != nil {
			return fmt.Errorf("read extra-rules: %w", err)
		}
		var file render.ExtraRulesFile
		if err := yaml.Unmarshal(extraData, &file); err != nil {
			return fmt.Errorf("parse extra-rules: %w", err)
		}
		extra = file.ExtraRules
	}

	renderer := render.New(assets.Files)
	output, err := renderer.Render(m, extra)
	if err != nil {
		return fmt.Errorf("render alert rules: %w", err)
	}

	if *outputPath == "" {
		_, err = os.Stdout.Write(output)
		return err
	}
	return os.WriteFile(*outputPath, output, 0o644)
}
