#pragma once
#include <cstddef>
#include <cstdint>
#include <functional>
#include <stdexcept>
#include <vector>

namespace netsec {

enum class ValType : std::uint8_t { i32, i64, f32, f64 };

struct FuncType {
    std::vector<ValType> params;
    std::vector<ValType> results;
};

bool operator==(const FuncType& lhs, const FuncType& rhs);
bool operator!=(const FuncType& lhs, const FuncType& rhs);

struct Trap : std::runtime_error {
    using std::runtime_error::runtime_error;
};

using Value = std::int64_t;
using HostFunc = std::function<std::vector<Value>(const std::vector<Value>&)>;

struct Func {
    FuncType type;
    HostFunc callable;
};

class Table {
public:
    std::size_t emplace(Func func);
    const Func& at(std::size_t index) const;
    std::size_t size() const { return entries_.size(); }

    // 间接调用裁决：期望类型与表项实际类型必须全等，否则陷入。
    std::vector<Value> call_indirect(const FuncType& expected,
                                     std::size_t index,
                                     const std::vector<Value>& args) const;

private:
    std::vector<Func> entries_;
};

} // namespace netsec

// 可导入的 C ABI 解码器：供运行时做间接调用的类型全等裁决。
extern "C" bool netsec_types_equal(const netsec::ValType* lhs_params, std::size_t lhs_param_count,
                                   const netsec::ValType* lhs_results, std::size_t lhs_result_count,
                                   const netsec::ValType* rhs_params, std::size_t rhs_param_count,
                                   const netsec::ValType* rhs_results, std::size_t rhs_result_count);
