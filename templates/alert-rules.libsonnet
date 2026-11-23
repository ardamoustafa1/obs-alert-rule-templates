// alert-rules.libsonnet
//
// Parametric PrometheusRule (Prometheus Operator CRD) generator, driven by
// a manifest's SLO targets and golden-signal metric names. See ../README.md
// for the burn-rate math and ./lib/burnrate.libsonnet for the PromQL this
// builds on top of.
local burnrate = import 'lib/burnrate.libsonnet';

// pascalCase turns "checkout-api" into "CheckoutApi" so generated
// `alert:` names are valid, readable Prometheus identifiers.
local pascalCase(s) =
  local parts = std.split(s, '-');
  std.join('', [std.asciiUpper(p[0:1]) + p[1:] for p in parts if p != '']);

// runbookURL implements the fixed convention every alert must satisfy:
// https://runbooks.acme.com/{serviceType}/{alertname}
local runbookURL(serviceType, alertName) =
  'https://runbooks.acme.com/%s/%s' % [serviceType, alertName];

local commonLabels(input, severity, sloTarget) = {
  team: input.team,
  service: input.name,
  environment: input.environment,
  severity: severity,
  slo_target: burnrate.fmtNum(sloTarget),
};

// burnRateGroup builds one alert rule for one (window pair, burn rate)
// tier of the multi-window multi-burn-rate table.
local burnRateRule(input, budget, tier) =
  local name = pascalCase(input.name);
  local alertName = '%sErrorBudgetBurn%s' % [name, tier.suffix];
  {
    alert: alertName,
    expr: burnrate.burnRateExpr(
      input.errorMetric,
      input.trafficMetric,
      input.name,
      input.environment,
      budget,
      tier.burnRate,
      tier.longWindow,
      tier.shortWindow,
    ),
    'for': tier.forDuration,
    labels: commonLabels(input, 'page', input.availability_target) {
      alert_type: 'burn_rate',
      burn_rate_window: '%s/%s' % [tier.longWindow, tier.shortWindow],
    },
    annotations: {
      summary: '%s is burning its error budget %sx faster than sustainable (%s/%s window)' % [input.name, tier.burnRate, tier.longWindow, tier.shortWindow],
      description: 'Observed error ratio over both the %s and %s windows exceeds %sx the allowed burn rate for a %s availability target. At this rate the 30 day error budget empties in about %s.' % [tier.longWindow, tier.shortWindow, tier.burnRate, input.availability_target, tier.exhaustsIn],
      runbook_url: runbookURL(input.serviceType, alertName),
    },
  };

// burnRateTiers: the two paging tiers this repo generates. Windows and
// multipliers follow the Google SRE Workbook's worked example for a 30 day
// SLO window; see README.md for the derivation of `for` and `exhaustsIn`.
local burnRateTiers = [
  {
    suffix: 'Fast',
    longWindow: '1h',
    shortWindow: '5m',
    burnRate: 14.4,
    forDuration: '2m',
    exhaustsIn: '~2 days',
  },
  {
    suffix: 'Slow',
    longWindow: '6h',
    shortWindow: '30m',
    burnRate: 6,
    forDuration: '15m',
    exhaustsIn: '~5 days',
  },
];

// latencyTiers: same PromQL shape (a threshold breach), evaluated over
// progressively longer windows. Severity escalates from warning to page as
// the window grows, because a p_N breach sustained for 6h is far more
// likely to be a systemic regression than transient noise, and by that
// point the failing requests have already accumulated for a long time.
local latencyTiers = [
  { window: '5m', forDuration: '5m', severity: 'warning' },
  { window: '1h', forDuration: '15m', severity: 'warning' },
  { window: '6h', forDuration: '30m', severity: 'page' },
];

local latencyRule(input, tier) =
  local name = pascalCase(input.name);
  local alertName = '%sLatencySLOViolation%s' % [name, tier.window];
  local thresholdSeconds = burnrate.fmtNum(input.latency_target_ms / 1000);
  local sel = burnrate.selector(input.name, input.environment);
  local percentile = burnrate.fmtNum(input.latency_percentile / 100);
  {
    alert: alertName,
    expr: std.stripChars(
      |||
        histogram_quantile(
          %(percentile)s,
          sum(rate(%(metric)s_bucket%(sel)s[%(window)s])) by (le)
        ) > %(threshold)s
      ||| % {
        percentile: percentile,
        metric: input.latencyMetric,
        sel: sel,
        window: tier.window,
        threshold: thresholdSeconds,
      },
      '\n'
    ),
    'for': tier.forDuration,
    labels: commonLabels(input, tier.severity, input.availability_target) {
      alert_type: 'latency_slo',
      latency_window: tier.window,
    },
    annotations: {
      summary: '%s p%s latency exceeds the %sms SLO target over %s' % [input.name, input.latency_percentile, input.latency_target_ms, tier.window],
      description: 'p%s of %s over the last %s is above the %sms target (%ss).' % [input.latency_percentile, input.latencyMetric, tier.window, input.latency_target_ms, thresholdSeconds],
      runbook_url: runbookURL(input.serviceType, alertName),
    },
  };

// saturationRule: early warning before a resource constraint turns into a
// user-facing incident. Ticket severity (not page) because saturation
// alone has not yet breached any SLO -- it is a leading indicator.
//
// Assumption: each entry in saturationMetrics is already a 0..1 utilization
// ratio (e.g. cpu_utilization_ratio, queue_depth_utilization_ratio). Raw
// byte/count gauges need a recording rule to normalize against capacity
// before this template can alert on them meaningfully -- see README.md.
local saturationRule(input, metric) =
  local name = pascalCase(input.name);
  local metricPascal = pascalCase(std.strReplace(metric, '_', '-'));
  local alertName = '%sSaturationHigh%s' % [name, metricPascal];
  local sel = burnrate.selector(input.name, input.environment);
  {
    alert: alertName,
    expr: 'avg_over_time(%s%s[10m]) > 0.8' % [metric, sel],
    'for': '10m',
    labels: commonLabels(input, 'warning', input.availability_target) {
      alert_type: 'saturation',
      saturation_metric: metric,
    },
    annotations: {
      summary: '%s saturation on %s is above 80%%' % [input.name, metric],
      description: '%s has averaged above 80%% utilization on %s for the last 10 minutes. This is a leading indicator, not yet an SLO breach.' % [input.name, metric],
      runbook_url: runbookURL(input.serviceType, alertName),
    },
  };

local extraRule(input, rule) = {
  alert: rule.name,
  expr: rule.expr,
  'for': if std.objectHas(rule, 'for') then rule['for'] else '5m',
  labels: commonLabels(input, rule.severity, input.availability_target) {
    alert_type: 'extra',
  },
  annotations: {
    summary: if std.objectHas(rule, 'summary') then rule.summary else rule.name,
    runbook_url: runbookURL(input.serviceType, rule.name),
  },
};

local render(input) =
  local budget = burnrate.errorBudget(input.availability_target);
  local extraRules = if std.objectHas(input, 'extraRules') then input.extraRules else [];
  local groups = [
    {
      name: '%s.slo.burn-rate' % input.name,
      rules: [burnRateRule(input, budget, tier) for tier in burnRateTiers],
    },
    {
      name: '%s.slo.latency' % input.name,
      rules: [latencyRule(input, tier) for tier in latencyTiers],
    },
    {
      name: '%s.saturation' % input.name,
      rules: [saturationRule(input, m) for m in input.saturationMetrics],
    },
  ] + (
    if std.length(extraRules) > 0 then [{
      name: '%s.extra' % input.name,
      rules: [extraRule(input, r) for r in extraRules],
    }] else []
  );
  {
    apiVersion: 'monitoring.coreos.com/v1',
    kind: 'PrometheusRule',
    metadata: {
      name: '%s-slo-alerts' % input.name,
      labels: {
        team: input.team,
        service: input.name,
        environment: input.environment,
        generator: 'obs-alert-rule-templates',
      },
    },
    spec: {
      groups: groups,
    },
  };

{
  render: render,
}
