// Single-snapshot deep marshal engine.
//
// The host may rewrite any shared byte at any time, so every external byte is
// (1) proven to lie fully outside the enclave with overflow-checked span math,
// (2) claimed once so no two targets can alias it, and (3) copied across the
// boundary exactly once.  After the copy, length handling, cycle detection and
// pointer relocation all operate on private memory; the untrusted bus is never
// consulted again.
//
// Traversal is depth-first with an explicit stack.  A block is marked
// in-progress while its children are planned, which detects alias cycles and
// guarantees a parent array claims its full span before element headers.
#ifndef CCM_MARSHALLER_HPP
#define CCM_MARSHALLER_HPP

#include <functional>
#include <limits>
#include <deque>
#include <unordered_map>
#include <unordered_set>
#include <vector>

#include "ccm/abi_decode.hpp"
#include "ccm/auditor.hpp"
#include "ccm/checked_math.hpp"
#include "ccm/schema.hpp"
#include "ccm/shared_bus.hpp"
#include "ccm/status.hpp"
#include "ccm/types.hpp"

namespace ccm {

struct MarshaledNode {
    TypeId type = 0;
    addr_t external_addr = 0;
    len_t header_size = 0;
    len_t tail_size = 0;
    std::vector<std::uint8_t> data;

    len_t total_size() const { return static_cast<len_t>(data.size()); }

    const std::uint8_t* field_ptr(const FieldDesc& field) const {
        return data.data() + field.offset;
    }

    u64_t scalar(const FieldDesc& field) const {
        return decode_scalar(field.width, field_ptr(field));
    }

    const std::uint8_t* tail_data() const {
        return data.data() + header_size;
    }
};

using SemanticValidator =
    std::function<Status(const std::vector<MarshaledNode>&, const Schema&)>;

class Marshaller {
public:
    Marshaller(const AddressSpace& space, const Schema& schema, IBus& bus)
        : auditor_(space), schema_(schema), bus_(bus) {}

    Result<std::vector<MarshaledNode>> marshal(TypeId root_type,
                                               addr_t root_address) {
        reset();
        if (schema_.type(root_type) == nullptr) {
            return Status{StatusCode::SchemaInvalid, "unknown root type"};
        }
        constexpr len_t kMaxTraversalDepth = 256;

        std::deque<WorkItem> queue;
        std::deque<WorkItem> nested_queue;
        queue.push_back(WorkItem{root_type, root_address, false});
        queued_.insert(root_address);

        while (!queue.empty() || !nested_queue.empty()) {
            WorkItem item;
            if (!queue.empty()) {
                item = queue.front();
                queue.pop_front();
            } else {
                item = nested_queue.front();
                nested_queue.pop_front();
            }

            const TypeDesc* desc = schema_.type(item.type);
            if (desc == nullptr) {
                return Status{StatusCode::SchemaInvalid,
                              "schema lookup failed during traversal"};
            }
            if (item.depth > kMaxTraversalDepth) {
                return Status{StatusCode::CycleDetected,
                              "pointer graph exceeds the bounded depth limit"};
            }

            std::vector<WorkItem> children;
            Status planned = plan_block(*desc, item.address,
                                        item.header_preclaimed, children);
            if (!planned.ok()) {
                return planned;
            }
            for (const WorkItem& child : children) {
                if (queued_.count(child.address) != 0 ||
                    completed_.count(child.address) != 0) {
                    // Re-dereferencing a visited address is a live back-edge
                    // cycle; plain byte aliasing is separately rejected by
                    // claim_span before this point can be reached.
                    return Status{
                        StatusCode::CycleDetected,
                        "pointer graph re-dereferences a visited block"};
                }
                queued_.insert(child.address);
                WorkItem scheduled = child;
                scheduled.depth = static_cast<len_t>(item.depth + 1);
                if (scheduled.array_element) {
                    queue.push_back(scheduled);
                } else {
                    nested_queue.push_back(scheduled);
                }
            }

            Status finished = finalize_block(item.type, item.address);
            if (!finished.ok()) {
                return finished;
            }
        }

        Status relocated = relocate_pointers();
        if (!relocated.ok()) {
            return relocated;
        }
        if (validator_) {
            Status semantic = validator_(nodes_, schema_);
            if (!semantic.ok()) {
                return semantic;
            }
        }
        return std::move(nodes_);
    }

    void set_semantic_validator(SemanticValidator validator) {
        validator_ = std::move(validator);
    }

    Status write_output(addr_t destination, const std::uint8_t* bytes,
                        len_t size) {
        Status check = auditor_.audit_output(destination, size);
        if (!check.ok()) {
            return check;
        }
        if (!bus_.write(destination, bytes, size)) {
            return Status{StatusCode::OutsideExternal,
                          "bus rejected the output write"};
        }
        return Status::ok_status();
    }

    len_t fetched_byte_count() const {
        len_t total = 0;
        for (const Span& span : fetched_spans_) {
            total += static_cast<len_t>(span.end - span.base);
        }
        return total;
    }

    const PointerAuditor& auditor() const { return auditor_; }

private:
    struct Span {
        addr_t base;
        addr_t end;
    };

    struct WorkItem {
        TypeId type;
        addr_t address;
        bool array_element = false;
        bool header_preclaimed = false;
        len_t depth = 0;
    };

    void reset() {
        nodes_.clear();
        staging_.clear();
        queued_.clear();
        completed_.clear();
        external_to_private_.clear();
        claimed_spans_.clear();
        fetched_spans_.clear();
    }

    static bool overlaps(const Span& span, addr_t base, addr_t end) {
        return end > span.base && base < span.end;
    }

    Status claim_span(addr_t base, len_t size) {
        if (size == 0) {
            return Status::ok_status();
        }
        addr_t end = 0;
        if (checked_span_end(base, size, &end)) {
            return Status{StatusCode::Overflow,
                          "external span end overflows"};
        }
        Status check = auditor_.audit(base, size);
        if (!check.ok()) {
            return check;
        }
        for (const Span& span : claimed_spans_) {
            if (overlaps(span, base, end)) {
                return Status{
                    StatusCode::OverlappingRegions,
                    "two pointer targets alias the same external bytes"};
            }
        }
        claimed_spans_.push_back(Span{base, end});
        return Status::ok_status();
    }

    Status fetch_once(addr_t address, std::uint8_t* dst, len_t size) {
        if (size == 0) {
            return Status::ok_status();
        }
        addr_t end = static_cast<addr_t>(address + size);
        for (const Span& span : fetched_spans_) {
            if (overlaps(span, address, end)) {
                return Status{StatusCode::ByteReadTwice,
                              "external byte would cross the boundary twice"};
            }
        }
        if (!bus_.read(address, dst, size)) {
            return Status{StatusCode::OutsideExternal,
                          "bus rejected the external fetch"};
        }
        fetched_spans_.push_back(Span{address, end});
        return Status::ok_status();
    }

    // Snapshot one block, then schedule its children.  The block's own claims
    // (header, tail, full array range) are recorded before any child fetch.
    Status plan_block(const TypeDesc& desc, addr_t address,
                      bool header_preclaimed,
                      std::vector<WorkItem>& children) {
        if (!header_preclaimed) {
            Status header_claim = claim_span(address, desc.fixed_size);
            if (!header_claim.ok()) {
                return header_claim;
            }
        }

        MarshaledNode node;
        node.type = desc.id;
        node.external_addr = address;
        node.header_size = desc.fixed_size;
        node.tail_size = 0;
        node.data.resize(desc.fixed_size);

        Status header_fetch =
            fetch_once(address, node.data.data(), desc.fixed_size);
        if (!header_fetch.ok()) {
            return header_fetch;
        }

        len_t total_size = desc.fixed_size;
        for (const FieldDesc& field : desc.fields) {
            if (field.kind == FieldKind::TailBytes) {
                Status tail =
                    plan_tail(desc, field, address, node, &total_size);
                if (!tail.ok()) {
                    return tail;
                }
            }
        }

        for (const FieldDesc& field : desc.fields) {
            if (field.kind == FieldKind::PtrArray) {
                // Claim the full parent array range before nested targets so
                // element headers always resolve against the parent claim.
                Status array =
                    plan_ptr_array(desc, field, node, children);
                if (!array.ok()) {
                    return array;
                }
            } else if (field.kind == FieldKind::NestedPtr) {
                Status nested = plan_nested_ptr(field, node, children);
                if (!nested.ok()) {
                    return nested;
                }
            }
        }

        staging_.push_back(std::move(node));
        return Status::ok_status();
    }

    Status plan_tail(const TypeDesc& desc, const FieldDesc& tail_field,
                     addr_t block_address, MarshaledNode& node,
                     len_t* total_size) {
        const FieldDesc* count = desc.find_field(tail_field.count_field);
        if (count == nullptr) {
            return Status{StatusCode::SchemaInvalid,
                          "tail count field vanished"};
        }
        u64_t raw = node.scalar(*count);
        if (raw >
            static_cast<u64_t>(std::numeric_limits<len_t>::max())) {
            return Status{StatusCode::Overflow,
                          "tail count exceeds addressable length"};
        }
        len_t tail_len = static_cast<len_t>(raw);
        addr_t tail_address =
            static_cast<addr_t>(block_address + desc.fixed_size);
        Status claimed = claim_span(tail_address, tail_len);
        if (!claimed.ok()) {
            return claimed;
        }
        len_t new_total = 0;
        if (checked_add<len_t>(*total_size, tail_len, &new_total)) {
            return Status{StatusCode::Overflow,
                          "tail extension overflows private block size"};
        }
        *total_size = new_total;
        node.tail_size = tail_len;
        node.data.resize(new_total);
        return fetch_once(tail_address,
                          node.data.data() + desc.fixed_size, tail_len);
    }

    Status plan_nested_ptr(const FieldDesc& field,
                           const MarshaledNode& node,
                           std::vector<WorkItem>& children) {
        u64_t pointer = node.scalar(field);
        if (pointer == 0) {
            return Status::ok_status();
        }
        if (schema_.type(field.target_type) == nullptr) {
            return Status{StatusCode::SchemaInvalid,
                          "nested target type missing"};
        }
        children.push_back(
            WorkItem{field.target_type, static_cast<addr_t>(pointer), false});
        return Status::ok_status();
    }

    Status plan_ptr_array(const TypeDesc& desc, const FieldDesc& field,
                          const MarshaledNode& node,
                          std::vector<WorkItem>& children) {
        u64_t pointer = node.scalar(field);
        const FieldDesc* count = desc.find_field(field.count_field);
        if (count == nullptr) {
            return Status{StatusCode::SchemaInvalid,
                          "array count field vanished"};
        }
        u64_t raw_count = node.scalar(*count);
        if (raw_count >
            static_cast<u64_t>(std::numeric_limits<len_t>::max())) {
            return Status{StatusCode::Overflow,
                          "array count exceeds addressable length"};
        }
        len_t element_count = static_cast<len_t>(raw_count);
        if (pointer == 0) {
            if (element_count != 0) {
                return Status{StatusCode::InvalidArgument,
                              "null pointer paired with a non-zero count"};
            }
            return Status::ok_status();
        }
        const TypeDesc* target = schema_.type(field.target_type);
        if (target == nullptr) {
            return Status{StatusCode::SchemaInvalid,
                          "array target type missing"};
        }
        Status array_span = auditor_.audit_array(
            static_cast<addr_t>(pointer), target->fixed_size, element_count);
        if (!array_span.ok()) {
            return array_span;
        }
        len_t total_bytes = 0;
        if (checked_mul<len_t>(target->fixed_size, element_count,
                               &total_bytes)) {
            return Status{StatusCode::Overflow,
                          "array byte size multiplication overflows"};
        }
        Status claimed =
            claim_span(static_cast<addr_t>(pointer), total_bytes);
        if (!claimed.ok()) {
            return claimed;
        }
        for (len_t i = 0; i < element_count; ++i) {
            len_t offset_bytes = 0;
            if (checked_mul<len_t>(target->fixed_size, i, &offset_bytes)) {
                return Status{
                    StatusCode::Overflow,
                    "array element offset multiplication overflows"};
            }
            addr_t element_address = static_cast<addr_t>(
                static_cast<addr_t>(pointer) + offset_bytes);
            WorkItem element;
            element.type = field.target_type;
            element.address = element_address;
            element.array_element = true;
            element.header_preclaimed = true;
            children.push_back(element);
        }
        return Status::ok_status();
    }

    Status finalize_block(TypeId type, addr_t address) {
        MarshaledNode finalized;
        bool found = false;
        for (auto it = staging_.begin(); it != staging_.end(); ++it) {
            if (it->type == type && it->external_addr == address) {
                finalized = std::move(*it);
                staging_.erase(it);
                found = true;
                break;
            }
        }
        if (!found) {
            return Status{StatusCode::InvalidArgument,
                          "internal marshal state inconsistency"};
        }
        queued_.erase(address);
        completed_.insert(address);
        external_to_private_.emplace(
            address, reinterpret_cast<addr_t>(finalized.data.data()));
        nodes_.push_back(std::move(finalized));
        return Status::ok_status();
    }

    Status relocate_pointers() {
        for (MarshaledNode& node : nodes_) {
            const TypeDesc* desc = schema_.type(node.type);
            if (desc == nullptr) {
                return Status{StatusCode::SchemaInvalid,
                              "schema lookup failed during relocation"};
            }
            for (const FieldDesc& field : desc->fields) {
                if (field.kind != FieldKind::NestedPtr &&
                    field.kind != FieldKind::PtrArray) {
                    continue;
                }
                u64_t original = node.scalar(field);
                if (original == 0) {
                    continue;
                }
                auto it =
                    external_to_private_.find(static_cast<addr_t>(original));
                if (it == external_to_private_.end()) {
                    return Status{
                        StatusCode::InvalidArgument,
                        "pointer target missing from the marshaled graph"};
                }
                encode_pointer(node.data.data() + field.offset, it->second);
            }
        }
        return Status::ok_status();
    }

    PointerAuditor auditor_;
    const Schema& schema_;
    IBus& bus_;
    SemanticValidator validator_;
    std::vector<MarshaledNode> nodes_;
    std::vector<MarshaledNode> staging_;
    std::unordered_set<addr_t> queued_;
    std::unordered_set<addr_t> completed_;
    std::unordered_map<addr_t, addr_t> external_to_private_;
    std::vector<Span> claimed_spans_;
    std::vector<Span> fetched_spans_;
};

}  // namespace ccm

#endif  // CCM_MARSHALLER_HPP
