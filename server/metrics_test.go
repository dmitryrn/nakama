// Copyright 2026 The Nakama Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uber-go/tally/v4"
	"go.uber.org/zap"
)

func TestMetricsCounterAddNegativeDoesNotPanic(t *testing.T) {
	logger := zap.NewNop()
	cfg := NewConfig(logger)
	cfg.Metrics.ReportingFreqSec = 1
	reportingInterval := time.Duration(cfg.Metrics.ReportingFreqSec) * time.Second
	flushWait := reportingInterval + 200*time.Millisecond

	metrics := NewLocalMetrics(logger, logger, nil, cfg)
	defer metrics.Stop(logger)

	module := &RuntimeGoNakamaModule{metrics: metrics}
	module.MetricsCounterAdd("panic_counter", nil, 1)

	time.Sleep(flushWait)
	module.MetricsCounterAdd("panic_counter", nil, -1)

	time.Sleep(flushWait)
}

// scrapedSeries returns every exported line of the named metric, as Prometheus would serve it.
func scrapedSeries(t *testing.T, metrics *LocalMetrics, name string) []string {
	t.Helper()

	recorder := httptest.NewRecorder()
	metrics.prometheusReporter.HTTPHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	series := make([]string, 0, 2)
	for _, line := range strings.Split(recorder.Body.String(), "\n") {
		if strings.HasPrefix(line, name+"{") {
			series = append(series, strings.TrimSpace(line))
		}
	}
	return series
}

func newTestMetrics(t *testing.T) (*LocalMetrics, *RuntimeGoNakamaModule, time.Duration) {
	t.Helper()

	logger := zap.NewNop()
	cfg := NewConfig(logger)
	cfg.Metrics.ReportingFreqSec = 1

	metrics := NewLocalMetrics(logger, logger, nil, cfg)
	t.Cleanup(func() { metrics.Stop(logger) })

	flushWait := time.Duration(cfg.Metrics.ReportingFreqSec)*time.Second + 200*time.Millisecond
	return metrics, &RuntimeGoNakamaModule{metrics: metrics}, flushWait
}

func TestMetricsGaugeDeleteRemovesOnlyTheGivenTagSet(t *testing.T) {
	metrics, module, flushWait := newTestMetrics(t)
	const exported = "nakama_custom_server_ping_ms"

	module.MetricsGaugeSet("server_ping_ms", map[string]string{"instance": "srv_a"}, 42)
	module.MetricsGaugeSet("server_ping_ms", map[string]string{"instance": "srv_b"}, 17)
	time.Sleep(flushWait)

	if series := scrapedSeries(t, metrics, exported); len(series) != 2 {
		t.Fatalf("expected both gauges to be exported, got %q", series)
	}

	module.MetricsGaugeDelete("server_ping_ms", map[string]string{"instance": "srv_a"})

	// A full report cycle must not resurrect it: the tally scope has to be gone too, not just the
	// Prometheus child.
	time.Sleep(flushWait)

	series := scrapedSeries(t, metrics, exported)
	if len(series) != 1 {
		t.Fatalf("expected one surviving gauge, got %q", series)
	}
	if !strings.Contains(series[0], `instance="srv_b"`) || !strings.HasSuffix(series[0], " 17") {
		t.Fatalf("the wrong gauge survived: %q", series[0])
	}
}

func TestMetricsGaugeDeleteLetsTheSeriesReturn(t *testing.T) {
	metrics, module, flushWait := newTestMetrics(t)
	const exported = "nakama_custom_server_players"
	tags := map[string]string{"instance": "srv_a"}

	module.MetricsGaugeSet("server_players", tags, 8)
	time.Sleep(flushWait)
	module.MetricsGaugeDelete("server_players", tags)

	if series := scrapedSeries(t, metrics, exported); len(series) != 0 {
		t.Fatalf("expected the gauge to be gone, got %q", series)
	}

	module.MetricsGaugeSet("server_players", tags, 3)
	time.Sleep(flushWait)

	series := scrapedSeries(t, metrics, exported)
	if len(series) != 1 || !strings.HasSuffix(series[0], " 3") {
		t.Fatalf("expected the gauge to come back with the new value, got %q", series)
	}
}

func TestMetricsLimitedScopeDeleteFreesItsSlot(t *testing.T) {
	root, closer := tally.NewRootScope(tally.ScopeOptions{Reporter: tally.NullStatsReporter}, 0)
	defer func() { _ = closer.Close() }()

	limited := newMetricsLimitedScope(root, 1)
	first := map[string]string{"instance": "srv_a"}
	second := map[string]string{"instance": "srv_b"}

	if limited.Tagged(first) == tally.NoopScope {
		t.Fatal("the first tag set is within the limit")
	}
	if limited.Tagged(second) != tally.NoopScope {
		t.Fatal("the second tag set exceeds the limit and must be dropped")
	}

	limited.Delete(first)

	if limited.Tagged(second) == tally.NoopScope {
		t.Fatal("deleting a tag set must free its slot")
	}
}
