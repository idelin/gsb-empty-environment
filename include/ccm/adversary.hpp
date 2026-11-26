// Deterministic hostile-host model.  Real SGX/SEV-SNP attacks are racy and
// therefore hard to reproduce; the simulator instead drives the exact same
// malicious behaviour through bus read hooks in a reproducible, bounded way.
// Every interleaving is identified by a seed and a read-event index, so a
// failing case can be replayed byte-for-byte.
#ifndef CCM_ADVERSARY_HPP
#define CCM_ADVERSARY_HPP

#include <cstdint>
#include <vector>

#include "ccm/shared_bus.hpp"
#include "ccm/types.hpp"

namespace ccm {

// xorshift64* PRNG: small, dependency-free and identical on every platform.
class DeterministicRng {
public:
    explicit DeterministicRng(std::uint64_t seed)
        : state_(seed == 0 ? default_seed() : seed) {}

    std::uint64_t next() {
        std::uint64_t x = state_;
        x ^= x >> 12;
        x ^= x << 25;
        x ^= x >> 27;
        state_ = x;
        return x * 0x2545F4914F6CDD1DULL;
    }

    std::uint64_t bounded(std::uint64_t limit) {
        if (limit == 0) {
            return 0;
        }
        return next() % limit;
    }

    static std::uint64_t default_seed() { return 0x9E3779B97F4A7C15ULL; }

private:
    std::uint64_t state_;
};

// An attack is a single mutation applied on a chosen read-event index.
struct Mutation {
    len_t event_index = 0;          // fire before this bus read
    len_t shared_offset = 0;        // byte offset inside the shared pool
    std::uint8_t value = 0;
};

// Generates a bounded stream of mutations for one scenario run.
class Adversary {
public:
    Adversary(SharedBus& bus, std::uint64_t seed,
              std::vector<len_t> mutable_offsets)
        : bus_(bus),
          rng_(seed),
          mutable_offsets_(std::move(mutable_offsets)),
          max_events_(64),
          max_mutations_(4) {}

    void set_bounds(len_t max_events, len_t max_mutations) {
        max_events_ = max_events;
        max_mutations_ = max_mutations;
    }

    len_t read_events_seen() const { return reads_; }

    // Called by the bus before each read; applies at most max_mutations_
    // mutations and never beyond the bounded event horizon.
    void on_read(addr_t address, len_t size) {
        (void)address;
        (void)size;
        if (reads_ >= max_events_ || mutations_applied_ >= max_mutations_) {
            ++reads_;
            return;
        }
        if (mutable_offsets_.empty()) {
            ++reads_;
            return;
        }
        // Mutate on roughly half of the read gaps, driven by the PRNG.
        if ((rng_.next() & 1U) == 1U) {
            len_t offset_index =
                static_cast<len_t>(rng_.bounded(
                    static_cast<std::uint64_t>(mutable_offsets_.size())));
            len_t offset = mutable_offsets_[offset_index];
            std::uint8_t value =
                static_cast<std::uint8_t>(rng_.next() & 0xFFU);
            std::uint8_t bytes[1] = {value};
            bus_.host_poke(bus_.base() + offset, bytes, 1);
            ++mutations_applied_;
            history_.push_back(
                Mutation{reads_, offset, value});
        }
        ++reads_;
    }

    const std::vector<Mutation>& history() const { return history_; }

private:
    SharedBus& bus_;
    DeterministicRng rng_;
    std::vector<len_t> mutable_offsets_;
    len_t max_events_;
    len_t max_mutations_;
    len_t reads_ = 0;
    len_t mutations_applied_ = 0;
    std::vector<Mutation> history_;
};

}  // namespace ccm

#endif  // CCM_ADVERSARY_HPP
