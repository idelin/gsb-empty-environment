#include "module_plan/exhaustive.hpp"

#include <iomanip>
#include <iostream>

int main() {
    const auto cases = mplan::runExhaustiveMatrix();

    std::cout << "| case | expected | actual | peak/budget | review | result |\n";
    std::cout << "|---|---|---|---:|---|---|\n";
    bool allMatched = true;
    for (const auto& item : cases) {
        allMatched = allMatched && item.matched;
        std::cout << "| `" << item.id << "` | `"
                  << mplan::toString(item.expected) << "` | `"
                  << mplan::toString(item.actual) << "` | "
                  << item.peakBytes << '/' << item.budgetBytes << " | "
                  << (item.reviewOk ? "pass" : "fail") << " | "
                  << (item.matched ? "MATCH" : "MISMATCH") << " |\n";
        if (!item.matched && !item.detail.empty()) {
            std::cerr << item.id << ": " << item.detail << '\n';
        }
    }

    std::cout << "\n" << cases.size() << " scenarios; "
              << (allMatched ? "all matched" : "failures present") << '\n';
    return allMatched ? 0 : 1;
}
