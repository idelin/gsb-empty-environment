// Package taskpkg implements diagnostic-only multichannel polarity
// correlation analysis for a Go audio QC host.
//
// Each fixed window yields signed zero-lag Pearson correlation and an
// energy ratio for every channel pair. Silent, weak-correlation, and
// out-of-range-energy pairs are unknown evidence. A signed graph built from
// confirmed edges is checked for a globally consistent polarity assignment;
// inconsistent evidence yields a shortest negative signed cycle. The package
// never applies inversion or returns corrected audio.
package taskpkg
