package metrics

import (
	"strings"
	"testing"
)

const sample = `# HELP x
switchyard_client_responses_total{outcome="ok",otel_scope_name="switchyard"} 10
switchyard_client_responses_total{outcome="other_error",otel_scope_name="switchyard"} 2
switchyard_client_responses_total{outcome="client_disconnected",otel_scope_name="switchyard"} 5
switchyard_prompt_tokens_total{model="a",otel_scope_name="switchyard"} 100
switchyard_prompt_tokens_total{model="b",otel_scope_name="switchyard"} 50
switchyard_total_latency_ms_sum{model="a",otel_scope_name="switchyard"} 300
switchyard_total_latency_ms_count{model="a",otel_scope_name="switchyard"} 3
switchyard_upstream_attempts_total{outcome="ok",code="200",otel_scope_name="switchyard"} 9
switchyard_upstream_attempts_total{outcome="retryable",code="429",otel_scope_name="switchyard"} 4
switchyard_router_retry_recovered_total{otel_scope_name="switchyard"} 1
`

func TestParse(t *testing.T) {
	c, err := parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.requests != 12 || c.errors != 2 || c.prompt != 150 || c.latencySum != 300 || c.upstreamFailures != 4 || c.retryRecovered != 1 {
		t.Fatalf("%+v", c)
	}
}
