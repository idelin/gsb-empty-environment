#pragma once

#include <cstdint>
#include <string>
#include <vector>

#include "gsb/json.hpp"
#include "gsb/model.hpp"

namespace gsb {

enum class SolveStatus { Optimal, Unsat, Undetermined };

std::string solveStatusName(SolveStatus status);

struct Conflict {
  std::string kind;
  std::string detail;
  std::string export_id;
  uint64_t position = 0;
  std::string page_id;
  uint64_t page_offset = 0;
  std::vector<int64_t> blocked_items;

  Json toJson() const;
};

struct SolveOptions {
  uint64_t node_budget = 2000000;
};

struct SolveResult {
  SolveStatus status = SolveStatus::Undetermined;
  std::vector<int64_t> selected;
  uint64_t total_cost = 0;
  std::vector<Conflict> conflicts;
  uint64_t nodes_explored = 0;
  uint64_t node_budget = 0;
  bool has_best_known = false;
  std::vector<int64_t> best_known;
  uint64_t best_known_cost = 0;
};

SolveResult solve(const Model& model, SolveOptions options = {});

// Structural validation of a concrete plan.  If check_optimality is true the
// solver is re-run and the plan must match the optimum (unless the budget is
// exhausted, in which case the result carries an explanatory warning).
struct ValidationResult {
  bool valid = false;
  bool optimality_checked = false;
  bool optimality_verified = false;
  std::string message;
  std::vector<std::string> errors;
  std::vector<std::string> warnings;
  SolveResult recomputed;
};

ValidationResult validatePlan(const Model& model, const Json& plan,
                              bool check_optimality = true,
                              SolveOptions options = {});

}  // namespace gsb
