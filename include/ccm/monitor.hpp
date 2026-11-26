// Security monitor: the trusted observer that records every suspicious event
// produced while a handler touches the boundary.  Both the vulnerable and the
// safe handler are judged by the same monitor, giving a like-for-like verdict.
#ifndef CCM_MONITOR_HPP
#define CCM_MONITOR_HPP

#include <functional>
#include <vector>

#include "ccm/address_space.hpp"
#include "ccm/shared_bus.hpp"
#include "ccm/status.hpp"
#include "ccm/types.hpp"

namespace ccm {

enum class ViolationKind {
    None = 0,
    EnclaveAliasAccess,   // host pointer caused a load/store of private memory
    OutOfBoundsAccess,    // access escaped every external pool
    DoubleFetchOverrun,   // second fetch used a mutated length/pointer
    TamperedValue,        // observed value changed between fetch and use
    ByteReadTwice,        // external byte crossed the boundary more than once
    RejectedByAuditor,    // library correctly refused a hostile input
};

struct Violation {
    ViolationKind kind = ViolationKind::None;
    addr_t address = 0;
    len_t size = 0;
    const char* detail = "";
};

class SecurityMonitor {
public:
    explicit SecurityMonitor(const AddressSpace& space) : space_(space) {}

    void attach(SharedBus& bus) {
        bus.set_violation_hook(
            [this](addr_t address, len_t size) {
                if (space_.contains_enclave(address) ||
                    space_.intersects_enclave(
                        address,
                        static_cast<addr_t>(address + size))) {
                    record(ViolationKind::EnclaveAliasAccess, address, size,
                           "bus access aliases enclave private memory");
                } else {
                    record(ViolationKind::OutOfBoundsAccess, address, size,
                           "bus access escapes the external pools");
                }
            });
    }

    void record(ViolationKind kind, addr_t address, len_t size,
                const char* detail) {
        violations_.push_back(Violation{kind, address, size, detail});
    }

    bool has(ViolationKind kind) const {
        for (const Violation& violation : violations_) {
            if (violation.kind == kind) {
                return true;
            }
        }
        return false;
    }

    bool compromised() const {
        for (const Violation& violation : violations_) {
            if (violation.kind == ViolationKind::EnclaveAliasAccess ||
                violation.kind == ViolationKind::OutOfBoundsAccess ||
                violation.kind == ViolationKind::DoubleFetchOverrun ||
                violation.kind == ViolationKind::TamperedValue ||
                violation.kind == ViolationKind::ByteReadTwice) {
                return true;
            }
        }
        return false;
    }

    const std::vector<Violation>& violations() const { return violations_; }

    void reset() { violations_.clear(); }

private:
    const AddressSpace& space_;
    std::vector<Violation> violations_;
};

inline const char* violation_name(ViolationKind kind) {
    switch (kind) {
        case ViolationKind::None:
            return "None";
        case ViolationKind::EnclaveAliasAccess:
            return "EnclaveAliasAccess";
        case ViolationKind::OutOfBoundsAccess:
            return "OutOfBoundsAccess";
        case ViolationKind::DoubleFetchOverrun:
            return "DoubleFetchOverrun";
        case ViolationKind::TamperedValue:
            return "TamperedValue";
        case ViolationKind::ByteReadTwice:
            return "ByteReadTwice";
        case ViolationKind::RejectedByAuditor:
            return "RejectedByAuditor";
    }
    return "Unknown";
}

}  // namespace ccm

#endif  // CCM_MONITOR_HPP
