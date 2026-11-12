#pragma once

#include <cstddef>
#include <cstdint>

namespace netsec {

// Bundle 二进制格式（所有多字节整数均为小端）：
//   bundle        := total_len:u32 magic:"BNDL" entry*
//     total_len   := 整个 bundle 的字节数（包含这 4 字节本身），最小为 8
//   entry         := key_len:u32 key_bytes type:u8 value
//   value(type=1) := i32
//   value(type=2) := len:u32 bytes
//   value(type=3) := len:u32 nested_bundle_bytes
//                    （len 必须等于嵌套 bundle 自身的 total_len）
//
// 校验规则：按声明长度跳过与逐字段解析所得的结束位置必须相等；
// 声明长度超过剩余字节、嵌套内部声明与外层不符、键重复均拒绝；
// 总字节数与条目数（跨嵌套累计）受预算限制。

enum class BundleError {
    kOk = 0,
    kByteBudgetExceeded,   // 输入总字节数超过预算
    kEntryBudgetExceeded,  // 累计条目数超过预算
    kDepthExceeded,        // 嵌套层级超过预算
    kTruncated,            // 声明长度超过剩余字节 / 意外结束
    kTrailingBytes,        // 声明总长小于输入，存在残余字节
    kBadMagic,             // 魔数不是 "BNDL"
    kLengthMismatch,       // 声明长度与逐字段解析结果不一致
    kDuplicateKey,         // 同一 bundle 作用域内键重复
    kUnknownValueType,     // 未知值类型码
};

const char* toString(BundleError error) noexcept;

struct BundleValidationOptions {
    std::size_t maxTotalBytes = 1u << 20;  // 输入缓冲区总字节预算
    std::size_t maxEntries = 1024;         // 全部嵌套层级累计条目预算
    std::size_t maxDepth = 32;             // 嵌套层级预算（防御栈溢出）
};

struct BundleValidationResult {
    BundleError error = BundleError::kOk;
    std::size_t offset = 0;  // 失败位置（字节偏移）

    bool ok() const noexcept { return error == BundleError::kOk; }
};

// 供系统服务在反序列化前调用。data 为 null 时按空输入处理。
BundleValidationResult validateBundle(const void* data, std::size_t size,
                                      const BundleValidationOptions& options = {});

}  // namespace netsec
