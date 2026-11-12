// Verification harness for netsec::shared_buffer.
//
// Simulates attacker-controlled Normal World memory with an injectable write
// hook that fires after every NW read, so the "normal world mutates data
// between two reads" TOCTOU attack can be reproduced deterministically.

#include "netsec/shared_buffer.hpp"

#include <cassert>
#include <cstring>
#include <functional>
#include <iostream>
#include <utility>
#include <vector>

namespace {

int g_checks = 0;
int g_failures = 0;

#define CHECK(cond)                                                            \
    do {                                                                       \
        ++g_checks;                                                            \
        if (!(cond)) {                                                         \
            ++g_failures;                                                      \
            std::cerr << "FAIL [" << __FILE__ << ':' << __LINE__               \
                      << "] " #cond "\n";                                      \
        }                                                                      \
    } while (0)

#define EXPECT_ERROR(result, expected_error)                                   \
    do {                                                                       \
        ++g_checks;                                                            \
        if ((result).ok() || (result).error != (expected_error)) {             \
            ++g_failures;                                                      \
            std::cerr << "FAIL [" << __FILE__ << ':' << __LINE__               \
                      << "] expected error "                                   \
                      << static_cast<int>(expected_error) << " got "          \
                      << static_cast<int>((result).error) << "\n";            \
        }                                                                      \
    } while (0)

constexpr std::uintptr_t kWindowBase = 0x40000000;
constexpr std::size_t kWindowSize = 0x10000;
constexpr std::size_t kAlignment = 8;

// Attacker-controlled Normal World simulation. Reads and writes go through the
// INormalWorldMemory interface; a hook runs after every successful read and
// may corrupt the backing bytes exactly like a hostile NW between two reads.
class SimWorld final : public netsec::INormalWorldMemory {
public:
    using ReadHook = std::function<void(SimWorld&)>;

    SimWorld(std::uintptr_t base, std::size_t size)
        : base_(base), backing_(size, 0) {}

    bool read(void* dst, std::uintptr_t src, std::size_t size) noexcept override {
        if (access_fault_ || !inside(src, size)) return false;
        std::memcpy(dst, backing_.data() + (src - base_), size);
        ++read_count_;
        read_log_.push_back({src, size});
        if (read_hook_) read_hook_(*this);
        return true;
    }

    bool write(std::uintptr_t dst, const void* src,
               std::size_t size) noexcept override {
        if (access_fault_ || !inside(dst, size)) return false;
        std::memcpy(backing_.data() + (dst - base_), src, size);
        write_log_.push_back({dst, size});
        return true;
    }

    // Attacker-side helpers: the NW can touch these bytes at any moment.
    void attackerWrite(std::size_t offset, const void* src, std::size_t size) {
        assert(offset + size <= backing_.size());
        std::memcpy(backing_.data() + offset, src, size);
    }
    void attackerFill(std::size_t offset, std::uint8_t value, std::size_t size) {
        assert(offset + size <= backing_.size());
        std::memset(backing_.data() + offset, value, size);
    }

    void setReadHook(ReadHook hook) { read_hook_ = std::move(hook); }
    void setAccessFault(bool fault) { access_fault_ = fault; }

    int readCount() const { return read_count_; }
    const std::vector<std::pair<std::uintptr_t, std::size_t>>& readLog() const {
        return read_log_;
    }
    const std::vector<std::pair<std::uintptr_t, std::size_t>>& writeLog() const {
        return write_log_;
    }

private:
    bool inside(std::uintptr_t addr, std::size_t size) const noexcept {
        std::uintptr_t end = 0;
        if (netsec::MemoryPolicy::add_overflow(addr, size, end)) return false;
        return addr >= base_ && end <= base_ + backing_.size();
    }

    std::uintptr_t base_;
    std::vector<std::uint8_t> backing_;
    bool access_fault_ = false;
    int read_count_ = 0;
    ReadHook read_hook_;
    std::vector<std::pair<std::uintptr_t, std::size_t>> read_log_;
    std::vector<std::pair<std::uintptr_t, std::size_t>> write_log_;
};

netsec::MemoryPolicy makePolicy() {
    // A Secure World carve-out deliberately aliases part of the simulated
    // address window so the disjointness check can be exercised directly.
    return netsec::MemoryPolicy(
        netsec::Range{kWindowBase, kWindowSize},
        {netsec::Range{0x50000000, 0x4000},
         netsec::Range{kWindowBase + 0x800, 0x100}},
        kAlignment, 0x100000);
}

void encodeLe32(std::uint8_t* p, std::uint32_t v) {
    p[0] = static_cast<std::uint8_t>(v);
    p[1] = static_cast<std::uint8_t>(v >> 8);
    p[2] = static_cast<std::uint8_t>(v >> 16);
    p[3] = static_cast<std::uint8_t>(v >> 24);
}

void encodeLe64(std::uint8_t* p, std::uint64_t v) {
    encodeLe32(p, static_cast<std::uint32_t>(v));
    encodeLe32(p + 4, static_cast<std::uint32_t>(v >> 32));
}

void putHeader(SimWorld& world, const netsec::TaRequestHeader& header,
               std::size_t offset = 0) {
    std::uint8_t wire[netsec::TaRequestHeader::kWireSize];
    encodeLe32(wire, header.magic);
    encodeLe32(wire + 4, header.flags);
    encodeLe64(wire + 8, header.payload_offset);
    encodeLe64(wire + 16, header.payload_size);
    encodeLe64(wire + 24, header.response_capacity);
    world.attackerWrite(offset, wire, sizeof(wire));
}

// Deliberately vulnerable two-pass reader used only as a control: it reads the
// same NW bytes twice, so an injected mutation between reads is observable.
bool vulnerableTwoPassRead(netsec::INormalWorldMemory& world,
                           std::uintptr_t addr, std::size_t size,
                           std::vector<std::uint8_t>& first,
                           std::vector<std::uint8_t>& second) {
    first.resize(size);
    second.resize(size);
    return world.read(first.data(), addr, size) &&
           world.read(second.data(), addr, size);
}



void test_alignment(const netsec::MemoryPolicy& policy) {
    EXPECT_ERROR(policy.validateReference(4, 8), netsec::Error::kMisaligned);
    EXPECT_ERROR(policy.validateReference(12, 8), netsec::Error::kMisaligned);
    EXPECT_ERROR(policy.validateAbsolute(kWindowBase + 4, 8),
                 netsec::Error::kMisaligned);
    CHECK(policy.validateReference(8, 4).ok());
    CHECK(policy.validateReference(0, 0).ok());
}

void test_wraparound(const netsec::MemoryPolicy& policy) {
    // base + offset wraps the 64-bit address space.
    const std::uint64_t wrapping_offset =
        std::uint64_t{0} - kWindowBase; // 2^64 - kWindowBase
    EXPECT_ERROR(policy.validateReference(wrapping_offset, 0x10),
                 netsec::Error::kWraparound);

    // offset + size wraps.
    EXPECT_ERROR(policy.validateReference(0x100, std::uint64_t{0} - 1),
                 netsec::Error::kWraparound);

    // absolute base + size wraps.
    EXPECT_ERROR(policy.validateAbsolute(std::uintptr_t{0} - 0x80, 0x100),
                 netsec::Error::kWraparound);
}

void test_window_and_size(const netsec::MemoryPolicy& policy) {
    EXPECT_ERROR(policy.validateReference(0, kWindowSize + 1),
                 netsec::Error::kOutOfWindow);
    EXPECT_ERROR(policy.validateReference(kWindowSize, 1),
                 netsec::Error::kOutOfWindow);

    netsec::MemoryPolicy small(policy.sharedWindow(), policy.secureRanges(),
                               kAlignment, 0x1000);
    EXPECT_ERROR(small.validateReference(0, 0x2000),
                 netsec::Error::kSizeTooLarge);
}

void test_secure_overlap(const netsec::MemoryPolicy& policy) {
    // Secure carve-out lives at window offset 0x800..0x900.
    EXPECT_ERROR(policy.validateReference(0x800, 0x80),
                 netsec::Error::kOverlapsSecure);
    EXPECT_ERROR(policy.validateReference(0x8f8, 0x10),
                 netsec::Error::kOverlapsSecure);

    // Touching but not overlapping (half-open intervals) is allowed.
    CHECK(policy.validateReference(0x900, 0x10).ok());
    CHECK(policy.validateReference(0x700, 0x100).ok());

    // Absolute reference whose range sits outside the window is rejected at
    // the containment check before disjointness is even evaluated.
    EXPECT_ERROR(policy.validateAbsolute(0x50001000, 0x10),
                 netsec::Error::kOutOfWindow);
}


void test_payload_toctou_single_copy(netsec::MemoryPolicy& policy,
                                     SimWorld& world) {
    constexpr std::uint64_t kOffset = 0x100;
    constexpr std::size_t kSize = 16;
    world.attackerFill(kOffset, 'A', kSize);

    // No hook yet: the snapshot performs its single NW read of the pristine
    // bytes and the backing store is never read again by the TA.
    auto snapshot =
        netsec::SecureSnapshot::take(policy, world, kOffset, kSize);
    CHECK(snapshot.ok());
    CHECK(snapshot.value.size() == kSize);
    CHECK(world.readCount() == 1); // exactly one NW read for this object

    // The secure copy holds exactly the pristine bytes.
    for (std::uint8_t byte : snapshot.value.span()) {
        CHECK(byte == 'A');
    }

    // Now arm the attacker: mutate the backing store immediately after the
    // next read. This is the "change data between two reads" TOCTOU window.
    world.setReadHook([](SimWorld& w) {
        w.attackerFill(kOffset, 'B', kSize);
        w.setReadHook(nullptr);
    });

    // Control: a vulnerable two-pass reader observes the mutation between its
    // two reads, proving the hook really does alter NW data mid-sequence.
    std::vector<std::uint8_t> first;
    std::vector<std::uint8_t> second;
    CHECK(vulnerableTwoPassRead(world, kWindowBase + kOffset, kSize,
                                first, second));
    CHECK(first[0] == 'A');
    CHECK(second[0] == 'B');
    CHECK(first != second);

    // The secure snapshot remains byte-identical even though NW memory changed.
    for (std::uint8_t byte : snapshot.value.span()) {
        CHECK(byte == 'A');
    }
}

void test_header_length_toctou(netsec::MemoryPolicy& policy,
                               SimWorld& world) {
    netsec::TaRequestHeader header;
    header.magic = netsec::TaRequestHeader::kMagic;
    header.payload_offset = 0x200;
    header.payload_size = 0x20;
    header.response_capacity = 0x40;
    putHeader(world, header);
    world.attackerFill(0x200, 'X', 0x20);

    // Attack: after the header read, inflate the length fields in NW memory.
    world.setReadHook([](SimWorld& w) {
        netsec::TaRequestHeader evil;
        evil.magic = netsec::TaRequestHeader::kMagic;
        evil.payload_offset = 0x200;
        evil.payload_size = 0x8000;       // attacker-inflated length
        evil.response_capacity = 0x100000;
        putHeader(w, evil);
        w.setReadHook(nullptr);
    });

    auto header_snapshot = netsec::SecureSnapshot::take(
        policy, world, 0, netsec::TaRequestHeader::kWireSize);
    CHECK(header_snapshot.ok());
    auto decoded =
        netsec::TaRequestHeader::decode(header_snapshot.value.span());
    CHECK(decoded.ok());
    CHECK(decoded.value.payload_size == 0x20);
    CHECK(decoded.value.response_capacity == 0x40);

    // The payload length comes solely from the secure copy; the inflated NW
    // header is never consulted again.
    auto payload = netsec::SecureSnapshot::take(
        policy, world, decoded.value.payload_offset,
        decoded.value.payload_size);
    CHECK(payload.ok());
    CHECK(payload.value.size() == 0x20);
    const auto& reads = world.readLog();
    CHECK(reads.size() == 2);
    CHECK(reads.back().second == 0x20); // not the attacker's 0x8000
    for (std::uint8_t byte : payload.value.span()) {
        CHECK(byte == 'X');
    }

    // Control: re-reading the NW header now exposes the tampered length.
    std::uint8_t reread[netsec::TaRequestHeader::kWireSize];
    CHECK(world.read(reread, kWindowBase, sizeof(reread)));
    CHECK(netsec::loadLe64(reread + 16) == 0x8000);
}

void test_fixed_buffer_and_fault(netsec::MemoryPolicy& policy,
                                 SimWorld& world) {
    world.setReadHook(nullptr);
    constexpr std::uint64_t kOffset = 0x300;
    world.attackerFill(kOffset, 'C', 0x40);

    std::uint8_t secure_buf[0x20];
    auto too_small = netsec::SecureSnapshot::takeInto(
        policy, world, kOffset, 0x40, secure_buf, sizeof(secure_buf));
    EXPECT_ERROR(too_small, netsec::Error::kCapacityExceeded);

    auto fit = netsec::SecureSnapshot::takeInto(policy, world, kOffset, 0x20,
                                                secure_buf,
                                                sizeof(secure_buf));
    CHECK(fit.ok());
    CHECK(fit.value.size == 0x20);
    CHECK(fit.value.data[0] == 'C');

    world.setAccessFault(true);
    auto faulted =
        netsec::SecureSnapshot::take(policy, world, kOffset, 0x10);
    EXPECT_ERROR(faulted, netsec::Error::kWorldAccessFault);
    world.setAccessFault(false);
}

void test_output_writer(netsec::MemoryPolicy& policy, SimWorld& world) {
    world.setReadHook(nullptr);
    constexpr std::uint64_t kOutOffset = 0x400;
    constexpr std::size_t kCapacity = 0x20;

    // Binding validates the output window itself against the policy.
    auto writer =
        netsec::OutputWriter::bind(policy, world, kOutOffset, kCapacity);
    CHECK(writer.ok());

    std::vector<std::uint8_t> response(0x18, 7);
    CHECK(writer.value.write(response.data(), response.size()).ok());
    CHECK(writer.value.used() == 0x18);

    // Fail-closed: only 8 bytes remain, so a 16-byte write is refused and
    // nothing is written.
    std::vector<std::uint8_t> big(0x10, 9);
    auto refused = writer.value.write(big.data(), big.size());
    EXPECT_ERROR(refused, netsec::Error::kCapacityExceeded);
    CHECK(writer.value.used() == 0x18);

    // Clamped writeback: an attacker-inflated length never exceeds capacity.
    std::vector<std::uint8_t> padded(0x40, 0xAB);
    const std::size_t written =
        writer.value.writeClamped(padded.data(), padded.size());
    CHECK(written == 0x08);
    CHECK(writer.value.used() == kCapacity);

    // Only the declared 0x20 bytes were ever written to the NW, all inside the
    // bound output window.
    std::size_t total = 0;
    for (const auto& w : world.writeLog()) {
        total += w.second;
        CHECK(w.first >= kWindowBase + kOutOffset);
        CHECK(w.first + w.second <=
              kWindowBase + kOutOffset + kCapacity);
    }
    CHECK(total == kCapacity);
}

void test_output_window_validation(netsec::MemoryPolicy& policy,
                                   SimWorld& world) {
    // Output window overlapping Secure World must be rejected at bind time.
    auto overlap =
        netsec::OutputWriter::bind(policy, world, 0x800, 0x40);
    EXPECT_ERROR(overlap, netsec::Error::kOverlapsSecure);

    // Wrapping output window.
    const std::uint64_t wrapping_offset = std::uint64_t{0} - kWindowBase;
    auto wrap =
        netsec::OutputWriter::bind(policy, world, wrapping_offset, 0x10);
    EXPECT_ERROR(wrap, netsec::Error::kWraparound);
}

void test_end_to_end(netsec::MemoryPolicy& policy, SimWorld& world) {
    world.setReadHook(nullptr);
    netsec::TaRequestHeader header;
    header.magic = netsec::TaRequestHeader::kMagic;
    header.flags = 0;
    header.payload_offset = 0x500;
    header.payload_size = 0x40;
    header.response_capacity = 0x20;
    putHeader(world, header);
    world.attackerFill(0x500, 'Z', 0x40);

    // 1. Snapshot and decode the header from the secure copy.
    auto header_snapshot = netsec::SecureSnapshot::take(
        policy, world, 0, netsec::TaRequestHeader::kWireSize);
    CHECK(header_snapshot.ok());
    auto decoded =
        netsec::TaRequestHeader::decode(header_snapshot.value.span());
    CHECK(decoded.ok());

    // 2. Snapshot the payload using only copy-derived lengths.
    auto payload = netsec::SecureSnapshot::take(
        policy, world, decoded.value.payload_offset,
        decoded.value.payload_size);
    CHECK(payload.ok());
    CHECK(payload.value.size() == 0x40);

    // 3. Bind the output window to the declared capacity and respond.
    auto writer = netsec::OutputWriter::bind(
        policy, world, 0x600, decoded.value.response_capacity);
    CHECK(writer.ok());
    std::uint8_t answer[0x30];
    std::memset(answer, 0x5A, sizeof(answer)); // bigger than capacity
    const std::size_t n =
        writer.value.writeClamped(answer, sizeof(answer));
    CHECK(n == 0x20);
    CHECK(writer.value.used() == 0x20);
}

} // namespace

int main() {
    netsec::MemoryPolicy policy = makePolicy();

    test_alignment(policy);
    test_wraparound(policy);
    test_window_and_size(policy);
    test_secure_overlap(policy);

    SimWorld payload_world(kWindowBase, kWindowSize);
    test_payload_toctou_single_copy(policy, payload_world);

    SimWorld header_world(kWindowBase, kWindowSize);
    test_header_length_toctou(policy, header_world);

    SimWorld fixed_world(kWindowBase, kWindowSize);
    test_fixed_buffer_and_fault(policy, fixed_world);

    SimWorld output_world(kWindowBase, kWindowSize);
    test_output_writer(policy, output_world);
    test_output_window_validation(policy, output_world);

    SimWorld e2e_world(kWindowBase, kWindowSize);
    test_end_to_end(policy, e2e_world);

    std::cout << "checks=" << g_checks << " failures=" << g_failures << "\n";
    return g_failures == 0 ? 0 : 1;
}
