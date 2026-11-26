#pragma once

#include "module_plan/module_plan.hpp"

#include <cstdint>
#include <string>
#include <vector>

namespace mplan {

struct MatrixCase {
    std::string id;
    std::string title;
    StatusCode expected = StatusCode::Ok;
    StatusCode actual = StatusCode::Ok;
    bool matched = false;
    std::uint64_t peakBytes = 0;
    std::uint64_t budgetBytes = 0;
    bool reviewOk = true;
    std::string detail;
};

std::vector<MatrixCase> runExhaustiveMatrix();

} // namespace mplan
