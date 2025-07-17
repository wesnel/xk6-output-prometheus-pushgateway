package extension

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/wesnel/xk6-output-prometheus-pushgateway/pkg/extension/configuration"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/push"
	"github.com/sirupsen/logrus"
	"go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

type Registerer interface {
	prometheus.Registerer
	prometheus.Gatherer
}

// Output implements the lib.Output interface
type Output struct {
	output.SampleBuffer

	logger logrus.FieldLogger

	config configuration.Config

	collectors map[string]collector
	pusher     *push.Pusher

	periodicFlusher *output.PeriodicFlusher

	queue   chan []metrics.SampleContainer
	stop    chan struct{}
	stopped chan struct{}
	once    *sync.Once
}

type collector struct {
	collector prometheus.Collector
	labels    map[string]string
}

type metric interface {
	add(metrics.Sample)
	value() float64
}

type counter struct {
	sink *metrics.CounterSink
}

func (c *counter) add(sample metrics.Sample) {
	c.sink.Add(sample)
}

func (c *counter) value() float64 {
	return c.sink.Format(0)["count"]
}

type gauge struct {
	sink *metrics.GaugeSink
}

func (g *gauge) add(sample metrics.Sample) {
	g.sink.Add(sample)
}

func (g *gauge) value() float64 {
	return g.sink.Format(0)["value"]
}

type rate struct {
	sink *metrics.RateSink
}

func (r *rate) add(sample metrics.Sample) {
	r.sink.Add(sample)
}

func (r *rate) value() float64 {
	return r.sink.Format(0)["rate"]
}

type trend struct {
	sink   *metrics.TrendSink
	sample metrics.Sample
}

func (t *trend) add(sample metrics.Sample) {
	t.sink.Add(sample)
}

func (t *trend) value() float64 {
	return t.sample.Value
}

// TODO: We should use WithBuiltinMetrics instead, and we can modify
// the built-in metrics to fit the format expected by the default K6
// Grafana dashboard.
//
// var _ output.WithBuiltinMetrics = new(Output)
var _ output.Output = new(Output)

var (
	ErrAddingMetricDataPoint = errors.New("error adding metric data point")
	ErrPushingMetrics        = errors.New("error pushing metrics")
)

// New creates an instance of the collector
func New(
	params output.Params,
	registerer Registerer,
) (*Output, error) {
	config, err := configuration.New(params)
	if err != nil {
		return nil, fmt.Errorf("error building config: %w", err)
	}

	collectors := make(map[string]collector, len(config.Metrics.Definitions))
	for name, metric := range config.Metrics.Definitions {
		name = config.Metrics.Prefix + name

		c, err := metric.Collector(name)
		if err != nil {
			return nil, fmt.Errorf("invalid metric configuration: %w", err)
		}

		if err := registerer.Register(c); err != nil {
			return nil, fmt.Errorf("error registering metric: %w", err)
		}

		labels := make(map[string]string, len(metric.Labels))
		for _, label := range metric.Labels {
			labels[label] = ""
		}

		collectors[name] = collector{
			collector: c,
			labels:    labels,
		}
	}

	var once sync.Once

	return &Output{
		collectors: collectors,
		config:     *config,
		logger:     params.Logger,
		once:       &once,
		queue:      make(chan []metrics.SampleContainer),
		stop:       make(chan struct{}),
		stopped:    make(chan struct{}),

		pusher: push.
			New(config.PushGatewayURL, config.JobName).
			Gatherer(registerer).
			Format(config.PushFormat),
	}, nil
}

// Description returns a human-readable description of the output that will be shown in `k6 run`.
func (o *Output) Description() string {
	return fmt.Sprintf("prometheus: %s", o.config.PushGatewayURL)
}

// Start performs initialization tasks prior to Engine using the output.
func (o *Output) Start() error {
	o.logger.Debug("Starting...")

	go o.listen()

	pf, err := output.NewPeriodicFlusher(o.config.PushInterval, o.flush)
	if err != nil {
		return err
	}
	o.periodicFlusher = pf

	o.logger.Debug("Started!")
	return nil
}

// Stop flushes all remaining metrics and finalizes the test run.
func (o *Output) Stop() error {
	o.logger.Debug("Stopping...")
	defer o.logger.Debug("Stopped!")

	o.once.Do(func() {
		// Stop the flusher first in order to guarantee no writes to closed channel:
		o.periodicFlusher.Stop()

		close(o.queue)
		close(o.stop)
	})

	// Wait for the listener goroutine to respond to our call to stop:
	<-o.stopped
	return nil
}

func (o *Output) listen() {
	o.logger.Debug("Listening...")
	defer o.logger.Debug("Stopped listening!")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		select {
		case samples := <-o.queue:
			// FIXME: push seems to be called multiple times per flush.
			o.push(ctx, samples)
		case <-o.stop:
			close(o.stopped)
			return
		}
	}
}

func (o *Output) flush() {
	o.logger.Debug("Flushing...")
	defer o.logger.Debug("Done flushing!")

	o.queue <- o.GetBufferedSamples()
}

func (o *Output) push(
	ctx context.Context,
	containers []metrics.SampleContainer,
) {
	o.logger.Debug("Pushing...")
	defer o.logger.Debug("Done pushing!")

	// Inspired by:
	//
	// https://github.com/grafana/k6/blob/20b553ca51a27400daf24197dab72d47a9a0ab3c/internal/output/prometheusrw/remotewrite/remotewrite.go#L236
	aggregated := make(map[metrics.TimeSeries]metric)
	for _, container := range containers {
		samples := container.GetSamples()

		for _, sample := range samples {
			_, found := aggregated[sample.TimeSeries]
			if !found {
				switch sample.Metric.Type {
				case metrics.Counter:
					aggregated[sample.TimeSeries] = &counter{
						sink: &metrics.CounterSink{},
					}

				case metrics.Gauge:
					aggregated[sample.TimeSeries] = &gauge{
						sink: &metrics.GaugeSink{},
					}

				case metrics.Rate:
					aggregated[sample.TimeSeries] = &rate{
						sink: &metrics.RateSink{},
					}

				case metrics.Trend:
					aggregated[sample.TimeSeries] = &trend{
						sample: sample,

						// HACK: We won't actually use this trend sink, but
						// we will track it anyways in order to prevent a
						// nil pointer without complicating the code
						// elsewhere:
						sink: &metrics.TrendSink{},
					}

					// HACK: Histograms should just be observed immediately
					// rather than aggregated into a sink and observed later:
					if err := o.add(
						sample.TimeSeries,
						aggregated[sample.TimeSeries],
					); err != nil {
						o.logger.
							WithError(fmt.Errorf("%w: %w", ErrAddingMetricDataPoint, err)).
							Error("Error adding metric data point")
					}
				}
			}

			aggregated[sample.TimeSeries].add(sample)
		}
	}

	for t, m := range aggregated {
		if err := o.add(t, m); err != nil {
			o.logger.
				WithError(fmt.Errorf("%w: %w", ErrAddingMetricDataPoint, err)).
				Error("Error adding metric data point")
		}
	}

	if err := o.pusher.PushContext(ctx); err != nil {
		o.logger.
			WithError(fmt.Errorf("%w: %w", ErrPushingMetrics, err)).
			Error("Error pushing metrics")
	}
}

func (o *Output) add(
	t metrics.TimeSeries,
	m metric,
) error {
	name := o.config.Metrics.Prefix + t.Metric.Name

	c, found := o.collectors[name]
	if !found {
		return fmt.Errorf("unknown metric %s", name)
	}

	switch m.(type) {
	case *counter:
		metric, ok := c.collector.(*prometheus.CounterVec)
		if !ok {
			return fmt.Errorf("metric %s is not a counter", name)
		}

		labels := maps.Clone(c.labels)
		maps.Copy(labels, t.Tags.Map())

		counter, err := metric.GetMetricWith(labels)
		if err != nil {
			return fmt.Errorf("invalid labels for %s: %w", name, err)
		}

		counter.Add(m.value())
	case *gauge:
		metric, ok := c.collector.(*prometheus.GaugeVec)
		if !ok {
			return fmt.Errorf("metric %s is not a gauge", name)
		}

		labels := maps.Clone(c.labels)
		maps.Copy(labels, t.Tags.Map())

		gauge, err := metric.GetMetricWith(labels)
		if err != nil {
			return fmt.Errorf("invalid labels for %s: %w", name, err)
		}

		gauge.Set(m.value())
	case *rate:
		metric, ok := c.collector.(*prometheus.GaugeVec)
		if !ok {
			return fmt.Errorf("metric %s is not a gauge", name)
		}

		labels := maps.Clone(c.labels)
		maps.Copy(labels, t.Tags.Map())

		gauge, err := metric.GetMetricWith(labels)
		if err != nil {
			return fmt.Errorf("invalid labels for %s: %w", name, err)
		}

		gauge.Set(m.value())
	case *trend:
		metric, ok := c.collector.(*prometheus.HistogramVec)
		if !ok {
			return fmt.Errorf("metric %s is not a histogram", name)
		}

		labels := maps.Clone(c.labels)
		maps.Copy(labels, t.Tags.Map())

		observer, err := metric.GetMetricWith(labels)
		if err != nil {
			return fmt.Errorf("invalid labels for %s: %w", name, err)
		}

		observer.Observe(m.value())
	}

	return nil
}
