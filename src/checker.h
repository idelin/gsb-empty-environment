#pragma once
#include <string>

// Independent collision / placement checker.
// Deliberately shares no logic with the solver: it re-interprets the
// instance and placement from a plain-text spec and validates the raw
// geometric constraints (border padding + pairwise separation) directly.
//
// Spec text format:
//   S
//   n pad mode            mode: "shared"/"0" or "exclusive"/"1"
//   w h                   (n lines, texture sizes)
//   x y rot               (n lines, placement; rot: 0/1)
//
// Returns an empty string when the placement is valid, otherwise a
// human-readable description of the first violations found.
namespace atlascheck {
std::string verify(const std::string& spec);
}
