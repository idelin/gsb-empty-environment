#pragma once

#include "geom.h"
#include <string>
#include <vector>

struct Diagnostic {
    std::string category; // polygon, degenerate, outside, crossing, overlap,
                          // boundary-gap, hole, area-mismatch
    std::string detail;
};

struct CheckResult {
    bool valid = false;
    std::vector<Diagnostic> diagnostics;
    Rat polygonArea;
    Rat triangleAreaSum;
};

// Verify that `triangles` exactly cover the simple `polygon` without overlap.
// Area equality alone is NOT trusted: the checker also verifies containment,
// pairwise non-overlap, boundary coverage, and interior edge pairing, and
// reports concrete counterexample edges / hole evidence for each failure.
CheckResult checkTriangulation(const std::vector<Pt>& polygon,
                               const std::vector<Tri>& triangles);
