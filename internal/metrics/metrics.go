// Package metrics scrapes switchyard-server's Prometheus endpoint every few seconds and
// keeps the last hour in memory for the live charts. History comes from the usage ledger.
package metrics

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	interval = 5 * time.Second
	keep     = 720 // one hour of points
)

// Point is what happened during one scrape interval.
type Point struct {
	T                int64   `json:"t"` // Unix seconds at the end of the interval
	Requests         float64 `json:"requests"`
	Errors           float64 `json:"errors"`
	PromptTokens     float64 `json:"prompt_tokens"`
	CompletionTokens float64 `json:"completion_tokens"`
	CachedTokens     float64 `json:"cached_tokens"`
	// Average full-turn latency and routing overhead of requests that finished in the
	// interval, in milliseconds; 0 when none did.
	LatencyMS          float64 `json:"latency_ms"`
	RoutingOverheadMS  float64 `json:"routing_overhead_ms"`
	UpstreamFailures   float64 `json:"upstream_failures"`
	RetryRecovered     float64 `json:"retry_recovered"`
	ClassifierFailOpen float64 `json:"classifier_fail_open"`
}

// counters are the cumulative values read from one scrape.
type counters struct {
	requests, errors, prompt, completion, cached         float64
	latencySum, latencyCount, overheadSum, overheadCount float64
	upstreamFailures, retryRecovered, failOpen           float64
}

type Scraper struct {
	url    string
	client *http.Client

	mu     sync.Mutex
	points []Point
	last   *counters
}

func NewScraper(switchyardURL string) *Scraper {
	return &Scraper{url: switchyardURL + "/metrics", client: &http.Client{Timeout: 3 * time.Second}}
}

// Run scrapes until ctx ends.
func (s *Scraper) Run(ctx context.Context) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.scrape(ctx, now)
		}
	}
}

// Points returns the recorded points, oldest first.
func (s *Scraper) Points() []Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Point{}, s.points...)
}

func (s *Scraper) scrape(ctx context.Context, now time.Time) {
	c, err := s.fetch(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Point{T: now.Unix()}
	if err == nil && s.last != nil {
		d := func(cur, prev float64) float64 {
			if cur < prev {
				return cur // switchyard-server restarted; its counters start again at zero
			}
			return cur - prev
		}
		prev := s.last
		p.Requests = d(c.requests, prev.requests)
		p.Errors = d(c.errors, prev.errors)
		p.PromptTokens = d(c.prompt, prev.prompt)
		p.CompletionTokens = d(c.completion, prev.completion)
		p.CachedTokens = d(c.cached, prev.cached)
		p.UpstreamFailures = d(c.upstreamFailures, prev.upstreamFailures)
		p.RetryRecovered = d(c.retryRecovered, prev.retryRecovered)
		p.ClassifierFailOpen = d(c.failOpen, prev.failOpen)
		if n := d(c.latencyCount, prev.latencyCount); n > 0 {
			p.LatencyMS = d(c.latencySum, prev.latencySum) / n
		}
		if n := d(c.overheadCount, prev.overheadCount); n > 0 {
			p.RoutingOverheadMS = d(c.overheadSum, prev.overheadSum) / n
		}
	}
	if err == nil {
		s.last = c
	} else {
		s.last = nil // switchyard-server is down; the next good scrape starts fresh
	}
	s.points = append(s.points, p)
	if len(s.points) > keep {
		s.points = s.points[len(s.points)-keep:]
	}
}

func (s *Scraper) fetch(ctx context.Context) (*counters, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return parse(resp.Body)
}

// parse reads the Prometheus text format, summing each metric across its labels except
// where a label decides what's counted.
func parse(r io.Reader) (*counters, error) {
	c := &counters{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		name, labels, value, ok := splitSample(line)
		if !ok {
			continue
		}
		switch name {
		case "switchyard_client_responses_total":
			outcome := labelValue(labels, "outcome")
			if outcome == "client_disconnected" {
				continue
			}
			c.requests += value
			if outcome != "ok" {
				c.errors += value
			}
		case "switchyard_prompt_tokens_total":
			c.prompt += value
		case "switchyard_completion_tokens_total":
			c.completion += value
		case "switchyard_cached_tokens_total":
			c.cached += value
		case "switchyard_total_latency_ms_sum":
			c.latencySum += value
		case "switchyard_total_latency_ms_count":
			c.latencyCount += value
		case "switchyard_routing_overhead_ms_sum":
			c.overheadSum += value
		case "switchyard_routing_overhead_ms_count":
			c.overheadCount += value
		case "switchyard_upstream_attempts_total":
			if labelValue(labels, "outcome") != "ok" {
				c.upstreamFailures += value
			}
		case "switchyard_router_retry_recovered_total":
			c.retryRecovered += value
		case "switchyard_classifier_fail_open_total":
			c.failOpen += value
		}
	}
	return c, sc.Err()
}

func splitSample(line string) (name, labels string, value float64, ok bool) {
	rest := line
	if i := strings.IndexByte(line, '{'); i >= 0 {
		j := strings.LastIndexByte(line, '}')
		if j < i {
			return "", "", 0, false
		}
		name, labels, rest = line[:i], line[i+1:j], strings.TrimSpace(line[j+1:])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", "", 0, false
		}
		name, rest = fields[0], fields[1]
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return "", "", 0, false
	}
	v, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return "", "", 0, false
	}
	return name, labels, v, true
}

func labelValue(labels, key string) string {
	for _, part := range strings.Split(labels, ",") {
		k, v, found := strings.Cut(part, "=")
		if found && strings.TrimSpace(k) == key {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}
