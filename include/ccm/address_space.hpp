// Models the virtual address layout seen by an enclave:
//   - one private enclave range [encBase, encEnd)
//   - one or more external shared-memory pools (OCALL/ECALL buffers)
// Every attacker-supplied span must be proven fully external and disjoint
// from the private range before a single byte is touched.
#ifndef CCM_ADDRESS_SPACE_HPP
#define CCM_ADDRESS_SPACE_HPP

#include <vector>

#include "ccm/checked_math.hpp"
#include "ccm/status.hpp"
#include "ccm/types.hpp"

namespace ccm {

struct Region {
    addr_t base = 0;
    len_t size = 0;
};

class AddressSpace {
public:
    AddressSpace() = default;
    AddressSpace(addr_t enc_base, addr_t enc_end, std::vector<Region> external)
        : enc_base_(enc_base),
          enc_end_(enc_end),
          external_(std::move(external)) {}

    // Validates the layout itself: the private range must be non-empty, must
    // not wrap, and external pools must not overlap it or each other.
    static Result<AddressSpace> create(addr_t enc_base, addr_t enc_end,
                                       std::vector<Region> external) {
        if (enc_end <= enc_base) {
            return Status{StatusCode::InvalidArgument,
                          "enclave range must be non-empty and well ordered"};
        }
        for (len_t i = 0; i < external.size(); ++i) {
            const Region& region = external[i];
            addr_t region_end = 0;
            if (checked_span_end(region.base, region.size, &region_end)) {
                return Status{StatusCode::Overflow,
                              "external region end wraps the address space"};
            }
            if (region.size == 0) {
                return Status{StatusCode::InvalidArgument,
                              "external region must be non-empty"};
            }
            if (region_end > enc_base && region.base < enc_end) {
                return Status{StatusCode::OverlapsEnclave,
                              "external region overlaps the enclave range"};
            }
            for (len_t j = 0; j < i; ++j) {
                const Region& other = external[j];
                addr_t other_end = static_cast<addr_t>(other.base + other.size);
                if (region_end > other.base && region.base < other_end) {
                    return Status{
                        StatusCode::OverlappingRegions,
                        "external regions must not overlap each other"};
                }
            }
        }
        return AddressSpace{enc_base, enc_end, std::move(external)};
    }

    addr_t enclave_base() const { return enc_base_; }
    addr_t enclave_end() const { return enc_end_; }
    const std::vector<Region>& external_regions() const { return external_; }

    bool contains_enclave(addr_t address) const {
        return address >= enc_base_ && address < enc_end_;
    }

    // True when [base, base + size) has any byte in common with the private
    // enclave range.  The caller guarantees base + size does not wrap.
    bool intersects_enclave(addr_t base, addr_t end) const {
        return end > enc_base_ && base < enc_end_;
    }

    // The core audit primitive.  Rejects wrapped spans, spans touching enclave
    // memory and spans not fully contained in a single external pool.
    Status audit_span(addr_t base, len_t size) const {
        if (size == 0) {
            return Status::ok_status();  // nothing to dereference
        }
        addr_t end = 0;
        if (checked_span_end(base, size, &end)) {
            return Status{StatusCode::Overflow,
                          "span end overflows the address space"};
        }
        if (intersects_enclave(base, end)) {
            return Status{
                StatusCode::OverlapsEnclave,
                "attacker span intersects private enclave memory"};
        }
        for (const Region& region : external_) {
            addr_t region_end = static_cast<addr_t>(region.base + region.size);
            if (base >= region.base && end <= region_end) {
                return Status::ok_status();
            }
        }
        return Status{
            StatusCode::OutsideExternal,
            "span is not fully contained in an external shared pool"};
    }

private:
    addr_t enc_base_ = 0;
    addr_t enc_end_ = 0;
    std::vector<Region> external_;
};

}  // namespace ccm

#endif  // CCM_ADDRESS_SPACE_HPP
