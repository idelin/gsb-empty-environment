// Package sim runs Monte Carlo traffic streams against the TRW/SPRT
// detector so its decision latency and misclassification rates can be
// measured empirically.
package sim

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strings"

	"example.org/scandetect"
)

// Params describes one Monte Carlo experiment.
type Params struct {
	// Trials is the number of simulated sources per class.
	Trials int
	// Horizon is the maximum unique connections simulated for a source.
	Horizon int
	// ScanSuccessProb is the Bernoulli success rate of a scanner.
	ScanSuccessProb float64
	// BenignSuccessProb is the Bernoulli success rate of a benign client.
	BenignSuccessProb float64
	// BenignServicePool is the number of distinct services a benign client
	// uses; connections repeatedly pick among these (and are deduped).
	BenignServicePool int
	// MaxSources caps the detector. Each trial uses a unique source, so it
	// must exceed Trials to avoid eviction inside a run.
	MaxSources int
	Seed       int64
	Config     scandetect.Config
}

// Trial records what happened to one simulated source.
type Trial struct {
	Verdict           scandetect.Verdict
	UniqueConnections int
}

// Aggregate summarizes a class of trials.
type Aggregate struct {
	Label       string
	Trials      int
	ScannerHits int
	BenignHits  int
	Undecided   int
	DecisionRTS []int
}

// Report is the full experiment result.
type Report struct {
	Params  Params
	Upper   float64
	Lower   float64
	Scanner Aggregate
	Benign  Aggregate
}

func newAggregate(label string, trials []Trial) Aggregate {
	a := Aggregate{Label: label, Trials: len(trials)}
	for _, t := range trials {
		switch t.Verdict {
		case scandetect.Scanner:
			a.ScannerHits++
		case scandetect.Benign:
			a.BenignHits++
		default:
			a.Undecided++
		}
		if t.Verdict != scandetect.Pending {
			a.DecisionRTS = append(a.DecisionRTS, t.UniqueConnections)
		}
	}
	return a
}

// Run executes Trials scanner streams and Trials benign streams.
func Run(p Params) (*Report, error) {
	if p.Trials <= 0 || p.Horizon <= 0 || p.BenignServicePool <= 0 {
		return nil, fmt.Errorf("sim: Trials, Horizon and BenignServicePool must be positive")
	}
	if p.MaxSources <= p.Trials {
		return nil, fmt.Errorf("sim: MaxSources (%d) must exceed Trials (%d)", p.MaxSources, p.Trials)
	}
	cfg := p.Config
	cfg.MaxSources = p.MaxSources
	det, err := scandetect.New(cfg)
	if err != nil {
		return nil, err
	}
	upper, lower := det.Thresholds()
	rng := rand.New(rand.NewSource(p.Seed))

	scannerTrials := make([]Trial, 0, p.Trials)
	for i := 0; i < p.Trials; i++ {
		source := fmt.Sprintf("scanner-%d", i)
		t := Trial{Verdict: scandetect.Pending}
		for conn := 0; conn < p.Horizon; conn++ {
			target := fmt.Sprintf("host-%d:port", conn)
			success := rng.Float64() < p.ScanSuccessProb
			v := det.Observe(source, target, success)
			t.UniqueConnections++
			if v != scandetect.Pending {
				t.Verdict = v
				break
			}
		}
		scannerTrials = append(scannerTrials, t)
	}

	benignTrials := make([]Trial, 0, p.Trials)
	for i := 0; i < p.Trials; i++ {
		source := fmt.Sprintf("benign-%d", i)
		t := Trial{Verdict: scandetect.Pending}
		seen := make(map[string]bool)
		for conn := 0; conn < p.Horizon; conn++ {
			// A normal client reuses a small pool of services, so most
			// connections hit an already-seen target and are not counted.
			target := fmt.Sprintf("svc-%d:443", rng.Intn(p.BenignServicePool))
			success := rng.Float64() < p.BenignSuccessProb
			v := det.Observe(source, target, success)
			if !seen[target] {
				seen[target] = true
				t.UniqueConnections++
			}
			if v != scandetect.Pending {
				t.Verdict = v
				break
			}
		}
		benignTrials = append(benignTrials, t)
	}

	return &Report{
		Params:  p,
		Upper:   upper,
		Lower:   lower,
		Scanner: newAggregate("scanner", scannerTrials),
		Benign:  newAggregate("benign", benignTrials),
	}, nil
}

// Rate returns the fraction of trials flagged as scanner.
func (a Aggregate) Rate() float64 {
	if a.Trials == 0 {
		return 0
	}
	return float64(a.ScannerHits) / float64(a.Trials)
}

// MeanDecisionRT is the mean unique connections needed to reach any verdict.
func (a Aggregate) MeanDecisionRT() float64 {
	if len(a.DecisionRTS) == 0 {
		return math.NaN()
	}
	sum := 0
	for _, rt := range a.DecisionRTS {
		sum += rt
	}
	return float64(sum) / float64(len(a.DecisionRTS))
}

// PercentileDecisionRT returns the p (0..1) quantile of decision latency.
func (a Aggregate) PercentileDecisionRT(p float64) int {
	if len(a.DecisionRTS) == 0 {
		return 0
	}
	sorted := append([]int(nil), a.DecisionRTS...)
	sort.Ints(sorted)
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Render prints a human-readable report.
func (r *Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "TRW/SPRT simulation (trials/class=%d, horizon=%d unique conns, seed=%d)\n",
		r.Params.Trials, r.Params.Horizon, r.Params.Seed)
	fmt.Fprintf(&b, "theta0(benign success)=%.2f theta1(scanner success)=%.2f alpha=%.4f beta=%.4f\n",
		r.Params.Config.SuccessProbBenign, r.Params.Config.SuccessProbScanner,
		r.Params.Config.FalsePositiveRate, r.Params.Config.FalseNegativeRate)
	fmt.Fprintf(&b, "thresholds: upper=%.3f (scanner), lower=%.3f (benign), log-space\n\n", r.Upper, r.Lower)

	for _, a := range []Aggregate{r.Scanner, r.Benign} {
		fmt.Fprintf(&b, "%s class (true label=%s)\n", a.Label, a.Label)
		fmt.Fprintf(&b, "  flagged scanner : %d/%d (%.4f)\n", a.ScannerHits, a.Trials, a.Rate())
		fmt.Fprintf(&b, "  declared benign : %d/%d (%.4f)\n", a.BenignHits, a.Trials,
			float64(a.BenignHits)/float64(a.Trials))
		fmt.Fprintf(&b, "  still undecided : %d/%d (%.4f)\n", a.Undecided, a.Trials,
			float64(a.Undecided)/float64(a.Trials))
		fmt.Fprintf(&b, "  unique conns to decision: mean=%.2f p50=%d p95=%d\n\n",
			a.MeanDecisionRT(), a.PercentileDecisionRT(0.5), a.PercentileDecisionRT(0.95))
	}
	b.WriteString("Interpretation:\n")
	fmt.Fprintf(&b, "  false-positive rate = benign flagged scanner = %.4f (nominal alpha=%.4f)\n",
		r.Benign.Rate(), r.Params.Config.FalsePositiveRate)
	fmt.Fprintf(&b, "  scanner detection   = %.4f; miss rate = %.4f (nominal beta=%.4f)\n",
		r.Scanner.Rate(), 1-r.Scanner.Rate()-float64(r.Scanner.Undecided)/float64(r.Scanner.Trials),
		r.Params.Config.FalseNegativeRate)
	return b.String()
}
