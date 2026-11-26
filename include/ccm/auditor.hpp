// Strict pointer/span auditor.  These free functions are the single place
// that decides whether an attacker-supplied byte range may cross the enclave
// boundary.  The marshal engine, output writer and tests all route through
// them so the audit policy cannot be bypassed by convenience wrappers.
#ifndef CCM_AUDITOR_HPP
#define CCM_AUDITOR_HPP

#include "ccm/address_space.hpp"
#include "ccm/checked_math.hpp"
#include "ccm/status.hpp"
#include "ccm/types.hpp"

namespace ccm {

class PointerAuditor {
public:
    explicit PointerAuditor(const AddressSpace& space) : space_(space) {}

    // Audit an input or output span of ordinary bytes.
    Status audit(addr_t base, len_t size) const {
        return space_.audit_span(base, size);
    }

    // Audit [element_base, element_base + element_size * count), proving the
    // multiplication and addition cannot wrap and the full array is external.
    Status audit_array(addr_t element_base, len_t element_size,
                       len_t count) const {
        len_t total = 0;
        if (checked_mul<len_t>(element_size, count, &total)) {
            return Status{StatusCode::Overflow,
                          "array byte size multiplication overflows"};
        }
        return space_.audit_span(element_base, total);
    }

    // Decode an on-wire pointer and immediately prove its entire target span
    // is outside the enclave before any caller can dereference it.
    Status audit_pointer(addr_t pointer, len_t target_size) const {
        return space_.audit_span(pointer, target_size);
    }

    // Output buffers obey the identical discipline: the host must never be
    // handed an alias for private enclave memory.
    Status audit_output(addr_t base, len_t size) const {
        Status span = space_.audit_span(base, size);
        if (!span.ok()) {
            return span;
        }
        return Status::ok_status();
    }

    // Bounds-check an internal (already-private) offset, e.g. a variable
    // structure offset that must stay inside its marshaled block.
    Status audit_internal_offset(len_t block_size, len_t offset,
                                 len_t field_size) const {
        addr_t end = 0;
        if (checked_span_end(offset, field_size, &end)) {
            return Status{StatusCode::Overflow,
                          "internal offset addition overflows"};
        }
        if (end > block_size) {
            return Status{StatusCode::OutOfBoundsOffset,
                          "internal field escapes its enclosing block"};
        }
        return Status::ok_status();
    }

    const AddressSpace& space() const { return space_; }

private:
    const AddressSpace& space_;
};

}  // namespace ccm

#endif  // CCM_AUDITOR_HPP
