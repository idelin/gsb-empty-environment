#pragma once

#include <cstddef>
#include <cstdint>
#include <mutex>
#include <stdexcept>
#include <string>
#include <string_view>
#include <typeindex>
#include <typeinfo>
#include <unordered_map>
#include <utility>
#include <vector>

namespace netsec {

enum class CheckStatus {
    Ok,
    UnknownTarget,
    AddressNotTaken,
    TypeMismatch,
    InteriorPointer,
    FoldedAliasConflict,
};

struct CheckResult {
    CheckStatus status;

    bool allowed() const { return status == CheckStatus::Ok; }
    explicit operator bool() const { return allowed(); }
};

std::string_view reason(CheckStatus status);

struct Violation : std::runtime_error {
    CheckStatus status;

    explicit Violation(CheckStatus status);
};

// 模块加载期产出的一条登记信息：函数入口、函数类型标识、
// 函数体字节数（用于识别函数体内部地址）以及取地址标记。
struct TargetRecord {
    const void* entry;
    std::type_index type;
    std::size_t size;
    bool address_taken;
};

template <typename Fn>
TargetRecord make_target_record(const Fn* entry, std::size_t size, bool address_taken) {
    return TargetRecord{
        reinterpret_cast<const void*>(entry),
        std::type_index(typeid(Fn)),
        size,
        address_taken,
    };
}

class Registry {
public:
    Registry() = default;
    Registry(const Registry&) = delete;
    Registry& operator=(const Registry&) = delete;

    // 模块加载时逐条登记；同一入口登记不同类型视为 ICF 折叠冲突，
    // 该入口将被永久拒绝（失败关闭）。
    void register_target(const TargetRecord& record);
    void load_module(const std::string& module_name,
                     const std::vector<TargetRecord>& records);

    CheckResult validate(const void* candidate, std::type_index expected) const;

    // 调用点通过函数指针类型隐式提交期望类型。
    template <typename Fn>
    CheckResult validate(const Fn* candidate) const {
        return validate(reinterpret_cast<const void*>(candidate),
                        std::type_index(typeid(Fn)));
    }

    void reset();

private:
    struct Slot {
        const void* entry = nullptr;
        std::type_index type = std::type_index(typeid(void));
        std::size_t size = 0;
        bool address_taken = false;
        bool conflicted = false;
    };

    std::unordered_map<const void*, Slot> slots_;
    mutable std::mutex mutex_;
};

Registry& default_registry();
void load_module(const std::string& module_name,
                 const std::vector<TargetRecord>& records);

// 前向间接调用的受控入口：先校验再调用，校验失败抛 Violation。
template <typename R, typename... Args>
R checked_call_with(Registry& registry, R (*fn)(Args...), Args... args) {
    CheckResult result = registry.validate(
        reinterpret_cast<const void*>(fn),
        std::type_index(typeid(R(Args...))));
    if (!result) {
        throw Violation(result.status);
    }
    return fn(std::forward<Args>(args)...);
}

// 前向间接调用的受控入口：先校验再调用，校验失败抛 Violation。
template <typename R, typename... Args>
R checked_call(R (*fn)(Args...), Args... args) {
    return checked_call_with(default_registry(), fn, std::forward<Args>(args)...);
}

}  // namespace netsec
