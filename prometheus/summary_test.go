// Copyright 2014 The Prometheus Authors
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

package prometheus

import (
	"math"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"testing/quick"
	"time"

	dto "github.com/prometheus/client_model/go"
)

func TestSummaryWithDefaultObjectives(t *testing.T) {
	now := time.Now()

	reg := NewRegistry()
	summaryWithDefaultObjectives := NewSummary(SummaryOpts{
		Name: "default_objectives",
		Help: "Test help.",
		now:  func() time.Time { return now },
	})
	if err := reg.Register(summaryWithDefaultObjectives); err != nil {
		t.Error(err)
	}

	m := &dto.Metric{}
	if err := summaryWithDefaultObjectives.Write(m); err != nil {
		t.Error(err)
	}
	if len(m.GetSummary().Quantile) != 0 {
		t.Error("expected no objectives in summary")
	}

	if !m.Summary.CreatedTimestamp.AsTime().Equal(now) {
		t.Errorf("expected created timestamp %s, got %s", now, m.Summary.CreatedTimestamp.AsTime())
	}
}

func TestSummaryWithoutObjectives(t *testing.T) {
	reg := NewRegistry()
	summaryWithEmptyObjectives := NewSummary(SummaryOpts{
		Name:       "empty_objectives",
		Help:       "Test help.",
		Objectives: map[float64]float64{},
	})
	if err := reg.Register(summaryWithEmptyObjectives); err != nil {
		t.Error(err)
	}
	summaryWithEmptyObjectives.Observe(3)
	summaryWithEmptyObjectives.Observe(0.14)

	m := &dto.Metric{}
	if err := summaryWithEmptyObjectives.Write(m); err != nil {
		t.Error(err)
	}
	if got, want := m.GetSummary().GetSampleSum(), 3.14; got != want {
		t.Errorf("got sample sum %f, want %f", got, want)
	}
	if got, want := m.GetSummary().GetSampleCount(), uint64(2); got != want {
		t.Errorf("got sample sum %d, want %d", got, want)
	}
	if len(m.GetSummary().Quantile) != 0 {
		t.Error("expected no objectives in summary")
	}
}

func TestSummaryWithQuantileLabel(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Attempt to create Summary with 'quantile' label did not panic.")
		}
	}()
	_ = NewSummary(SummaryOpts{
		Name:        "test_summary",
		Help:        "less",
		ConstLabels: Labels{"quantile": "test"},
	})
}

func TestSummaryVecWithQuantileLabel(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("Attempt to create SummaryVec with 'quantile' label did not panic.")
		}
	}()
	_ = NewSummaryVec(SummaryOpts{
		Name: "test_summary",
		Help: "less",
	}, []string{"quantile"})
}

func benchmarkSummaryObserve(w int, b *testing.B) {
	b.StopTimer()

	wg := new(sync.WaitGroup)
	wg.Add(w)

	g := new(sync.WaitGroup)
	g.Add(1)

	s := NewSummary(SummaryOpts{})

	for i := 0; i < w; i++ {
		go func() {
			g.Wait()

			for i := 0; i < b.N; i++ {
				s.Observe(float64(i))
			}

			wg.Done()
		}()
	}

	b.StartTimer()
	g.Done()
	wg.Wait()
}

func BenchmarkSummaryObserve1(b *testing.B) {
	benchmarkSummaryObserve(1, b)
}

func BenchmarkSummaryObserve2(b *testing.B) {
	benchmarkSummaryObserve(2, b)
}

func BenchmarkSummaryObserve4(b *testing.B) {
	benchmarkSummaryObserve(4, b)
}

func BenchmarkSummaryObserve8(b *testing.B) {
	benchmarkSummaryObserve(8, b)
}

func benchmarkSummaryWrite(w int, b *testing.B) {
	b.StopTimer()

	wg := new(sync.WaitGroup)
	wg.Add(w)

	g := new(sync.WaitGroup)
	g.Add(1)

	s := NewSummary(SummaryOpts{})

	for i := 0; i < 1000000; i++ {
		s.Observe(float64(i))
	}

	for j := 0; j < w; j++ {
		outs := make([]dto.Metric, b.N)

		go func(o []dto.Metric) {
			g.Wait()

			for i := 0; i < b.N; i++ {
				s.Write(&o[i])
			}

			wg.Done()
		}(outs)
	}

	b.StartTimer()
	g.Done()
	wg.Wait()
}

func BenchmarkSummaryWrite1(b *testing.B) {
	benchmarkSummaryWrite(1, b)
}

func BenchmarkSummaryWrite2(b *testing.B) {
	benchmarkSummaryWrite(2, b)
}

func BenchmarkSummaryWrite4(b *testing.B) {
	benchmarkSummaryWrite(4, b)
}

func BenchmarkSummaryWrite8(b *testing.B) {
	benchmarkSummaryWrite(8, b)
}

func TestSummaryConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode.")
	}

	rand.New(rand.NewSource(42))
	objMap := map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001}

	it := func(n uint32) bool {
		mutations := int(n%1e4 + 1e4)
		concLevel := int(n%5 + 1)
		total := mutations * concLevel

		var start, end sync.WaitGroup
		start.Add(1)
		end.Add(concLevel)

		sum := NewSummary(SummaryOpts{
			Name:       "test_summary",
			Help:       "helpless",
			Objectives: objMap,
		})

		allVars := make([]float64, total)
		var sampleSum float64
		for i := 0; i < concLevel; i++ {
			vals := make([]float64, mutations)
			for j := 0; j < mutations; j++ {
				v := rand.NormFloat64()
				vals[j] = v
				allVars[i*mutations+j] = v
				sampleSum += v
			}

			go func(vals []float64) {
				start.Wait()
				for _, v := range vals {
					sum.Observe(v)
				}
				end.Done()
			}(vals)
		}
		sort.Float64s(allVars)
		start.Done()
		end.Wait()

		m := &dto.Metric{}
		sum.Write(m)
		if got, want := int(*m.Summary.SampleCount), total; got != want {
			t.Errorf("got sample count %d, want %d", got, want)
		}
		if got, want := *m.Summary.SampleSum, sampleSum; math.Abs((got-want)/want) > 0.001 {
			t.Errorf("got sample sum %f, want %f", got, want)
		}

		objSlice := make([]float64, 0, len(objMap))
		for qu := range objMap {
			objSlice = append(objSlice, qu)
		}
		sort.Float64s(objSlice)

		for i, wantQ := range objSlice {
			ε := objMap[wantQ]
			gotQ := *m.Summary.Quantile[i].Quantile
			gotV := *m.Summary.Quantile[i].Value
			minBound, maxBound := getBounds(allVars, wantQ, ε)
			if gotQ != wantQ {
				t.Errorf("got quantile %f, want %f", gotQ, wantQ)
			}
			if gotV < minBound || gotV > maxBound {
				t.Errorf("got %f for quantile %f, want [%f,%f]", gotV, gotQ, minBound, maxBound)
			}
		}
		return true
	}

	if err := quick.Check(it, nil); err != nil {
		t.Error(err)
	}
}

func TestSummaryVecConcurrency(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode.")
	}

	rand.New(rand.NewSource(42))
	objMap := map[float64]float64{0.5: 0.05, 0.9: 0.01, 0.99: 0.001}

	objSlice := make([]float64, 0, len(objMap))
	for qu := range objMap {
		objSlice = append(objSlice, qu)
	}
	sort.Float64s(objSlice)

	it := func(n uint32) bool {
		mutations := int(n%1e4 + 1e4)
		concLevel := int(n%7 + 1)
		vecLength := int(n%3 + 1)

		var start, end sync.WaitGroup
		start.Add(1)
		end.Add(concLevel)

		sum := NewSummaryVec(
			SummaryOpts{
				Name:       "test_summary",
				Help:       "helpless",
				Objectives: objMap,
			},
			[]string{"label"},
		)

		allVars := make([][]float64, vecLength)
		sampleSums := make([]float64, vecLength)
		for i := 0; i < concLevel; i++ {
			vals := make([]float64, mutations)
			picks := make([]int, mutations)
			for j := 0; j < mutations; j++ {
				v := rand.NormFloat64()
				vals[j] = v
				pick := rand.Intn(vecLength)
				picks[j] = pick
				allVars[pick] = append(allVars[pick], v)
				sampleSums[pick] += v
			}

			go func(vals []float64) {
				start.Wait()
				for i, v := range vals {
					sum.WithLabelValues(string('A' + rune(picks[i]))).Observe(v)
				}
				end.Done()
			}(vals)
		}
		for _, vars := range allVars {
			sort.Float64s(vars)
		}
		start.Done()
		end.Wait()

		for i := 0; i < vecLength; i++ {
			m := &dto.Metric{}
			s := sum.WithLabelValues(string('A' + rune(i)))
			s.(Summary).Write(m)
			if got, want := int(*m.Summary.SampleCount), len(allVars[i]); got != want {
				t.Errorf("got sample count %d for label %c, want %d", got, 'A'+i, want)
			}
			if got, want := *m.Summary.SampleSum, sampleSums[i]; math.Abs((got-want)/want) > 0.001 {
				t.Errorf("got sample sum %f for label %c, want %f", got, 'A'+i, want)
			}
			for j, wantQ := range objSlice {
				ε := objMap[wantQ]
				gotQ := *m.Summary.Quantile[j].Quantile
				gotV := *m.Summary.Quantile[j].Value
				minBound, maxBound := getBounds(allVars[i], wantQ, ε)
				if gotQ != wantQ {
					t.Errorf("got quantile %f for label %c, want %f", gotQ, 'A'+i, wantQ)
				}
				if gotV < minBound || gotV > maxBound {
					t.Errorf("got %f for quantile %f for label %c, want [%f,%f]", gotV, gotQ, 'A'+i, minBound, maxBound)
				}
			}
		}
		return true
	}

	if err := quick.Check(it, nil); err != nil {
		t.Error(err)
	}
}

func TestSummaryDecay(t *testing.T) {
	now := time.Now()

	sum := NewSummary(SummaryOpts{
		Name:       "test_summary",
		Help:       "helpless",
		MaxAge:     100 * time.Millisecond,
		Objectives: map[float64]float64{0.1: 0.001},
		AgeBuckets: 10,
		now: func() time.Time {
			return now
		},
	})

	m := &dto.Metric{}
	for i := 1; i <= 1000; i++ {
		now = now.Add(time.Millisecond)
		sum.Observe(float64(i))
		if i%10 == 0 {
			sum.Write(m)
			got := *m.Summary.Quantile[0].Value
			want := math.Max(float64(i)/10, float64(i-90))
			if math.Abs(got-want) > 20 {
				t.Errorf("%d. got %f, want %f", i, got, want)
			}
			m.Reset()
		}
	}

	// Simulate waiting for MaxAge without observations
	now = now.Add(100 * time.Millisecond)
	sum.Write(m)
	if got := *m.Summary.Quantile[0].Value; !math.IsNaN(got) {
		t.Errorf("got %f, want NaN after expiration", got)
	}
}

func getBounds(vars []float64, q, ε float64) (minBound, maxBound float64) {
	// TODO(beorn7): This currently tolerates an error of up to 2*ε. The
	// error must be at most ε, but for some reason, it's sometimes slightly
	// higher. That's a bug.
	n := float64(len(vars))
	lower := int((q - 2*ε) * n)
	upper := int(math.Ceil((q + 2*ε) * n))
	minBound = vars[0]
	if lower > 1 {
		minBound = vars[lower-1]
	}
	maxBound = vars[len(vars)-1]
	if upper < len(vars) {
		maxBound = vars[upper-1]
	}
	return
}

func TestSummaryVecCreatedTimestampWithDeletes(t *testing.T) {
	for _, tcase := range []struct {
		desc       string
		objectives map[float64]float64
	}{
		{desc: "summary with objectives", objectives: map[float64]float64{1.0: 1.0}},
		{desc: "no objectives summary", objectives: nil},
	} {
		now := time.Now()
		t.Run(tcase.desc, func(t *testing.T) {
			summaryVec := NewSummaryVec(SummaryOpts{
				Name:       "test",
				Help:       "test help",
				Objectives: tcase.objectives,
				now:        func() time.Time { return now },
			}, []string{"label"})

			// First use of "With" should populate CT.
			summaryVec.WithLabelValues("1")
			expected := map[string]time.Time{"1": now}

			now = now.Add(1 * time.Hour)
			expectCTsForMetricVecValues(t, summaryVec.MetricVec, dto.MetricType_SUMMARY, expected)

			// Two more labels at different times.
			summaryVec.WithLabelValues("2")
			expected["2"] = now

			now = now.Add(1 * time.Hour)

			summaryVec.WithLabelValues("3")
			expected["3"] = now

			now = now.Add(1 * time.Hour)
			expectCTsForMetricVecValues(t, summaryVec.MetricVec, dto.MetricType_SUMMARY, expected)

			// Recreate metric instance should reset created timestamp to now.
			summaryVec.DeleteLabelValues("1")
			summaryVec.WithLabelValues("1")
			expected["1"] = now

			now = now.Add(1 * time.Hour)
			expectCTsForMetricVecValues(t, summaryVec.MetricVec, dto.MetricType_SUMMARY, expected)
		})
	}
}

func TestNewConstSummaryWithCreatedTimestamp(t *testing.T) {
	metricDesc := NewDesc(
		"sample_value",
		"sample value",
		nil,
		nil,
		nil,
	)
	quantiles := map[float64]float64{50: 200.12, 99: 500.342}
	createdTs := time.Unix(1719670764, 123)

	s, err := NewConstSummaryWithCreatedTimestamp(metricDesc, 100, 200, quantiles, createdTs)
	if err != nil {
		t.Fatal(err)
	}

	var metric dto.Metric
	if err := s.Write(&metric); err != nil {
		t.Fatal(err)
	}

	if metric.Summary.CreatedTimestamp.AsTime().UnixMicro() != createdTs.UnixMicro() {
		t.Errorf("Expected created timestamp %v, got %v", createdTs, &metric.Summary.CreatedTimestamp)
	}
}
