package main

import (
	"fmt"
	"os"

	"cutting"
)

func main() {
	stocks := []cutting.Stock{
		{Length: 6000, Price: 120},
		{Length: 4000, Price: 90},
		{Length: 2500, Price: 60},
	}
	kerf := 5
	parts := []cutting.Part{
		{Order: "frame", Length: 1200},
		{Order: "frame", Length: 1200},
		{Order: "leg", Length: 700},
		{Order: "leg", Length: 700},
		{Order: "leg", Length: 700},
		{Order: "leg", Length: 700},
		{Order: "brace", Length: 950},
		{Order: "brace", Length: 950},
		{Order: "rail", Length: 1800},
		{Order: "rail", Length: 1800},
	}

	sol, err := cutting.Optimize(stocks, kerf, parts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "optimize:", err)
		os.Exit(1)
	}
	if err := cutting.Verify(sol, stocks, kerf, parts); err != nil {
		fmt.Fprintln(os.Stderr, "verify:", err)
		os.Exit(1)
	}
	fmt.Print(sol)
}
