package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"

	"example.org/offlinecli/acl"
)

type input struct {
	Old [][2]string `json:"old"`
	New [][2]string `json:"new"`
}

func main() {
	verify := flag.Bool("verify", false, "replay raw rules to validate the computed difference blocks")
	samples := flag.Int("samples", 1000, "random verification addresses (with -verify)")
	seed := flag.Int64("seed", 1, "random seed for -verify")
	flag.Parse()

	if err := run(*verify, *samples, *seed); err != nil {
		fmt.Fprintln(os.Stderr, "acldiff:", err)
		os.Exit(1)
	}
}

func run(verify bool, samples int, seed int64) error {
	var in input
	decoder := json.NewDecoder(os.Stdin)
	if err := decoder.Decode(&in); err != nil {
		return fmt.Errorf("decode stdin: %w", err)
	}

	rulesOld, err := parseRules(in.Old, "old")
	if err != nil {
		return err
	}
	rulesNew, err := parseRules(in.New, "new")
	if err != nil {
		return err
	}

	blocks, err := acl.Diff(rulesOld, rulesNew)
	if err != nil {
		return err
	}

	if verify {
		rng := rand.New(rand.NewSource(seed))
		if err := acl.Verify(rulesOld, rulesNew, blocks, samples, rng); err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}
		fmt.Fprintln(os.Stderr, "verification ok")
	}

	out := make([][3]string, 0, len(blocks))
	for _, block := range blocks {
		out = append(out, [3]string{
			block.Prefix().String(),
			acl.ActionName(block.Old),
			acl.ActionName(block.New),
		})
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(out); err != nil {
		return fmt.Errorf("encode output: %w", err)
	}
	return nil
}

func parseRules(entries [][2]string, side string) ([]acl.Rule, error) {
	rules := make([]acl.Rule, 0, len(entries))
	for index, entry := range entries {
		rule, err := acl.ParseRule(entry[0], entry[1])
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", side, index, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}
