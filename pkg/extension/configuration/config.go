package configuration

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

const defaultPushInterval = time.Duration(10) * time.Second

// Config holds the settings for the Prometheus Pushgateway output extension,
// including the Pushgateway URL, push interval, serialization format, job name,
// and metric definitions. It is parsed from the k6 script's options.ext block
// and environment variables.
type Config struct {
	PushGatewayURL string               `json:"push_gateway_url" validate:"required,url"`
	PushInterval   time.Duration        `json:"push_interval" validate:"required"`
	PushFormat     expfmt.Format        `json:"push_format" validate:"required"`
	JobName        string               `json:"job_name" validate:"required"`
	Metrics        MetricSpecifications `json:"metrics" validate:"required"`
}

// MetricSpecifications defines the set of Prometheus metrics to register,
// including an optional common prefix and a map of metric names to their options.
type MetricSpecifications struct {
	Prefix      string                   `json:"prefix"`
	Definitions map[string]MetricOptions `json:"definitions"`
}

// MetricOptions configures a single Prometheus metric, specifying its k6 metric
// type (counter, gauge, rate, or trend), label names, and optional histogram
// buckets for trend metrics.
type MetricOptions struct {
	Type   metrics.MetricType `json:"type" validate:"required"`
	Labels []string           `json:"labels"`

	// HACK: Buckets are only used by trend/histogram metrics.
	Buckets []float64 `json:"buckets"`
}

// New builds a Config from k6 output.Params by first unmarshalling the JSON
// configuration from options.ext["xk6-output-prometheus-pushgateway"], then
// applying overrides from environment variables (K6_PUSHGATEWAY_URL,
// K6_JOB_NAME, K6_PUSH_INTERVAL, K6_PUSH_FORMAT), filling in defaults for
// unset fields, and finally validating the result.
func New(params output.Params) (*Config, error) {
	var config *Config
	if raw, ok := params.ScriptOptions.External["xk6-output-prometheus-pushgateway"]; ok {
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, fmt.Errorf("invalid configuration for options.ext.xk6-output-prometheus-pushgateway: %w", err)
		}
	}

	if pushGatewayURL := params.Environment["K6_PUSHGATEWAY_URL"]; pushGatewayURL != "" {
		config.PushGatewayURL = pushGatewayURL
	}

	if jobName := params.Environment["K6_JOB_NAME"]; jobName != "" {
		config.JobName = jobName
	}

	if pushInterval := params.Environment["K6_PUSH_INTERVAL"]; pushInterval != "" {
		parsed, err := time.ParseDuration(pushInterval)
		if err != nil {
			return nil, fmt.Errorf("invalid push interval: %w", err)
		}

		config.PushInterval = parsed
	}

	if config.PushInterval == 0 {
		config.PushInterval = defaultPushInterval
	}

	if pushFormat := params.Environment["K6_PUSH_FORMAT"]; pushFormat != "" {
		config.PushFormat = expfmt.Format(pushFormat)
	}

	if config.PushFormat == "" {
		config.PushFormat = expfmt.FmtProtoDelim
	}

	validate := validator.New(validator.WithRequiredStructEnabled())
	if err := validate.Struct(config); err != nil {
		return nil, fmt.Errorf("invalid configuration for options.ext.xk6-output-prometheus-pushgateway: %w", err)
	}

	return config, nil
}

// Collector creates a prometheus.Collector for the given metric name based on
// the k6 metric type: Counter becomes a CounterVec, Gauge and Rate become a
// GaugeVec, and Trend becomes a HistogramVec using the configured Buckets.
func (s MetricOptions) Collector(name string) (prometheus.Collector, error) {
	// TODO: Fill out metric opts using metric specification.
	switch s.Type {
	case metrics.Counter:
		return prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: name,
		}, s.Labels), nil
	case metrics.Gauge:
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: name,
		}, s.Labels), nil
	case metrics.Rate:
		return prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: name,
		}, s.Labels), nil
	case metrics.Trend:
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    name,
			Buckets: s.Buckets,
		}, s.Labels), nil
	default:
		return nil, fmt.Errorf(`invalid type "%s" for metric "%s"`, s.Type, name)
	}
}
