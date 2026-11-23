// burnrate.libsonnet
//
// Multi-window, multi-burn-rate PromQL builders, following the Google SRE
// Workbook chapter "Alerting on SLOs":
// https://sre.google/workbook/alerting-on-slos/
//
// Error budget:
//   budget = 1 - availability_target
// e.g. a 99.9% target leaves a 0.1% (0.001) error budget over the SLO window.
//
// Burn rate:
//   burn_rate = observed_error_ratio / budget
// A burn rate of 1 means the service is consuming the error budget exactly
// as fast as the SLO window allows (i.e. it would exhaust the whole budget
// right at the end of the window). A burn rate of 14.4 over a 1h window
// exhausts the entire 30-day budget in about 30d / 14.4 ≈ 50 hours.
//
// Why two windows per alert instead of one threshold check:
//   - A *long* window (e.g. 1h) confirms the burn is sustained, not a blip.
//   - A *short* window (e.g. 5m) makes the alert clear almost immediately
//     once the incident is fixed, instead of hanging around for the length
//     of the long window.
//   Requiring BOTH windows to exceed the threshold (logical AND) is what
//   gives multi-window alerts their two headline properties versus a naive
//   "error rate > 1%" rule:
//     1. Noise reduction: a single 1-minute spike in the short window alone
//        does not page anyone, because the long window filters it out.
//     2. Both fast and slow leaks are caught: a short, severe outage trips
//        the short window fast; a long, low-grade leak trips the long
//        window even though the short window may be intermittently normal.
//
// A single fixed threshold cannot do both at once: set it low enough to
// catch slow leaks and it pages constantly on noise; set it high enough to
// stay quiet and it misses slow leaks until the budget is already gone.

local errorBudget(availabilityTarget) = 1 - availabilityTarget;

// fmtNum renders a float as a clean decimal string for embedding in PromQL.
// Jsonnet's default float-to-string conversion prints raw binary floating
// point noise for values like 14.4 * 0.001 (e.g. "0.0060000000000000001").
// Formatting through '%g' at a fixed significant-digit count keeps generated
// rule expressions human-readable and diffable.
local fmtNum(x) = std.format('%.6g', x);

// selector renders the common {service="...",environment="..."} matcher
// every golden-signal metric in this repo's convention carries.
local selector(service, environment) =
  '{service="%s",environment="%s"}' % [service, environment];

// errorRatioQuery: fraction of requests that errored over `window`.
// clamp_min on the denominator avoids a divide-by-zero NaN when a service
// receives no traffic during the window.
local errorRatioQuery(errorMetric, trafficMetric, service, environment, window) =
  |||
    (
      sum(rate(%(errorMetric)s%(sel)s[%(window)s]))
      /
      clamp_min(sum(rate(%(trafficMetric)s%(sel)s[%(window)s])), 1e-9)
    )
  ||| % {
    errorMetric: errorMetric,
    trafficMetric: trafficMetric,
    sel: selector(service, environment),
    window: window,
  };

// burnRateExpr builds the multi-window, multi-burn-rate PromQL condition:
//   observed_error_ratio(longWindow)  > burnRate * budget
//   AND
//   observed_error_ratio(shortWindow) > burnRate * budget
local burnRateExpr(errorMetric, trafficMetric, service, environment, budget, burnRate, longWindow, shortWindow) =
  local threshold = fmtNum(burnRate * budget);
  std.stripChars(
    |||
      %(long)s > %(threshold)s
      and
      %(short)s > %(threshold)s
    ||| % {
      long: std.rstripChars(errorRatioQuery(errorMetric, trafficMetric, service, environment, longWindow), '\n'),
      short: std.rstripChars(errorRatioQuery(errorMetric, trafficMetric, service, environment, shortWindow), '\n'),
      threshold: threshold,
    },
    '\n'
  );

{
  errorBudget: errorBudget,
  fmtNum: fmtNum,
  selector: selector,
  errorRatioQuery: errorRatioQuery,
  burnRateExpr: burnRateExpr,
}
