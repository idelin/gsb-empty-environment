#include "netsec.hpp"

#include <sstream>

namespace netsec {

bool operator==(const FuncType& lhs, const FuncType& rhs) {
    return lhs.params == rhs.params && lhs.results == rhs.results;
}

bool operator!=(const FuncType& lhs, const FuncType& rhs) {
    return !(lhs == rhs);
}

std::size_t Table::emplace(Func func) {
    entries_.push_back(std::move(func));
    return entries_.size() - 1;
}

const Func& Table::at(std::size_t index) const {
    if (index >= entries_.size()) {
        throw Trap("undefined element: table index out of bounds");
    }
    return entries_[index];
}

std::vector<Value> Table::call_indirect(const FuncType& expected,
                                        std::size_t index,
                                        const std::vector<Value>& args) const {
    const Func& target = at(index);
    // 裁决：类型索引指向的期望类型与表项实际类型必须全等；
    // 不等则陷入，绝不按另一签名解释参数。
    if (!netsec_types_equal(expected.params.data(), expected.params.size(),
                            expected.results.data(), expected.results.size(),
                            target.type.params.data(), target.type.params.size(),
                            target.type.results.data(), target.type.results.size())) {
        std::ostringstream oss;
        oss << "indirect call type mismatch: expected (" << expected.params.size()
            << " params, " << expected.results.size() << " results) but table entry "
            << index << " has (" << target.type.params.size() << " params, "
            << target.type.results.size() << " results)";
        throw Trap(oss.str());
    }
    return target.callable(args);
}

} // namespace netsec

extern "C" bool netsec_types_equal(const netsec::ValType* lhs_params, std::size_t lhs_param_count,
                                   const netsec::ValType* lhs_results, std::size_t lhs_result_count,
                                   const netsec::ValType* rhs_params, std::size_t rhs_param_count,
                                   const netsec::ValType* rhs_results, std::size_t rhs_result_count) {
    if (lhs_param_count != rhs_param_count || lhs_result_count != rhs_result_count) {
        return false;
    }
    for (std::size_t i = 0; i < lhs_param_count; ++i) {
        if (lhs_params[i] != rhs_params[i]) {
            return false;
        }
    }
    for (std::size_t i = 0; i < lhs_result_count; ++i) {
        if (lhs_results[i] != rhs_results[i]) {
            return false;
        }
    }
    return true;
}
