#pragma once

#include <cstdint>
#include <optional>
#include <string>
#include <string_view>

#include "gsb/json.hpp"
#include "gsb/model.hpp"
#include "gsb/solver.hpp"

namespace gsb {

Json buildPlanJson(const Model& model, const SolveResult& result);

struct EvidenceResult {
  bool ok = false;
  std::string error;
  Json evidence;
};

// Reads back mask bits and a provenance chain for a short output range.  The
// returned evidence never contains page byte values.
EvidenceResult inspectRange(const Model& model, const Json& plan,
                            std::string_view export_id, uint64_t start,
                            uint64_t length, uint64_t max_length = 4096);

}  // namespace gsb
