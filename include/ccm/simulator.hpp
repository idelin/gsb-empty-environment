// Bounded adversarial simulator comparing the classic double-fetch pattern
// against the single-snapshot marshal engine.  Two deterministic, targeted
// attacks are provided on top of the randomized adversary:
//
//   LengthDoubleFetch: the host swaps a validated length for an overrun value
//     in the gap between the validation fetch and the use fetch.
//   PointerSwap:       the host swaps a validated external pointer for the
//     address of enclave private memory after validation.
//
// The vulnerable handler must be caught by the monitor every time; the safe
// handler must never produce a monitor compromise under any interleaving.
#ifndef CCM_SIMULATOR_HPP
#define CCM_SIMULATOR_HPP

#include <cstring>
#include <memory>
#include <vector>

#include "ccm/abi_decode.hpp"
#include "ccm/adversary.hpp"
#include "ccm/demo_abi.hpp"
#include "ccm/marshaller.hpp"
#include "ccm/monitor.hpp"

namespace ccm {

enum class AttackKind {
    None,
    LengthDoubleFetch,
    PointerSwap,
    RandomTamper,
};

struct WorldLayout {
    addr_t enclave_base = 0;
    addr_t enclave_end = 0;
    addr_t shared_base = 0;
    len_t shared_size = 0;
    len_t envelope_offset = 0;
    len_t records_offset = 0;
    len_t single_offset = 0;
    len_t output_offset = 0;
};

struct HandlerReport {
    bool accepted = false;
    bool monitor_compromised = false;
    len_t bus_reads = 0;
    len_t external_bytes_read = 0;
    StatusCode library_status = StatusCode::Ok;
    std::vector<ViolationKind> violations;
};

class SimWorld {
public:
    static constexpr len_t kSecretSize = 64;
    static constexpr len_t kSharedSize = 4096;
    static constexpr len_t kEnclaveSize = 4096;

    SimWorld()
        : bus_(kSharedBase, kSharedSize),
          space_(AddressSpace::create(
                     kEnclaveBase,
                     kEnclaveBase + static_cast<addr_t>(kEnclaveSize),
                     std::vector<Region>{
                         Region{kSharedBase, kSharedSize}})
                     .value()),
          monitor_(space_),
          schema_(demo::build_schema()) {
        monitor_.attach(bus_);
        layout_.enclave_base = kEnclaveBase;
        layout_.enclave_end =
            kEnclaveBase + static_cast<addr_t>(kEnclaveSize);
        layout_.shared_base = kSharedBase;
        layout_.shared_size = kSharedSize;
        layout_.envelope_offset = 0;
        // Envelope block reserves fixed header plus the maximum tail so its
        // variable tail never aliases another block.
        layout_.records_offset = 512;
        layout_.single_offset = 1024;
        layout_.output_offset = 2048;
        secret_.assign(kSecretSize, 0);
        for (len_t i = 0; i < kSecretSize; ++i) {
            secret_[i] = static_cast<std::uint8_t>(0xA0 + (i & 0x0F));
        }
    }

    static constexpr addr_t kEnclaveBase = 0x100000;
    static constexpr addr_t kSharedBase = 0x200000;

    // Builds a well-formed envelope with one records array and one nested
    // record.  All pointers are initially valid external addresses.
    void build_benign_input(u32_t record_count, u32_t tail_len) {
        std::vector<std::uint8_t> env(demo::kEnvelopeSize, 0);
        write_le32(env.data() + demo::kEnvMagicOff, demo::kExpectedMagic);
        write_le32(env.data() + demo::kEnvVersionOff,
                   demo::kExpectedVersion);
        write_le32(env.data() + demo::kEnvCountOff, record_count);
        write_le32(env.data() + demo::kEnvTailLenOff, tail_len);
        write_le64(env.data() + demo::kEnvRecordsOff,
                   static_cast<u64_t>(records_addr()));
        write_le64(env.data() + demo::kEnvOutOff,
                   static_cast<u64_t>(output_addr()));
        write_le64(env.data() + demo::kEnvSingleOff,
                   static_cast<u64_t>(single_addr()));
        write_le64(env.data() + demo::kEnvLimitOff, demo::kMaxLimit);
        bus_.host_poke(envelope_addr(), env.data(), env.size());

        for (u32_t i = 0; i < record_count; ++i) {
            std::vector<std::uint8_t> record(demo::kRecordSize, 0);
            write_le32(record.data() + demo::kRecordIdOff,
                       static_cast<u32_t>(100 + i));
            write_le32(record.data() + demo::kRecordLenOff, 8U);
            for (len_t tag = 0; tag < demo::kRecordTagSize; ++tag) {
                record[demo::kRecordTagOff + tag] =
                    static_cast<std::uint8_t>('T' + tag);
            }
            write_le64(record.data() + demo::kRecordNextOff, 0U);
            addr_t at = static_cast<addr_t>(
                records_addr() +
                static_cast<addr_t>(i) * demo::kRecordSize);
            bus_.host_poke(at, record.data(), record.size());
        }

        std::vector<std::uint8_t> single(demo::kRecordSize, 0);
        write_le32(single.data() + demo::kRecordIdOff, 777U);
        write_le32(single.data() + demo::kRecordLenOff, 4U);
        write_le64(single.data() + demo::kRecordNextOff, 0U);
        bus_.host_poke(single_addr(), single.data(), single.size());

        std::vector<std::uint8_t> tail(tail_len, 0);
        for (u32_t i = 0; i < tail_len; ++i) {
            tail[i] = static_cast<std::uint8_t>(i & 0xFF);
        }
        bus_.host_poke(
            envelope_addr() + demo::kEnvelopeSize, tail.data(),
            static_cast<len_t>(tail_len));
    }

    addr_t envelope_addr() const {
        return layout_.shared_base + layout_.envelope_offset;
    }
    addr_t records_addr() const {
        return layout_.shared_base + layout_.records_offset;
    }
    addr_t single_addr() const {
        return layout_.shared_base + layout_.single_offset;
    }
    addr_t output_addr() const {
        return layout_.shared_base + layout_.output_offset;
    }
    addr_t secret_addr() const { return layout_.enclave_base + 128; }

    SharedBus& bus() { return bus_; }
    const AddressSpace& space() const { return space_; }
    SecurityMonitor& monitor() { return monitor_; }
    const Schema& schema() const { return schema_; }
    const WorldLayout& layout() const { return layout_; }
    std::vector<std::uint8_t>& secret() { return secret_; }

private:
    WorldLayout layout_;
    SharedBus bus_;
    AddressSpace space_;
    SecurityMonitor monitor_;
    Schema schema_;
    std::vector<std::uint8_t> secret_;
};

inline std::vector<ViolationKind> collect_violations(
    const SecurityMonitor& monitor) {
    std::vector<ViolationKind> kinds;
    for (const Violation& violation : monitor.violations()) {
        kinds.push_back(violation.kind);
    }
    return kinds;
}

// Installs one deterministic targeted attack (or a bounded random adversary).
inline void install_attack(SimWorld& world, AttackKind kind,
                           std::uint64_t seed) {
    SharedBus& bus = world.bus();
    switch (kind) {
        case AttackKind::None:
            bus.set_before_read_hook(nullptr);
            return;
        case AttackKind::LengthDoubleFetch: {
            // Fire once, immediately before the vulnerable handler's second
            // envelope fetch: inflate record_count beyond the real array.
            auto counter = std::make_shared<len_t>(0);
            len_t count_offset =
                world.layout().envelope_offset + demo::kEnvCountOff;
            bus.set_before_read_hook(
                [&bus, counter, count_offset](addr_t, len_t) {
                    len_t index = *counter;
                    ++(*counter);
                    if (index != 1) {
                        return;
                    }
                    // Inflate far beyond the real 2-element array and the
                    // shared pool so the second fetch drives an overrun.
                    std::uint8_t inflated[4] = {200, 0, 0, 0};
                    bus.host_poke(bus.base() + count_offset, inflated, 4);
                });
            return;
        }
        case AttackKind::PointerSwap: {
            // Replace the validated records pointer with an enclave address.
            auto counter = std::make_shared<len_t>(0);
            len_t pointer_offset =
                world.layout().envelope_offset + demo::kEnvRecordsOff;
            addr_t secret_address = world.secret_addr();
            bus.set_before_read_hook(
                [&bus, counter, pointer_offset,
                 secret_address](addr_t, len_t) {
                    len_t index = *counter;
                    ++(*counter);
                    if (index != 1) {
                        return;
                    }
                    std::uint8_t bytes[8];
                    write_le64(bytes, static_cast<u64_t>(secret_address));
                    bus.host_poke(bus.base() + pointer_offset, bytes, 8);
                });
            return;
        }
        case AttackKind::RandomTamper: {
            std::vector<len_t> offsets = {
                world.layout().envelope_offset + demo::kEnvCountOff,
                world.layout().envelope_offset + demo::kEnvTailLenOff,
                world.layout().envelope_offset + demo::kEnvLimitOff,
                world.layout().records_offset,
                world.layout().single_offset};
            auto adversary =
                std::make_shared<Adversary>(bus, seed, std::move(offsets));
            adversary->set_bounds(64, 4);
            bus.set_before_read_hook(
                [adversary](addr_t address, len_t size) {
                    adversary->on_read(address, size);
                });
            return;
        }
    }
}

// The deliberately vulnerable pattern: validate against one fetch, then trust
// a second fetch of the same bytes.  It intentionally contains no deep copy.
inline HandlerReport run_vulnerable_handler(SimWorld& world) {
    HandlerReport report;
    SecurityMonitor& monitor = world.monitor();
    SharedBus& bus = world.bus();
    const AddressSpace& space = world.space();

    std::vector<std::uint8_t> header(demo::kEnvelopeSize);
    // Fetch #1: validation view.
    if (!bus.read(world.envelope_addr(), header.data(), header.size())) {
        report.library_status = StatusCode::OutsideExternal;
        return report;
    }

    u32_t validated_count =
        read_le32(header.data() + demo::kEnvCountOff);
    u64_t validated_records =
        read_le64(header.data() + demo::kEnvRecordsOff);
    u64_t output_pointer =
        read_le64(header.data() + demo::kEnvOutOff);

    PointerAuditor auditor(space);
    Status records_check = auditor.audit_array(
        static_cast<addr_t>(validated_records), demo::kRecordSize,
        validated_count);
    if (!records_check.ok()) {
        monitor.record(ViolationKind::RejectedByAuditor,
                       static_cast<addr_t>(validated_records), 0,
                       records_check.message);
        report.library_status = records_check.code;
        report.violations = collect_violations(monitor);
        report.monitor_compromised = monitor.compromised();
        report.bus_reads = bus.read_event_count();
        return report;
    }
    Status output_check = auditor.audit(
        static_cast<addr_t>(output_pointer), 1);
    if (!output_check.ok()) {
        monitor.record(ViolationKind::RejectedByAuditor,
                       static_cast<addr_t>(output_pointer), 1,
                       output_check.message);
        report.library_status = output_check.code;
        report.violations = collect_violations(monitor);
        report.monitor_compromised = monitor.compromised();
        report.bus_reads = bus.read_event_count();
        return report;
    }

    // Fetch #2 of the same bytes: classic double fetch.  The host mutates the
    // shared page in this exact gap.
    std::vector<std::uint8_t> use_header(demo::kEnvelopeSize);
    if (!bus.read(world.envelope_addr(), use_header.data(),
                  use_header.size())) {
        report.library_status = StatusCode::OutsideExternal;
        report.violations = collect_violations(monitor);
        report.monitor_compromised = monitor.compromised();
        report.bus_reads = bus.read_event_count();
        return report;
    }
    u32_t use_count = read_le32(use_header.data() + demo::kEnvCountOff);
    u64_t use_records =
        read_le64(use_header.data() + demo::kEnvRecordsOff);

    if (use_count != validated_count) {
        monitor.record(ViolationKind::TamperedValue,
                       world.envelope_addr() + demo::kEnvCountOff, 4,
                       "record count changed between validation and use");
    }
    if (use_records != validated_records) {
        monitor.record(ViolationKind::TamperedValue,
                       world.envelope_addr() + demo::kEnvRecordsOff, 8,
                       "records pointer changed between validation and use");
    }

    // The bug: use the re-fetched, attacker-controlled values without
    // re-auditing and without a private snapshot.
    std::vector<std::uint8_t> sink(demo::kRecordSize, 0);
    for (u32_t i = 0; i < use_count; ++i) {
        len_t offset =
            static_cast<len_t>(i) * demo::kRecordSize;
        addr_t element =
            static_cast<addr_t>(use_records) + offset;
        if (!bus.read(element, sink.data(), demo::kRecordSize)) {
            monitor.record(ViolationKind::DoubleFetchOverrun, element,
                           demo::kRecordSize,
                           "overrun pointer dereference blocked by bus");
            report.library_status = StatusCode::AdversarialViolation;
            report.violations = collect_violations(monitor);
            report.monitor_compromised = monitor.compromised();
            report.bus_reads = bus.read_event_count();
            return report;
        }
    }

    report.accepted = true;
    report.violations = collect_violations(monitor);
    report.monitor_compromised = monitor.compromised();
    report.bus_reads = bus.read_event_count();
    return report;
}

// The safe pattern: one audited snapshot, all logic on private memory.
inline HandlerReport run_safe_handler(SimWorld& world) {
    HandlerReport report;
    SecurityMonitor& monitor = world.monitor();
    SharedBus& raw_bus = world.bus();
    ReadOnceBus bus(raw_bus);
    bus.set_double_read_hook([&monitor](addr_t address, len_t size) {
        monitor.record(ViolationKind::ByteReadTwice, address, size,
                       "safe handler attempted to re-read external bytes");
    });

    Marshaller marshaller(world.space(), world.schema(), bus);
    marshaller.set_semantic_validator(
        [](const std::vector<MarshaledNode>& nodes,
           const Schema&) -> Status {
        for (const MarshaledNode& node : nodes) {
            if (node.type != demo::kTypeEnvelope) {
                continue;
            }
            u32_t magic =
                read_le32(node.data.data() + demo::kEnvMagicOff);
            u32_t version =
                read_le32(node.data.data() + demo::kEnvVersionOff);
            u32_t count =
                read_le32(node.data.data() + demo::kEnvCountOff);
            u32_t tail =
                read_le32(node.data.data() + demo::kEnvTailLenOff);
            u64_t limit =
                read_le64(node.data.data() + demo::kEnvLimitOff);
            if (magic != demo::kExpectedMagic ||
                version != demo::kExpectedVersion) {
                return Status{StatusCode::SemanticViolation,
                              "magic/version mismatch in private snapshot"};
            }
            if (count > demo::kMaxRecordCount ||
                tail > demo::kMaxTailLen || limit > demo::kMaxLimit) {
                return Status{StatusCode::SemanticViolation,
                              "business bound exceeded in private snapshot"};
            }
        }
        return Status::ok_status();
    });

    auto marshaled =
        marshaller.marshal(demo::kTypeEnvelope, world.envelope_addr());
    if (!marshaled.ok()) {
        monitor.record(ViolationKind::RejectedByAuditor, world.envelope_addr(),
                       0, marshaled.status().message);
        report.library_status = marshaled.status().code;
        report.violations = collect_violations(monitor);
        report.monitor_compromised = monitor.compromised();
        report.bus_reads = raw_bus.read_event_count();
        report.external_bytes_read = marshaller.fetched_byte_count();
        return report;
    }

    // Business logic dereferences only swizzled private pointers.
    const std::vector<MarshaledNode>& nodes = marshaled.value();
    const MarshaledNode* envelope = nullptr;
    for (const MarshaledNode& node : nodes) {
        if (node.type == demo::kTypeEnvelope) {
            envelope = &node;
            break;
        }
    }
    if (envelope == nullptr) {
        report.library_status = StatusCode::SemanticViolation;
        return report;
    }
    u64_t output_pointer =
        read_le64(envelope->data.data() + demo::kEnvOutOff);
    std::uint8_t digest = 0;
    for (std::uint8_t byte : envelope->data) {
        digest = static_cast<std::uint8_t>(digest ^ byte);
    }
    std::uint8_t output[1] = {digest};
    Status written = marshaller.write_output(
        static_cast<addr_t>(output_pointer), output, 1);
    if (!written.ok()) {
        monitor.record(ViolationKind::RejectedByAuditor,
                       static_cast<addr_t>(output_pointer), 1,
                       written.message);
        report.library_status = written.code;
    } else {
        report.accepted = true;
    }

    report.violations = collect_violations(monitor);
    report.monitor_compromised = monitor.compromised();
    report.bus_reads = raw_bus.read_event_count();
    report.external_bytes_read = marshaller.fetched_byte_count();
    return report;
}

}  // namespace ccm

#endif  // CCM_SIMULATOR_HPP
