package extension_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wesnel/xk6-output-prometheus-pushgateway/pkg/extension"
	"github.com/wesnel/xk6-output-prometheus-pushgateway/pkg/extension/configuration"

	"github.com/mstoykov/atlas"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/expfmt"
	"github.com/sirupsen/logrus"
	"go.k6.io/k6/lib"
	"go.k6.io/k6/metrics"
	"go.k6.io/k6/output"
)

func TestOutput(t *testing.T) {
	type testSpec struct {
		name       string
		options    lib.Options
		containers []metrics.SampleContainer
		expected   []string
	}

	testCases := []testSpec{
		{
			name: "basic successful flush",
			options: lib.Options{
				External: map[string]json.RawMessage{
					"xk6-output-prometheus-pushgateway": must[[]byte](t)(
						json.Marshal(configuration.Config{
							Metrics: configuration.MetricSpecifications{
								Prefix: "prefix_",
								Definitions: map[string]configuration.MetricOptions{
									"test_counter": {
										Type: metrics.Counter,
										Labels: []string{
											"test_counter_label",
										},
									},
								},
							},
						}),
					),
				},
			},
			containers: []metrics.SampleContainer{
				metrics.Sample{
					TimeSeries: metrics.TimeSeries{
						Metric: &metrics.Metric{
							Name: "test_counter",
							Type: metrics.Counter,
						},
						Tags: (*metrics.TagSet)(atlas.New().
							AddLink("test_counter_label", "test_counter_label_value")),
					},
					Value: 1,
				},
				metrics.Sample{
					TimeSeries: metrics.TimeSeries{
						Metric: &metrics.Metric{
							Name: "test_counter",
							Type: metrics.Counter,
						},
						Tags: (*metrics.TagSet)(atlas.New().
							AddLink("test_counter_label", "test_counter_label_value")),
					},
					Value: 1,
				},
			},
			expected: []string{
				"# TYPE prefix_test_counter counter",
				`prefix_test_counter{test_counter_label="test_counter_label_value"} 2`,
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			logger := logrus.New()
			logger.SetLevel(logrus.DebugLevel)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				s := string(b)

				for _, line := range testCase.expected {
					if !strings.Contains(s, line) {
						t.Fatalf("expected:\n%s\ngot:\n%s",
							line,
							s)
					}
				}
			}))
			t.Cleanup(server.Close)

			out, err := extension.New(output.Params{
				Logger:        logger,
				ScriptOptions: testCase.options,

				Environment: map[string]string{
					"K6_JOB_NAME":        "test",
					"K6_PUSHGATEWAY_URL": server.URL,
					"K6_PUSH_FORMAT":     string(expfmt.FmtText),
				},
			}, prometheus.NewRegistry())
			if err != nil {
				t.Fatalf(`Expected error to be nil but instead it was "%s"`, err.Error())
			}

			if err := out.Start(); err != nil {
				t.Fatalf(`Expected error to be nil but instead it was "%s"`, err.Error())
			}

			out.AddMetricSamples(testCase.containers)

			if err := out.Stop(); err != nil {
				t.Fatalf(`Expected error to be nil but instead it was "%s"`, err.Error())
			}
		})
	}
}

func must[T any](t testing.TB) func(T, error) T {
	return func(val T, err error) T {
		if err != nil {
			t.Fatalf(`Expected error to be nil but instead it was "%s"`, err.Error())
		}

		return val
	}
}
