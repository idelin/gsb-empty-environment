// Command scansim runs a Monte Carlo validation of the TRW/SPRT scan
// detector against simulated scanner and benign connection streams.
package main

import (
	"flag"
	"fmt"
	"os"

	"example.org/scandetect"
	"example.org/scandetect/internal/sim"
)

func main() {
	var (
		trials   = flag.Int("trials", 5000, "simulated sources per class")
		horizon  = flag.Int("horizon", 200, "max unique connections per source")
		alpha    = flag.Float64("alpha", 0.01, "nominal false-positive rate")
		beta     = flag.Float64("beta", 0.01, "nominal false-negative rate")
		theta0   = flag.Float64("theta0", 0.8, "benign connection success probability")
		theta1   = flag.Float64("theta1", 0.2, "scanner connection success probability")
		poolSize = flag.Int("pool", 25, "distinct services used by a benign client")
		seed     = flag.Int64("seed", 42, "RNG seed")
	)
	flag.Parse()

	cfg := scandetect.DefaultConfig()
	cfg.FalsePositiveRate = *alpha
	cfg.FalseNegativeRate = *beta
	cfg.SuccessProbBenign = *theta0
	cfg.SuccessProbScanner = *theta1

	report, err := sim.Run(sim.Params{
		Trials:            *trials,
		Horizon:           *horizon,
		ScanSuccessProb:   *theta1,
		BenignSuccessProb: *theta0,
		BenignServicePool: *poolSize,
		MaxSources:        *trials*2 + 16,
		Seed:              *seed,
		Config:            cfg,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "scansim:", err)
		os.Exit(1)
	}
	fmt.Print(report.Render())
}
