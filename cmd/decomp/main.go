// Command decomp reads a full rational joint probability table as JSON
// and prints the finest mutual-independence decomposition together with
// its exact equality certificate.
//
// Input (file argument or stdin):
//
//	{"dims": [2,2,2], "probs": ["1/8","1/8",...]}   // row-major, last var fastest
//
// Output: JSON decomposition + certificate, or human-readable text with -v.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"

	"decomp"
)

type input struct {
	Dims  []int    `json:"dims"`
	Probs []string `json:"probs"`
}

type blockOut struct {
	Vars  []int    `json:"vars"`
	Dims  []int    `json:"dims"`
	Probs []string `json:"probs"`
}

type output struct {
	Partition            [][]int    `json:"partition"`
	RGS                  []int      `json:"rgs"`
	Marginals            []blockOut `json:"marginals"`
	CellsChecked         int        `json:"cells_checked_exact"`
	SplitEquations       []string   `json:"split_equations"`
	PartitionsEnumerated int        `json:"partitions_enumerated"`
	ValidPartitions      [][]int    `json:"valid_partitions_rgs"`
	FinestNumBlocks      int        `json:"finest_num_blocks"`
	CertificateVerified  bool       `json:"certificate_verified"`
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "decomp:", err)
	os.Exit(1)
}

func main() {
	verbose := flag.Bool("v", false, "print human-readable certificate")
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() > 0 {
		f, err := os.Open(flag.Arg(0))
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}
	var in input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		fatal(fmt.Errorf("bad input JSON: %w", err))
	}
	probs := make([]*big.Rat, len(in.Probs))
	for i, s := range in.Probs {
		p, ok := new(big.Rat).SetString(s)
		if !ok {
			fatal(fmt.Errorf("probs[%d]: cannot parse %q as a rational", i, s))
		}
		probs[i] = p
	}
	tab, err := decomp.NewTable(in.Dims, probs)
	if err != nil {
		fatal(err)
	}
	res, err := tab.Decompose()
	if err != nil {
		fatal(err)
	}
	cert := res.Certificate
	if !decomp.VerifyCertificate(tab, cert) {
		fatal(fmt.Errorf("internal error: certificate failed verification"))
	}

	if *verbose {
		fmt.Print(cert.String())
		return
	}
	out := output{
		RGS:                  res.Partition.RGS(),
		CellsChecked:         cert.FullCheck.Cells,
		PartitionsEnumerated: cert.PartitionsEnumerated,
		FinestNumBlocks:      cert.FinestNumBlocks,
		CertificateVerified:  true,
	}
	for _, b := range res.Partition.Blocks {
		out.Partition = append(out.Partition, b)
	}
	for i, m := range res.Marginals {
		bo := blockOut{Vars: res.Partition.Blocks[i], Dims: m.Dims}
		for _, p := range m.Probs {
			bo.Probs = append(bo.Probs, p.RatString())
		}
		out.Marginals = append(out.Marginals, bo)
	}
	out.SplitEquations = append(out.SplitEquations, cert.FullCheck.Equation)
	for _, s := range cert.Steps {
		out.SplitEquations = append(out.SplitEquations, s.Equation)
	}
	for _, q := range cert.ValidPartitions {
		out.ValidPartitions = append(out.ValidPartitions, q.RGS())
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fatal(err)
	}
}
