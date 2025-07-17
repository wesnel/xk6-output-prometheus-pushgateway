package pushgateway

import (
	"github.com/wesnel/xk6-output-prometheus-pushgateway/pkg/extension"

	"github.com/prometheus/client_golang/prometheus"
	"go.k6.io/k6/output"
)

const name = "xk6-output-prometheus-pushgateway"

func init() {
	output.RegisterExtension(name, func(p output.Params) (output.Output, error) {
		p.Logger = p.Logger.WithField("component", name)
		return extension.New(p, prometheus.NewRegistry())
	})
}
