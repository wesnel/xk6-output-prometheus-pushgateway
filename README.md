# xk6-output-prometheus-pushgateway

A [k6](https://k6.io/) output extension that pushes test metrics to a
[Prometheus Pushgateway](https://prometheus.io/docs/instrumenting/pushing/).

This extension maps k6 metric types to native Prometheus collectors and
periodically pushes them to a Pushgateway during (and at the end of) a test
run.

> **Breaking rewrite.** This is a complete, from-scratch rewrite of
> [martymarron/xk6-output-prometheus-pushgateway](https://github.com/martymarron/xk6-output-prometheus-pushgateway).
> The configuration format, metric handling, and internal architecture are all
> different. See [Differences from the original project](#differences-from-the-original-project)
> below.

## Metric type mapping

| k6 type   | Prometheus type  |
|-----------|------------------|
| Counter   | `CounterVec`     |
| Gauge     | `GaugeVec`       |
| Rate      | `GaugeVec`       |
| Trend     | `HistogramVec`   |

## Building

Use [xk6](https://github.com/grafana/xk6) to build a k6 binary with this
extension:

```bash
xk6 build --with github.com/wesnel/xk6-output-prometheus-pushgateway@latest
```

## Configuration

Configuration is provided primarily through the `options.ext` block of your k6
script. Environment variables can override individual fields.

### Script options

Add a JSON object under the key `xk6-output-prometheus-pushgateway` inside
`options.ext`:

```javascript
export const options = {
  ext: {
    "xk6-output-prometheus-pushgateway": {
      push_gateway_url: "http://localhost:9091",
      job_name: "k6_load_test",
      push_interval: "10s",
      push_format: "text/plain; version=0.0.4; charset=utf-8",
      metrics: {
        prefix: "k6_",
        definitions: {
          http_reqs: {
            type: "counter",
            labels: ["expected_response", "method", "name", "status", "url"],
          },
          http_req_duration: {
            type: "trend",
            labels: ["expected_response", "method", "name", "status", "url"],
            buckets: [10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000],
          },
          http_req_failed: {
            type: "rate",
            labels: ["expected_response", "method", "name", "status", "url"],
          },
          vus: {
            type: "gauge",
            labels: [],
          },
        },
      },
    },
  },
};
```

### Environment variable overrides

| Variable             | Overrides            | Example                         |
|----------------------|----------------------|---------------------------------|
| `K6_PUSHGATEWAY_URL` | `push_gateway_url`  | `http://pushgateway:9091`       |
| `K6_JOB_NAME`        | `job_name`          | `k6_load_test`                  |
| `K6_PUSH_INTERVAL`   | `push_interval`     | `15s`                           |
| `K6_PUSH_FORMAT`     | `push_format`       | `text/plain; version=0.0.4; charset=utf-8` |

### Defaults

| Field           | Default                                                |
|-----------------|--------------------------------------------------------|
| `push_interval` | `10s`                                                  |
| `push_format`   | `application/vnd.google.protobuf; proto=io.prometheus.client.MetricFamily; encoding=delimited` |

### Metrics configuration

Every metric you want to collect **must** be declared in `metrics.definitions`.
Only declared metrics are registered with Prometheus and pushed; samples for
undeclared metrics are silently dropped.

Each definition accepts:

| Field     | Required | Description |
|-----------|----------|-------------|
| `type`    | yes      | k6 metric type: `counter`, `gauge`, `rate`, or `trend`. |
| `labels`  | no       | Label names. Values are populated from k6 tags at runtime. |
| `buckets` | no       | Histogram bucket boundaries (only used for `trend` metrics). |

An optional `prefix` string is prepended to every metric name. For example,
with `prefix: "k6_"` a definition keyed `http_reqs` registers the Prometheus
metric `k6_http_reqs`.

## Usage

Run k6 with the `--out` flag:

```bash
K6_PUSHGATEWAY_URL=http://localhost:9091 \
K6_JOB_NAME=my_test \
./k6 run --out xk6-output-prometheus-pushgateway script.js
```

### Full example

```javascript
// script.js
import http from "k6/http";
import { check, sleep } from "k6";

export const options = {
  vus: 10,
  duration: "30s",
  ext: {
    "xk6-output-prometheus-pushgateway": {
      push_gateway_url: "http://localhost:9091",
      job_name: "k6_example",
      push_interval: "5s",
      metrics: {
        prefix: "k6_",
        definitions: {
          http_reqs: {
            type: "counter",
            labels: ["expected_response", "method", "name", "status"],
          },
          http_req_duration: {
            type: "trend",
            labels: ["expected_response", "method", "name", "status"],
            buckets: [50, 100, 200, 500, 1000, 2000, 5000],
          },
          http_req_failed: {
            type: "rate",
            labels: ["expected_response", "method", "name", "status"],
          },
          vus: {
            type: "gauge",
            labels: [],
          },
          iterations: {
            type: "counter",
            labels: [],
          },
        },
      },
    },
  },
};

export default function () {
  const res = http.get("https://test.k6.io");
  check(res, {
    "status is 200": (r) => r.status === 200,
  });
  sleep(1);
}
```

```bash
./k6 run --out xk6-output-prometheus-pushgateway script.js
```

## Differences from the original project

This is a **complete, breaking rewrite** of
[martymarron/xk6-output-prometheus-pushgateway](https://github.com/martymarron/xk6-output-prometheus-pushgateway).
It is not backwards-compatible. The following table summarizes the key
differences:

| Aspect | Original (`martymarron`) | This rewrite (`wesnel`) |
|---|---|---|
| **k6 version** | v0.49.0 | v1.1.0 |
| **Extension name** | `output-prometheus-pushgateway` | `xk6-output-prometheus-pushgateway` |
| **Configuration** | Environment variables only (`K6_PUSHGATEWAY_URL`, `K6_JOB_NAME`, `K6_PUSH_INTERVAL`, `K6_PUSHGATEWAY_NAMESPACE`); labels via `options.ext.pushgateway` or `K6_LABEL_*` env vars | Full JSON config in `options.ext["xk6-output-prometheus-pushgateway"]` with env var overrides |
| **Metric discovery** | Automatic -- all k6 metrics are collected | Explicit -- only metrics declared in `metrics.definitions` are collected |
| **Labels** | Global `ConstLabels` applied to every metric | Per-metric label names declared in config; values populated from k6 tags at runtime |
| **Trend metrics** | Exploded into 6 separate Prometheus Gauges (`_min`, `_max`, `_avg`, `_med`, `_p90`, `_p95`) | Native Prometheus `HistogramVec` with configurable bucket boundaries |
| **Counter metrics** | `CounterFunc` reading the sink's computed rate | `CounterVec` with `Add()`, reporting the raw count |
| **Gauge / Rate metrics** | `GaugeFunc` reading the raw sample value | `GaugeVec` with `Set()`, reading from the aggregated sink |
| **Registry lifecycle** | New ephemeral registry created on every flush | Single persistent registry created at startup |
| **Pusher lifecycle** | New pusher created on every flush | Single persistent pusher created at startup |
| **Push method** | `pusher.Add()` (appends to existing metrics) | `pusher.PushContext(ctx)` (replaces metrics for the job) |
| **Push format** | Not configurable (default) | Configurable via `push_format` / `K6_PUSH_FORMAT` |
| **Sample aggregation** | Deduplicated by metric name only (last sample wins) | Aggregated by full `TimeSeries` (metric name + tags), using k6 sinks |
| **Concurrency model** | New goroutine per flush cycle with `sync.WaitGroup` | Dedicated listener goroutine with channel-based queue |
| **Validation** | None | Struct-level validation via `go-playground/validator` |

### Why the rewrite?

The original project's approach of creating fresh registries and collectors on
every flush cycle, deduplicating samples by metric name only, and representing
Trend metrics as flat gauges made it difficult to get accurate, high-cardinality
metrics into Prometheus. This rewrite addresses those limitations by:

- **Declaring metrics up front** so that Prometheus collectors are registered
  once and reused, avoiding the overhead and data loss of ephemeral registries.
- **Using native Prometheus Histograms for Trend metrics** so that percentiles
  can be computed server-side by PromQL rather than being pre-computed and
  frozen at push time.
- **Aggregating by full time series** (metric + tags) instead of by metric name
  alone, so that label cardinality is preserved.
- **Supporting per-metric labels** populated from k6 tags, rather than global
  `ConstLabels` applied uniformly to all metrics.

## License

See [LICENSE](LICENSE).
