#include "netsec.hpp"

#include <algorithm>

namespace netsec {

namespace {

std::uintptr_t as_address(const void* pointer) {
    return reinterpret_cast<std::uintptr_t>(pointer);
}

}  // namespace

std::string_view reason(CheckStatus status) {
    switch (status) {
        case CheckStatus::Ok:
            return "ok";
        case CheckStatus::UnknownTarget:
            return "target is not a registered function entry";
        case CheckStatus::AddressNotTaken:
            return "function address was never taken";
        case CheckStatus::TypeMismatch:
            return "function type does not match the call-site type";
        case CheckStatus::InteriorPointer:
            return "pointer lands inside a function body rather than at its entry";
        case CheckStatus::FoldedAliasConflict:
            return "ICF-aliased functions with different types share one entry";
    }
    return "unknown check status";
}

Violation::Violation(CheckStatus status)
    : std::runtime_error(std::string(reason(status))), status(status) {}

void Registry::register_target(const TargetRecord& record) {
    if (record.entry == nullptr) {
        return;
    }

    std::lock_guard<std::mutex> lock(mutex_);
    auto [it, inserted] = slots_.try_emplace(record.entry);
    Slot& slot = it->second;

    if (inserted) {
        slot.entry = record.entry;
        slot.type = record.type;
        slot.size = record.size;
        slot.address_taken = record.address_taken;
        return;
    }

    // 同一模块重复登记或 ICF 后完全同类型的别名可以合并；
    // 类型不同的折叠别名不能共享类型，直接将该槽位置为冲突。
    if (slot.type != record.type) {
        slot.conflicted = true;
    }
    slot.size = std::max(slot.size, record.size);
    slot.address_taken = slot.address_taken || record.address_taken;
}

void Registry::load_module(const std::string& module_name,
                           const std::vector<TargetRecord>& records) {
    (void)module_name;  // 预留：真实加载器可用于卸载和诊断。
    for (const TargetRecord& record : records) {
        register_target(record);
    }
}

CheckResult Registry::validate(const void* candidate,
                               std::type_index expected) const {
    if (candidate == nullptr) {
        return CheckResult{CheckStatus::UnknownTarget};
    }

    std::lock_guard<std::mutex> lock(mutex_);

    auto exact = slots_.find(candidate);
    if (exact != slots_.end()) {
        const Slot& slot = exact->second;
        if (slot.conflicted) {
            return CheckResult{CheckStatus::FoldedAliasConflict};
        }
        if (!slot.address_taken) {
            return CheckResult{CheckStatus::AddressNotTaken};
        }
        if (slot.type != expected) {
            return CheckResult{CheckStatus::TypeMismatch};
        }
        return CheckResult{CheckStatus::Ok};
    }

    // 精确入口未命中时，检查地址是否落在某个已登记函数体内部。
    const std::uintptr_t target = as_address(candidate);
    for (const auto& [entry, slot] : slots_) {
        const std::uintptr_t begin = as_address(entry);
        if (target > begin && target < begin + slot.size) {
            return CheckResult{CheckStatus::InteriorPointer};
        }
    }

    return CheckResult{CheckStatus::UnknownTarget};
}

void Registry::reset() {
    std::lock_guard<std::mutex> lock(mutex_);
    slots_.clear();
}

Registry& default_registry() {
    static Registry registry;
    return registry;
}

void load_module(const std::string& module_name,
                 const std::vector<TargetRecord>& records) {
    default_registry().load_module(module_name, records);
}

}  // namespace netsec
