package netsec

import "fmt"

func Example() {
	secrets := []string{"alice", "bob", "carol", "dave"}
	plugins := []Plugin{
		{Name: "region", Labels: []string{"east", "east", "west", "east"}},
		{Name: "device", Labels: []string{"phone", "laptop", "phone", "laptop"}},
		{Name: "tier", Labels: []string{"gold", "silver", "gold", "bronze"}},
	}
	analysis, err := NewAnalysis(secrets, plugins, []float64{0.4, 0.2, 0.3, 0.1})
	if err != nil {
		panic(err)
	}

	mask, err := analysis.MaskOf([]string{"region", "device"})
	if err != nil {
		panic(err)
	}
	fmt.Print(analysis.FormatPartition(analysis.Refine(mask)))

	sets, err := analysis.MinimalSets(1.0, MetricBayes)
	if err != nil {
		panic(err)
	}
	fmt.Printf("minimum full-identification sets: %v\n", sets)

	report := analysis.Verify()
	fmt.Print(FormatVerifyReport(report))
	// Output:
	// plugins={region,device} classes=3
	//   class 0: secrets=alice mass=0.400000 guess=alice guess_mass=0.400000
	//   class 1: secrets=bob,dave mass=0.300000 guess=bob guess_mass=0.200000
	//   class 2: secrets=carol mass=0.300000 guess=carol guess_mass=0.300000
	//   bayes_rate=0.900000 singleton_rate=0.700000
	// minimum full-identification sets: [[region tier]]
	// verified subsets=8 over secrets=4: all cross-checks agree
}
