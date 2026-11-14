#include "netsec.hpp"

#include <algorithm>
#include <atomic>
#include <cmath>
#include <limits>
#include <mutex>
#include <stdexcept>
#include <utility>

#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__)
#include <cstdlib>
#elif defined(__linux__)
#include <cerrno>
#include <sys/random.h>
#include <sys/types.h>
#elif defined(_WIN32)
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <bcrypt.h>
#endif

namespace netsec {
namespace {

void compiler_barrier() noexcept {
    std::atomic_signal_fence(std::memory_order_seq_cst);
}

bool all_zero(const std::uint8_t* data, std::size_t size) noexcept {
    std::uint8_t value = 0;
    for (std::size_t i = 0; i < size; ++i) {
        value |= data[i];
    }
    return value == 0;
}

std::uint64_t load_u64(const std::uint8_t* data) noexcept {
    std::uint64_t value = 0;
    for (unsigned i = 0; i < 8; ++i) {
        value |= static_cast<std::uint64_t>(data[i]) << (8U * i);
    }
    return value;
}

// ---------------------------------------------------------------------------
// AES field and S-box construction
// ---------------------------------------------------------------------------

constexpr std::uint8_t xtime_byte(std::uint8_t value) noexcept {
    return static_cast<std::uint8_t>(static_cast<unsigned>(value) << 1U) ^
           static_cast<std::uint8_t>(0x1BU & static_cast<std::uint8_t>(-(value >> 7U)));
}

constexpr std::uint8_t gf_mul(std::uint8_t left, std::uint8_t right) noexcept {
    std::uint8_t product = 0;
    for (unsigned bit = 0; bit < 8; ++bit) {
        const std::uint8_t mask = static_cast<std::uint8_t>(-(right & 1U));
        product ^= static_cast<std::uint8_t>(left & mask);
        left = xtime_byte(left);
        right = static_cast<std::uint8_t>(right >> 1U);
    }
    return product;
}

constexpr std::uint8_t gf_inverse(std::uint8_t value) noexcept {
    std::uint8_t result = 1;
    std::uint8_t base = value;
    unsigned exponent = 254;
    while (exponent != 0) {
        if ((exponent & 1U) != 0) {
            result = gf_mul(result, base);
        }
        base = gf_mul(base, base);
        exponent >>= 1U;
    }
    return result;
}

constexpr std::uint8_t rotate_left8(std::uint8_t value, unsigned amount) noexcept {
    return static_cast<std::uint8_t>(
        static_cast<unsigned>(value) << amount |
        static_cast<unsigned>(value) >> (8U - amount));
}

constexpr std::array<std::uint8_t, 256> make_sbox() noexcept {
    std::array<std::uint8_t, 256> table{};
    for (std::size_t i = 0; i < table.size(); ++i) {
        const std::uint8_t inverse = gf_inverse(static_cast<std::uint8_t>(i));
        table[i] = static_cast<std::uint8_t>(
            inverse ^
            rotate_left8(inverse, 1) ^
            rotate_left8(inverse, 2) ^
            rotate_left8(inverse, 3) ^
            rotate_left8(inverse, 4) ^
            0x63U);
    }
    return table;
}

constexpr std::array<std::uint8_t, 256> make_inverse_sbox() noexcept {
    std::array<std::uint8_t, 256> table{};
    for (std::size_t i = 0; i < table.size(); ++i) {
        const std::uint8_t value = static_cast<std::uint8_t>(i);
        const std::uint8_t inverse_affine = static_cast<std::uint8_t>(
            rotate_left8(value, 1) ^
            rotate_left8(value, 3) ^
            rotate_left8(value, 6) ^
            0x05U);
        table[i] = gf_inverse(inverse_affine);
    }
    return table;
}

constexpr auto kSbox = make_sbox();
constexpr auto kInverseSbox = make_inverse_sbox();
constexpr std::array<std::uint8_t, 11> kRcon = {
    0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36};

// ---------------------------------------------------------------------------
// Masked byte primitives
// ---------------------------------------------------------------------------

struct MaskedByte {
    std::uint8_t share0 = 0;
    std::uint8_t share1 = 0;
};

struct MaskedBit {
    std::uint8_t share0 = 0;
    std::uint8_t share1 = 0;
};

struct MaskedState {
    std::array<MaskedByte, 16> bytes{};
};

struct OperationStats {
    std::uint64_t randomized_orders = 0;
    std::uint64_t mask_refreshes = 0;
};

void wipe_masked_byte(MaskedByte& value) noexcept {
    value.share0 = 0;
    value.share1 = 0;
    compiler_barrier();
}

void wipe_masked_state(MaskedState& state) noexcept {
    for (auto& value : state.bytes) {
        wipe_masked_byte(value);
    }
    compiler_barrier();
}

void wipe_schedule(std::vector<MaskedByte>& schedule) noexcept {
    for (auto& value : schedule) {
        wipe_masked_byte(value);
    }
    schedule.clear();
}

MaskedBit not_bit(MaskedBit value) noexcept {
    value.share0 ^= 1U;
    return value;
}

// First-order ISW bit multiplication. A fresh random bit is supplied for every
// product so no two nonlinear gates reuse the same randomness.
MaskedBit and_bit(MaskedBit left, MaskedBit right, std::uint8_t random_bit) noexcept {
    const std::uint8_t a0b0 = static_cast<std::uint8_t>(left.share0 & right.share0);
    const std::uint8_t a1b1 = static_cast<std::uint8_t>(left.share1 & right.share1);
    const std::uint8_t cross = static_cast<std::uint8_t>(
        static_cast<std::uint8_t>(left.share0 & right.share1) ^
        static_cast<std::uint8_t>(left.share1 & right.share0));
    return {
        static_cast<std::uint8_t>(a0b0 ^ random_bit),
        static_cast<std::uint8_t>(a1b1 ^ cross ^ random_bit)};
}

class RandomPool {
public:
    explicit RandomPool(RandomSource& random) noexcept : random_(random) {}

    ~RandomPool() {
        secure_erase(pool_.data(), pool_.size());
    }

    bool ready() const noexcept {
        return ready_;
    }

    std::uint64_t consumed() const noexcept {
        return consumed_;
    }

    std::uint8_t next() noexcept {
        if (position_ == pool_.size() && !refill()) {
            return 0;
        }
        ++consumed_;
        return pool_[position_++];
    }

    std::uint8_t next_bounded(std::uint8_t upper_exclusive) noexcept {
        if (upper_exclusive == 0) {
            return 0;
        }
        const unsigned limit = 256U - (256U % upper_exclusive);
        for (;;) {
            const unsigned value = next();
            if (value < limit) {
                return static_cast<std::uint8_t>(value % upper_exclusive);
            }
        }
    }

    void permutation(std::span<std::uint8_t> values) noexcept {
        for (std::size_t i = 0; i < values.size(); ++i) {
            values[i] = static_cast<std::uint8_t>(i);
        }
        for (std::size_t i = values.size(); i > 1; --i) {
            const std::size_t selected = next_bounded(static_cast<std::uint8_t>(i));
            std::swap(values[i - 1], values[selected]);
        }
    }

private:
    bool refill() noexcept {
        if (!ready_) {
            return false;
        }
        secure_erase(pool_.data(), pool_.size());
        if (!random_.fill(pool_)) {
            ready_ = false;
            return false;
        }
        position_ = 0;
        return true;
    }

    RandomSource& random_;
    std::array<std::uint8_t, 4096> pool_{};
    std::size_t position_ = 4096;
    std::uint64_t consumed_ = 0;
    bool ready_ = true;
};

MaskedByte split_byte(std::uint8_t value, RandomPool& random) noexcept {
    const std::uint8_t mask = random.next();
    return {static_cast<std::uint8_t>(value ^ mask), mask};
}

std::uint8_t unmask(MaskedByte value) noexcept {
    return static_cast<std::uint8_t>(value.share0 ^ value.share1);
}

MaskedByte xor_byte(MaskedByte left, MaskedByte right) noexcept {
    return {
        static_cast<std::uint8_t>(left.share0 ^ right.share0),
        static_cast<std::uint8_t>(left.share1 ^ right.share1)};
}

MaskedByte xor_constant(MaskedByte value, std::uint8_t constant) noexcept {
    value.share0 ^= constant;
    return value;
}

void refresh_byte(MaskedByte& value, RandomPool& random, OperationStats* stats) noexcept {
    const std::uint8_t mask = random.next();
    value.share0 ^= mask;
    value.share1 ^= mask;
    if (stats != nullptr) {
        ++stats->mask_refreshes;
    }
}

MaskedByte multiply_byte(MaskedByte value, std::uint8_t factor) noexcept {
    return {gf_mul(value.share0, factor), gf_mul(value.share1, factor)};
}

MaskedByte masked_equality_mask(
    MaskedByte value,
    std::uint8_t candidate,
    RandomPool& random) noexcept {
    MaskedBit not_equal{0, 0};
    for (unsigned bit = 0; bit < 8; ++bit) {
        const std::uint8_t mask_bit = static_cast<std::uint8_t>((candidate >> bit) & 1U);
        const MaskedBit difference{
            static_cast<std::uint8_t>(((value.share0 >> bit) & 1U) ^ mask_bit),
            static_cast<std::uint8_t>((value.share1 >> bit) & 1U)};
        const std::uint8_t random_bit = static_cast<std::uint8_t>(random.next() & 1U);
        const MaskedBit both_zero = and_bit(not_bit(difference), not_bit(not_equal), random_bit);
        not_equal = not_bit(both_zero);
    }
    const MaskedBit equal = not_bit(not_equal);
    return {
        static_cast<std::uint8_t>(0U - equal.share0),
        static_cast<std::uint8_t>(0U - equal.share1)};
}

template <std::size_t TableSize>
MaskedByte masked_table_select(
    MaskedByte value,
    const std::array<std::uint8_t, TableSize>& table,
    RandomPool& random) noexcept {
    const std::uint8_t mask = random.next();
    MaskedByte output{mask, mask};
    for (std::size_t candidate = 0; candidate < table.size(); ++candidate) {
        const MaskedByte selected =
            masked_equality_mask(value, static_cast<std::uint8_t>(candidate), random);
        output.share0 ^= static_cast<std::uint8_t>(table[candidate] & selected.share0);
        output.share1 ^= static_cast<std::uint8_t>(table[candidate] & selected.share1);
    }
    return output;
}

MaskedByte masked_sbox(MaskedByte value, RandomPool& random) noexcept {
    return masked_table_select(value, kSbox, random);
}

MaskedByte masked_inverse_sbox(MaskedByte value, RandomPool& random) noexcept {
    return masked_table_select(value, kInverseSbox, random);
}

void shift_rows(MaskedState& state) noexcept {
    MaskedState shifted{};
    for (std::size_t column = 0; column < 4; ++column) {
        for (std::size_t row = 0; row < 4; ++row) {
            shifted.bytes[4 * column + row] =
                state.bytes[4 * ((column + row) % 4) + row];
        }
    }
    state = shifted;
}

void inverse_shift_rows(MaskedState& state) noexcept {
    MaskedState shifted{};
    for (std::size_t column = 0; column < 4; ++column) {
        for (std::size_t row = 0; row < 4; ++row) {
            shifted.bytes[4 * column + row] =
                state.bytes[4 * ((column + 4 - row) % 4) + row];
        }
    }
    state = shifted;
}

void sub_bytes(
    MaskedState& state,
    RandomPool& random,
    bool inverse,
    OperationStats* stats) noexcept {
    std::array<std::uint8_t, 16> order{};
    random.permutation(order);
    if (stats != nullptr) {
        ++stats->randomized_orders;
    }
    for (const std::uint8_t index : order) {
        state.bytes[index] = inverse
            ? masked_inverse_sbox(state.bytes[index], random)
            : masked_sbox(state.bytes[index], random);
        compiler_barrier();
    }
}

void mix_columns(
    MaskedState& state,
    RandomPool& random,
    bool inverse,
    OperationStats* stats) noexcept {
    std::array<std::uint8_t, 4> order{};
    random.permutation(order);
    if (stats != nullptr) {
        ++stats->randomized_orders;
    }

    for (const std::uint8_t column : order) {
        const MaskedByte a0 = state.bytes[4 * column + 0];
        const MaskedByte a1 = state.bytes[4 * column + 1];
        const MaskedByte a2 = state.bytes[4 * column + 2];
        const MaskedByte a3 = state.bytes[4 * column + 3];

        if (!inverse) {
            const MaskedByte x0 = multiply_byte(a0, 2);
            const MaskedByte x1 = multiply_byte(a1, 2);
            const MaskedByte x2 = multiply_byte(a2, 2);
            const MaskedByte x3 = multiply_byte(a3, 2);
            state.bytes[4 * column + 0] =
                xor_byte(xor_byte(x0, xor_byte(x1, a1)), xor_byte(a2, a3));
            state.bytes[4 * column + 1] =
                xor_byte(xor_byte(a0, x1), xor_byte(xor_byte(x2, a2), a3));
            state.bytes[4 * column + 2] =
                xor_byte(xor_byte(a0, a1), xor_byte(x2, xor_byte(x3, a3)));
            state.bytes[4 * column + 3] =
                xor_byte(xor_byte(xor_byte(x0, a0), a1), xor_byte(a2, x3));
        } else {
            const MaskedByte a0_9 = multiply_byte(a0, 9);
            const MaskedByte a0_11 = multiply_byte(a0, 11);
            const MaskedByte a0_13 = multiply_byte(a0, 13);
            const MaskedByte a0_14 = multiply_byte(a0, 14);
            const MaskedByte a1_9 = multiply_byte(a1, 9);
            const MaskedByte a1_11 = multiply_byte(a1, 11);
            const MaskedByte a1_13 = multiply_byte(a1, 13);
            const MaskedByte a1_14 = multiply_byte(a1, 14);
            const MaskedByte a2_9 = multiply_byte(a2, 9);
            const MaskedByte a2_11 = multiply_byte(a2, 11);
            const MaskedByte a2_13 = multiply_byte(a2, 13);
            const MaskedByte a2_14 = multiply_byte(a2, 14);
            const MaskedByte a3_9 = multiply_byte(a3, 9);
            const MaskedByte a3_11 = multiply_byte(a3, 11);
            const MaskedByte a3_13 = multiply_byte(a3, 13);
            const MaskedByte a3_14 = multiply_byte(a3, 14);

            state.bytes[4 * column + 0] =
                xor_byte(xor_byte(a0_14, a1_11), xor_byte(a2_13, a3_9));
            state.bytes[4 * column + 1] =
                xor_byte(xor_byte(a0_9, a1_14), xor_byte(a2_11, a3_13));
            state.bytes[4 * column + 2] =
                xor_byte(xor_byte(a0_13, a1_9), xor_byte(a2_14, a3_11));
            state.bytes[4 * column + 3] =
                xor_byte(xor_byte(a0_11, a1_13), xor_byte(a2_9, a3_14));
        }
        compiler_barrier();
    }
}

void add_round_key(
    MaskedState& state,
    const std::vector<MaskedByte>& schedule,
    std::size_t round) noexcept {
    const std::size_t offset = round * 16;
    for (std::size_t i = 0; i < 16; ++i) {
        state.bytes[i] = xor_byte(state.bytes[i], schedule[offset + i]);
    }
}

void refresh_state(MaskedState& state, RandomPool& random, OperationStats* stats) noexcept {
    for (auto& value : state.bytes) {
        refresh_byte(value, random, stats);
    }
    compiler_barrier();
}

std::array<MaskedByte, 4> sub_word(
    std::array<MaskedByte, 4> word,
    RandomPool& random,
    bool inverse) noexcept {
    std::array<std::uint8_t, 4> order{};
    random.permutation(order);
    for (const std::uint8_t index : order) {
        word[index] = inverse
            ? masked_inverse_sbox(word[index], random)
            : masked_sbox(word[index], random);
        compiler_barrier();
    }
    return word;
}

bool expand_key(
    std::span<const std::uint8_t> key,
    std::vector<MaskedByte>& schedule,
    RandomPool& random,
    std::size_t& key_bits) noexcept {
    if (key.size() != kAes128KeySize && key.size() != kAes256KeySize) {
        return false;
    }

    const std::size_t key_words = key.size() / 4;
    const std::size_t rounds = key_words + 6;
    const std::size_t total_words = 4 * (rounds + 1);
    schedule.clear();
    schedule.resize(total_words * 4);

    for (std::size_t i = 0; i < key_words * 4; ++i) {
        schedule[i] = split_byte(key[i], random);
    }

    for (std::size_t word_index = key_words; word_index < total_words; ++word_index) {
        std::array<MaskedByte, 4> temporary{};
        for (std::size_t i = 0; i < 4; ++i) {
            temporary[i] = schedule[(word_index - 1) * 4 + i];
        }

        if (word_index % key_words == 0) {
            std::rotate(temporary.begin(), temporary.begin() + 1, temporary.end());
            temporary = sub_word(temporary, random, false);
            temporary[0] = xor_constant(temporary[0], kRcon[word_index / key_words]);
        } else if (key_words > 6 && word_index % key_words == 4) {
            temporary = sub_word(temporary, random, false);
        }

        for (std::size_t i = 0; i < 4; ++i) {
            MaskedByte value =
                xor_byte(schedule[(word_index - key_words) * 4 + i], temporary[i]);
            refresh_byte(value, random, nullptr);
            schedule[word_index * 4 + i] = value;
        }
    }

    key_bits = key.size() * 8;
    return random.ready();
}

void encrypt_state(
    MaskedState& state,
    const std::vector<MaskedByte>& schedule,
    std::size_t rounds,
    RandomPool& random,
    OperationStats& stats) noexcept {
    add_round_key(state, schedule, 0);
    for (std::size_t round = 1; round < rounds; ++round) {
        refresh_state(state, random, &stats);
        sub_bytes(state, random, false, &stats);
        shift_rows(state);
        mix_columns(state, random, false, &stats);
        add_round_key(state, schedule, round);
    }
    refresh_state(state, random, &stats);
    sub_bytes(state, random, false, &stats);
    shift_rows(state);
    add_round_key(state, schedule, rounds);
}

void decrypt_state(
    MaskedState& state,
    const std::vector<MaskedByte>& schedule,
    std::size_t rounds,
    RandomPool& random,
    OperationStats& stats) noexcept {
    add_round_key(state, schedule, rounds);
    for (std::size_t round = rounds; round-- > 1;) {
        inverse_shift_rows(state);
        sub_bytes(state, random, true, &stats);
        add_round_key(state, schedule, round);
        refresh_state(state, random, &stats);
        mix_columns(state, random, true, &stats);
    }
    inverse_shift_rows(state);
    sub_bytes(state, random, true, &stats);
    add_round_key(state, schedule, 0);
}

void split_state(
    std::span<const std::uint8_t, kAesBlockSize> input,
    MaskedState& state,
    RandomPool& random) noexcept {
    for (std::size_t i = 0; i < kAesBlockSize; ++i) {
        state.bytes[i] = split_byte(input[i], random);
    }
}

void join_state(
    const MaskedState& state,
    std::span<std::uint8_t, kAesBlockSize> output) noexcept {
    for (std::size_t i = 0; i < kAesBlockSize; ++i) {
        output[i] = unmask(state.bytes[i]);
    }
    compiler_barrier();
}

std::uint64_t random_key_id(RandomPool& random) noexcept {
    std::uint64_t value = 0;
    for (unsigned i = 0; i < 8; ++i) {
        value |= static_cast<std::uint64_t>(random.next()) << (8U * i);
    }
    return value == 0 ? 1 : value;
}

bool valid_trace_values(
    double rms,
    double peak,
    std::span<const double> spectrum) noexcept {
    if (!std::isfinite(rms) || !std::isfinite(peak) || rms < 0.0 || peak < 0.0) {
        return false;
    }
    for (const double value : spectrum) {
        if (!std::isfinite(value) || value < 0.0) {
            return false;
        }
    }
    return true;
}

} // namespace

std::string_view to_string(Status status) noexcept {
    switch (status) {
    case Status::ok:
        return "ok";
    case Status::invalid_argument:
        return "invalid_argument";
    case Status::invalid_size:
        return "invalid_size";
    case Status::unauthorized:
        return "unauthorized";
    case Status::monitor_blocked:
        return "monitor_blocked";
    case Status::random_failure:
        return "random_failure";
    case Status::not_ready:
        return "not_ready";
    }
    return "unknown";
}

std::string_view to_string(EmStatus status) noexcept {
    switch (status) {
    case EmStatus::calibrating:
        return "calibrating";
    case EmStatus::nominal:
        return "nominal";
    case EmStatus::intensity_anomaly:
        return "intensity_anomaly";
    case EmStatus::spectral_anomaly:
        return "spectral_anomaly";
    case EmStatus::sensor_fault:
        return "sensor_fault";
    case EmStatus::locked_out:
        return "locked_out";
    }
    return "unknown";
}

void secure_erase(void* data, std::size_t size) noexcept {
    if (data == nullptr) {
        return;
    }
    volatile std::uint8_t* bytes = static_cast<volatile std::uint8_t*>(data);
    for (std::size_t i = 0; i < size; ++i) {
        bytes[i] = 0;
    }
    compiler_barrier();
}

void secure_erase(std::span<std::uint8_t> data) noexcept {
    secure_erase(data.data(), data.size());
}

bool SystemRandomSource::fill(std::span<std::uint8_t> output) noexcept {
    if (output.empty()) {
        return true;
    }
#if defined(__APPLE__) || defined(__FreeBSD__) || defined(__OpenBSD__) || defined(__NetBSD__)
    arc4random_buf(output.data(), output.size());
    return true;
#elif defined(__linux__)
    std::size_t offset = 0;
    while (offset < output.size()) {
        const ssize_t result =
            ::getrandom(output.data() + offset, output.size() - offset, 0);
        if (result < 0) {
            if (errno == EINTR) {
                continue;
            }
            return false;
        }
        offset += static_cast<std::size_t>(result);
    }
    return true;
#elif defined(_WIN32)
    return BCryptGenRandom(
               nullptr,
               output.data(),
               static_cast<ULONG>(output.size()),
               BCRYPT_USE_SYSTEM_PREFERRED_RNG) == 0;
#else
    (void)output;
    return false;
#endif
}

DeterministicRandomSource::DeterministicRandomSource(std::uint64_t seed) noexcept
    : state_(seed) {}

void DeterministicRandomSource::reseed(std::uint64_t seed) noexcept {
    state_ = seed;
}

bool DeterministicRandomSource::fill(std::span<std::uint8_t> output) noexcept {
    for (std::size_t i = 0; i < output.size(); ++i) {
        state_ += 0x9E3779B97F4A7C15ULL;
        std::uint64_t value = state_;
        value = (value ^ (value >> 30U)) * 0xBF58476D1CE4E5B9ULL;
        value = (value ^ (value >> 27U)) * 0x94D049BB133111EBULL;
        value ^= value >> 31U;
        output[i] = static_cast<std::uint8_t>(value >> 56U);
    }
    return true;
}

bool ProtectionLease::valid() const noexcept {
    return zone_id_ != 0 &&
           expires_ > std::chrono::steady_clock::now() &&
           !all_zero(proof_.data(), proof_.size());
}

std::chrono::steady_clock::time_point ProtectionLease::expires_at() const noexcept {
    return expires_;
}

struct PhysicalProtectionArea::Impl {
    mutable std::mutex mutex;
    std::string region;
    std::uint64_t id = 0;
    std::array<std::uint8_t, 32> root{};
    std::array<std::uint8_t, 16> active_proof{};
    std::chrono::steady_clock::time_point active_expiry{};
    bool active = false;

    ~Impl() {
        secure_erase(root.data(), root.size());
        secure_erase(active_proof.data(), active_proof.size());
    }
};

PhysicalProtectionArea::PhysicalProtectionArea(std::string region_id, RandomSource& random)
    : impl_(std::make_shared<Impl>()) {
    impl_->region = std::move(region_id);
    if (!random.fill(impl_->root)) {
        throw std::runtime_error("failed to obtain physical-area root capability");
    }
    std::array<std::uint8_t, 8> id_bytes{};
    if (!random.fill(id_bytes)) {
        throw std::runtime_error("failed to obtain physical-area identifier");
    }
    impl_->id = load_u64(id_bytes.data());
    if (impl_->id == 0) {
        impl_->id = 1;
    }
    impl_->active = true;
}

PhysicalProtectionArea::~PhysicalProtectionArea() = default;

std::uint64_t PhysicalProtectionArea::id() const noexcept {
    return impl_ ? impl_->id : 0;
}

std::string_view PhysicalProtectionArea::region_id() const noexcept {
    if (!impl_) {
        return {};
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->region;
}

bool PhysicalProtectionArea::active() const noexcept {
    if (!impl_) {
        return false;
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->active;
}

ProtectionLease PhysicalProtectionArea::lease(std::chrono::milliseconds ttl) noexcept {
    if (!impl_ || ttl <= std::chrono::milliseconds::zero()) {
        return {};
    }
    std::scoped_lock lock(impl_->mutex);
    if (!impl_->active) {
        return {};
    }

    ProtectionLease result;
    result.zone_id_ = impl_->id;
    result.expires_ = std::chrono::steady_clock::now() + ttl;
    SystemRandomSource system_random;
    if (!system_random.fill(result.proof_)) {
        return {};
    }
    if (all_zero(result.proof_.data(), result.proof_.size())) {
        result.proof_[0] = 1;
    }
    for (std::size_t i = 0; i < result.proof_.size(); ++i) {
        impl_->active_proof[i] =
            static_cast<std::uint8_t>(result.proof_[i] ^ impl_->root[i]);
    }
    impl_->active_expiry = result.expires_;
    return result;
}

bool PhysicalProtectionArea::validate(const ProtectionLease& lease_value) const noexcept {
    if (!impl_ || lease_value.zone_id_ != impl_->id || !lease_value.valid()) {
        return false;
    }
    std::scoped_lock lock(impl_->mutex);
    if (!impl_->active || lease_value.expires_ > impl_->active_expiry) {
        return false;
    }
    std::uint8_t difference = 0;
    for (std::size_t i = 0; i < lease_value.proof_.size(); ++i) {
        const std::uint8_t expected =
            static_cast<std::uint8_t>(impl_->active_proof[i] ^ impl_->root[i]);
        difference |= static_cast<std::uint8_t>(lease_value.proof_[i] ^ expected);
    }
    return difference == 0;
}

void PhysicalProtectionArea::revoke() noexcept {
    if (!impl_) {
        return;
    }
    std::scoped_lock lock(impl_->mutex);
    impl_->active = false;
    secure_erase(impl_->active_proof.data(), impl_->active_proof.size());
    secure_erase(impl_->root.data(), impl_->root.size());
}

struct EmMonitor::Impl {
    explicit Impl(EmMonitorConfig value) : config(value) {}

    void reset_locked() noexcept {
        locked = false;
        samples = 0;
        mean_rms = 0.0;
        m2_rms = 0.0;
        mean_peak = 0.0;
        m2_peak = 0.0;
        mean_spectrum.clear();
        m2_spectrum.clear();
        status = EmStatus::calibrating;
        last = {};
        last.status = EmStatus::calibrating;
    }

    void trip(
        EmStatus reason,
        double rms,
        double peak,
        double intensity_z,
        double spectral_z) noexcept {
        status = reason;
        if (config.lock_on_anomaly) {
            locked = true;
        }
        last.status = reason;
        last.rms = rms;
        last.peak = peak;
        last.intensity_z = intensity_z;
        last.spectral_z = spectral_z;
        last.baseline_samples = samples;
        last.key_operations_allowed = false;
    }

    EmObservation observe(
        double rms,
        double peak,
        std::span<const double> spectrum) {
        std::scoped_lock lock(mutex);
        ++sequence;

        if (locked) {
            last.status = EmStatus::locked_out;
            last.sequence = sequence;
            last.key_operations_allowed = false;
            return last;
        }

        if (!valid_trace_values(rms, peak, spectrum)) {
            trip(EmStatus::sensor_fault, rms, peak, 0.0, 0.0);
            last.sequence = sequence;
            return last;
        }

        if (rms > config.rms_limit || peak > config.peak_limit) {
            trip(
                EmStatus::intensity_anomaly,
                rms,
                peak,
                std::numeric_limits<double>::infinity(),
                0.0);
            last.sequence = sequence;
            return last;
        }

        if (samples < config.warmup_samples) {
            if (!mean_spectrum.empty() && spectrum.size() != mean_spectrum.size()) {
                trip(EmStatus::sensor_fault, rms, peak, 0.0, 0.0);
                last.sequence = sequence;
                return last;
            }
            if (mean_spectrum.empty() && !spectrum.empty()) {
                mean_spectrum.assign(spectrum.size(), 0.0);
                m2_spectrum.assign(spectrum.size(), 0.0);
            }

            ++samples;
            const double rms_delta = rms - mean_rms;
            mean_rms += rms_delta / static_cast<double>(samples);
            m2_rms += rms_delta * (rms - mean_rms);

            const double peak_delta = peak - mean_peak;
            mean_peak += peak_delta / static_cast<double>(samples);
            m2_peak += peak_delta * (peak - mean_peak);

            for (std::size_t i = 0; i < spectrum.size(); ++i) {
                const double delta = spectrum[i] - mean_spectrum[i];
                mean_spectrum[i] += delta / static_cast<double>(samples);
                m2_spectrum[i] += delta * (spectrum[i] - mean_spectrum[i]);
            }

            status = samples >= config.warmup_samples
                ? EmStatus::nominal
                : EmStatus::calibrating;
            last.status = status;
            last.sequence = sequence;
            last.baseline_samples = samples;
            last.rms = rms;
            last.peak = peak;
            last.intensity_z = 0.0;
            last.spectral_z = 0.0;
            last.key_operations_allowed = status == EmStatus::nominal;
            return last;
        }

        if (!mean_spectrum.empty() && spectrum.size() != mean_spectrum.size()) {
            trip(EmStatus::sensor_fault, rms, peak, 0.0, 0.0);
            last.sequence = sequence;
            return last;
        }

        const double rms_variance =
            samples > 1 ? m2_rms / static_cast<double>(samples - 1) : 0.0;
        const double rms_z =
            (rms - mean_rms) / std::sqrt(std::max(rms_variance, config.variance_floor));
        const double peak_variance =
            samples > 1 ? m2_peak / static_cast<double>(samples - 1) : 0.0;
        const double peak_z =
            (peak - mean_peak) / std::sqrt(std::max(peak_variance, config.variance_floor));
        const double intensity_z = std::max(rms_z, peak_z);

        double spectral_z = 0.0;
        for (std::size_t i = 0; i < spectrum.size(); ++i) {
            const double variance =
                samples > 1 ? m2_spectrum[i] / static_cast<double>(samples - 1) : 0.0;
            const double z =
                (spectrum[i] - mean_spectrum[i]) /
                std::sqrt(std::max(variance, config.variance_floor * 1.0e-3));
            if (std::abs(z) > std::abs(spectral_z)) {
                spectral_z = z;
            }
        }

        last.sequence = sequence;
        last.baseline_samples = samples;
        last.rms = rms;
        last.peak = peak;
        last.intensity_z = intensity_z;
        last.spectral_z = spectral_z;

        if (intensity_z > config.intensity_z_limit) {
            trip(EmStatus::intensity_anomaly, rms, peak, intensity_z, spectral_z);
        } else if (std::abs(spectral_z) > config.spectral_z_limit) {
            trip(EmStatus::spectral_anomaly, rms, peak, intensity_z, spectral_z);
        } else {
            status = EmStatus::nominal;
            last.status = EmStatus::nominal;
            last.key_operations_allowed = true;
        }
        return last;
    }

    EmMonitorConfig config;
    mutable std::mutex mutex;
    EmStatus status = EmStatus::calibrating;
    bool locked = false;
    std::uint64_t sequence = 0;
    std::size_t samples = 0;
    double mean_rms = 0.0;
    double m2_rms = 0.0;
    double mean_peak = 0.0;
    double m2_peak = 0.0;
    std::vector<double> mean_spectrum;
    std::vector<double> m2_spectrum;
    EmObservation last;
};

EmMonitor::EmMonitor(EmMonitorConfig config)
    : impl_(std::make_unique<Impl>(config)) {}

EmMonitor::~EmMonitor() = default;
EmMonitor::EmMonitor(EmMonitor&&) noexcept = default;
EmMonitor& EmMonitor::operator=(EmMonitor&&) noexcept = default;

EmObservation EmMonitor::ingest(const EmTrace& trace) {
    return ingest(trace.rms, trace.peak, trace.spectrum);
}

EmObservation EmMonitor::ingest(
    double rms,
    double peak,
    std::span<const double> spectrum) {
    if (!impl_) {
        return {};
    }
    return impl_->observe(rms, peak, spectrum);
}

EmObservation EmMonitor::calibrate(std::span<const EmTrace> benign_traces) {
    if (!impl_ || benign_traces.empty()) {
        return impl_ ? impl_->observe(0.0, 0.0, {}) : EmObservation{};
    }

    const std::size_t spectrum_size = benign_traces.front().spectrum.size();
    for (const auto& trace : benign_traces) {
        if (!valid_trace_values(trace.rms, trace.peak, trace.spectrum) ||
            trace.spectrum.size() != spectrum_size) {
            std::scoped_lock lock(impl_->mutex);
            impl_->reset_locked();
            impl_->trip(EmStatus::sensor_fault, trace.rms, trace.peak, 0.0, 0.0);
            return impl_->last;
        }
    }

    std::scoped_lock lock(impl_->mutex);
    impl_->reset_locked();
    impl_->mean_spectrum.assign(spectrum_size, 0.0);
    impl_->m2_spectrum.assign(spectrum_size, 0.0);
    for (const auto& trace : benign_traces) {
        ++impl_->samples;
        const double rms_delta = trace.rms - impl_->mean_rms;
        impl_->mean_rms += rms_delta / static_cast<double>(impl_->samples);
        impl_->m2_rms += rms_delta * (trace.rms - impl_->mean_rms);

        const double peak_delta = trace.peak - impl_->mean_peak;
        impl_->mean_peak += peak_delta / static_cast<double>(impl_->samples);
        impl_->m2_peak += peak_delta * (trace.peak - impl_->mean_peak);

        for (std::size_t i = 0; i < spectrum_size; ++i) {
            const double delta = trace.spectrum[i] - impl_->mean_spectrum[i];
            impl_->mean_spectrum[i] += delta / static_cast<double>(impl_->samples);
            impl_->m2_spectrum[i] +=
                delta * (trace.spectrum[i] - impl_->mean_spectrum[i]);
        }
    }
    impl_->status = EmStatus::nominal;
    impl_->last = {};
    impl_->last.status = EmStatus::nominal;
    impl_->last.baseline_samples = impl_->samples;
    impl_->last.key_operations_allowed = true;
    return impl_->last;
}

bool EmMonitor::allows_key_operation() const noexcept {
    if (!impl_) {
        return false;
    }
    std::scoped_lock lock(impl_->mutex);
    return !impl_->locked && impl_->status == EmStatus::nominal;
}

EmStatus EmMonitor::status() const noexcept {
    if (!impl_) {
        return EmStatus::sensor_fault;
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->status;
}

EmObservation EmMonitor::snapshot() const noexcept {
    if (!impl_) {
        return {};
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->last;
}

const EmMonitorConfig& EmMonitor::config() const noexcept {
    static const EmMonitorConfig default_config{};
    return impl_ ? impl_->config : default_config;
}

void EmMonitor::reset() noexcept {
    if (!impl_) {
        return;
    }
    std::scoped_lock lock(impl_->mutex);
    impl_->reset_locked();
}

EmCollector::EmCollector(EmSensor& sensor, EmMonitor* monitor) noexcept
    : sensor_(sensor), monitor_(monitor) {}

bool EmCollector::collect(
    std::size_t count,
    std::vector<EmTrace>& output) {
    output.clear();
    output.reserve(count);
    for (std::size_t i = 0; i < count; ++i) {
        EmTrace trace;
        if (!sensor_.capture(trace)) {
            return false;
        }
        if (monitor_ != nullptr) {
            (void)monitor_->ingest(trace);
        }
        output.push_back(std::move(trace));
    }
    return true;
}

struct TemplateAnalyzer::Impl {
    struct Template {
        std::vector<double> mean;
        std::size_t count = 0;
    };

    static bool extract(const EmTrace& trace, std::vector<double>& output) {
        if (!trace.spectrum.empty()) {
            output.assign(trace.spectrum.begin(), trace.spectrum.end());
            return true;
        }
        output.assign({trace.rms, trace.peak});
        return true;
    }

    std::array<Template, 256> classes;
    std::vector<double> variance;
    std::size_t feature_count = 0;
    std::size_t trained_classes = 0;
};

TemplateAnalyzer::TemplateAnalyzer() : impl_(std::make_unique<Impl>()) {}
TemplateAnalyzer::~TemplateAnalyzer() = default;
TemplateAnalyzer::TemplateAnalyzer(TemplateAnalyzer&&) noexcept = default;
TemplateAnalyzer& TemplateAnalyzer::operator=(TemplateAnalyzer&&) noexcept = default;

bool TemplateAnalyzer::train(
    std::span<const EmTrace> traces,
    std::span<const std::uint8_t> labels) {
    if (!impl_ || traces.empty() || traces.size() != labels.size()) {
        return false;
    }

    impl_->classes = {};
    impl_->variance.clear();
    impl_->feature_count = 0;
    impl_->trained_classes = 0;

    std::vector<double> feature;
    std::vector<double> sums;
    std::vector<double> sum_squares;
    std::size_t total_count = 0;

    for (std::size_t i = 0; i < traces.size(); ++i) {
        if (!Impl::extract(traces[i], feature) || feature.empty()) {
            return false;
        }
        if (impl_->feature_count == 0) {
            impl_->feature_count = feature.size();
            sums.assign(impl_->feature_count, 0.0);
            sum_squares.assign(impl_->feature_count, 0.0);
        }
        if (feature.size() != impl_->feature_count) {
            return false;
        }

        auto& model = impl_->classes[labels[i]];
        if (model.mean.empty()) {
            model.mean.assign(impl_->feature_count, 0.0);
        }
        ++model.count;
        ++total_count;
        for (std::size_t j = 0; j < impl_->feature_count; ++j) {
            model.mean[j] += feature[j];
            sums[j] += feature[j];
            sum_squares[j] += feature[j] * feature[j];
        }
    }

    for (auto& model : impl_->classes) {
        if (model.count == 0) {
            continue;
        }
        ++impl_->trained_classes;
        for (double& value : model.mean) {
            value /= static_cast<double>(model.count);
        }
    }

    impl_->variance.assign(impl_->feature_count, 0.0);
    const double variance_denominator =
        static_cast<double>(std::max<std::size_t>(total_count, 2) - 1);
    for (std::size_t j = 0; j < impl_->feature_count; ++j) {
        const double centred_sum =
            sum_squares[j] - sums[j] * sums[j] / static_cast<double>(total_count);
        impl_->variance[j] = std::max(centred_sum / variance_denominator, 1.0e-9);
    }
    return impl_->trained_classes >= 2;
}

std::uint8_t TemplateAnalyzer::classify(const EmTrace& trace) const {
    if (!impl_ || impl_->trained_classes == 0) {
        return 0;
    }
    std::vector<double> feature;
    if (!Impl::extract(trace, feature) || feature.size() != impl_->feature_count) {
        return 0;
    }

    std::uint8_t best_label = 0;
    double best_score = -std::numeric_limits<double>::infinity();
    for (std::size_t label = 0; label < impl_->classes.size(); ++label) {
        const auto& model = impl_->classes[label];
        if (model.count == 0) {
            continue;
        }
        double score = 0.0;
        for (std::size_t j = 0; j < impl_->feature_count; ++j) {
            const double delta = feature[j] - model.mean[j];
            score -= delta * delta / impl_->variance[j];
        }
        if (score > best_score) {
            best_score = score;
            best_label = static_cast<std::uint8_t>(label);
        }
    }
    return best_label;
}

double TemplateAnalyzer::evaluate(
    std::span<const EmTrace> traces,
    std::span<const std::uint8_t> labels) const {
    if (traces.empty() || traces.size() != labels.size() || !impl_ ||
        impl_->trained_classes == 0) {
        return 0.0;
    }
    std::size_t correct = 0;
    for (std::size_t i = 0; i < traces.size(); ++i) {
        if (classify(traces[i]) == labels[i]) {
            ++correct;
        }
    }
    return static_cast<double>(correct) / static_cast<double>(traces.size());
}

std::size_t TemplateAnalyzer::trained_classes() const noexcept {
    return impl_ ? impl_->trained_classes : 0;
}

struct ProtectedAes::Impl {
    Impl(
        PhysicalProtectionArea protected_area,
        EmMonitor* em_monitor,
        RandomSource& random_source)
        : area(std::move(protected_area)),
          monitor(em_monitor),
          random(random_source) {}

    ~Impl() {
        wipe_schedule(schedule);
    }

    PhysicalProtectionArea area;
    EmMonitor* monitor = nullptr;
    RandomSource& random;
    mutable std::mutex mutex;
    std::vector<MaskedByte> schedule;
    std::size_t key_bits = 0;
    std::uint64_t key_id = 0;
    std::uint64_t next_operation_id = 1;
    bool ready = false;
};

ProtectedAes::ProtectedAes(
    PhysicalProtectionArea area,
    const ProtectionLease& lease,
    std::span<const std::uint8_t> key,
    RandomSource& random,
    EmMonitor* monitor)
    : impl_(std::make_unique<Impl>(std::move(area), monitor, random)) {
    if (key.size() != kAes128KeySize && key.size() != kAes256KeySize) {
        throw std::invalid_argument("AES key must be 16 or 32 bytes");
    }
    if (!impl_->area.validate(lease)) {
        throw std::invalid_argument("key import outside the physical protection area");
    }
    if (monitor != nullptr && !monitor->allows_key_operation()) {
        throw std::invalid_argument("EM monitor has not authorized key import");
    }

    RandomPool pool(random);
    std::size_t bits = 0;
    if (!expand_key(key, impl_->schedule, pool, bits)) {
        wipe_schedule(impl_->schedule);
        throw std::runtime_error("failed to expand masked AES key schedule");
    }
    impl_->key_bits = bits;
    impl_->key_id = random_key_id(pool);
    impl_->ready = true;
}

ProtectedAes::~ProtectedAes() = default;
ProtectedAes::ProtectedAes(ProtectedAes&&) noexcept = default;
ProtectedAes& ProtectedAes::operator=(ProtectedAes&&) noexcept = default;

bool ProtectedAes::ready() const noexcept {
    if (!impl_) {
        return false;
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->ready;
}

std::size_t ProtectedAes::key_bits() const noexcept {
    if (!impl_) {
        return 0;
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->key_bits;
}

std::uint64_t ProtectedAes::key_id() const noexcept {
    if (!impl_) {
        return 0;
    }
    std::scoped_lock lock(impl_->mutex);
    return impl_->key_id;
}

Status ProtectedAes::process(
    std::span<const std::uint8_t> input,
    std::span<std::uint8_t> output,
    const ProtectionLease& lease,
    OperationReport* report,
    bool decrypting) noexcept {
    if (report != nullptr) {
        *report = {};
    }
    if (!impl_) {
        return Status::not_ready;
    }
    if (input.size() != kAesBlockSize || output.size() != kAesBlockSize) {
        return Status::invalid_size;
    }

    std::scoped_lock lock(impl_->mutex);
    if (!impl_->ready) {
        return Status::not_ready;
    }

    const bool authorized = impl_->area.validate(lease);
    const bool monitor_allowed =
        impl_->monitor == nullptr || impl_->monitor->allows_key_operation();
    if (report != nullptr) {
        report->monitor_status = impl_->monitor == nullptr
            ? EmStatus::nominal
            : impl_->monitor->status();
        report->key_area_authorized = authorized;
        report->monitor_allowed = monitor_allowed;
    }
    if (!authorized) {
        return Status::unauthorized;
    }
    if (!monitor_allowed) {
        return Status::monitor_blocked;
    }

    RandomPool pool(impl_->random);
    MaskedState state{};
    std::array<std::uint8_t, kAesBlockSize> result{};
    OperationStats stats;

    split_state(
        std::span<const std::uint8_t, kAesBlockSize>(input.data(), kAesBlockSize),
        state,
        pool);

    const std::size_t rounds = impl_->key_bits == 128 ? 10 : 14;
    if (decrypting) {
        decrypt_state(state, impl_->schedule, rounds, pool, stats);
    } else {
        encrypt_state(state, impl_->schedule, rounds, pool, stats);
    }

    const bool random_ok = pool.ready();
    if (random_ok) {
        join_state(
            state,
            std::span<std::uint8_t, kAesBlockSize>(result.data(), kAesBlockSize));
        for (std::size_t i = 0; i < kAesBlockSize; ++i) {
            output[i] = result[i];
        }
        compiler_barrier();
    }

    wipe_masked_state(state);
    secure_erase(result.data(), result.size());

    if (!random_ok) {
        return Status::random_failure;
    }

    if (report != nullptr) {
        report->operation_id = impl_->next_operation_id++;
        if (impl_->next_operation_id == 0) {
            impl_->next_operation_id = 1;
        }
        report->randomized_orders = stats.randomized_orders;
        report->mask_refreshes = stats.mask_refreshes;
        report->random_bytes_consumed = pool.consumed();
        report->monitor_status = impl_->monitor == nullptr
            ? EmStatus::nominal
            : impl_->monitor->status();
        report->key_area_authorized = true;
        report->monitor_allowed = true;
    }
    return Status::ok;
}

Status ProtectedAes::encrypt(
    std::span<const std::uint8_t> plaintext,
    std::span<std::uint8_t> ciphertext,
    const ProtectionLease& lease,
    OperationReport* report) noexcept {
    return process(plaintext, ciphertext, lease, report, false);
}

Status ProtectedAes::decrypt(
    std::span<const std::uint8_t> ciphertext,
    std::span<std::uint8_t> plaintext,
    const ProtectionLease& lease,
    OperationReport* report) noexcept {
    return process(ciphertext, plaintext, lease, report, true);
}

Status ProtectedAes::encrypt_block(
    const std::array<std::uint8_t, kAesBlockSize>& plaintext,
    std::array<std::uint8_t, kAesBlockSize>& ciphertext,
    const ProtectionLease& lease,
    OperationReport* report) noexcept {
    return encrypt(
        std::span<const std::uint8_t, kAesBlockSize>(plaintext),
        std::span<std::uint8_t, kAesBlockSize>(ciphertext),
        lease,
        report);
}

Status ProtectedAes::decrypt_block(
    const std::array<std::uint8_t, kAesBlockSize>& ciphertext,
    std::array<std::uint8_t, kAesBlockSize>& plaintext,
    const ProtectionLease& lease,
    OperationReport* report) noexcept {
    return decrypt(
        std::span<const std::uint8_t, kAesBlockSize>(ciphertext),
        std::span<std::uint8_t, kAesBlockSize>(plaintext),
        lease,
        report);
}

Status ProtectedAes::rotate_key(
    const ProtectionLease& lease,
    std::span<const std::uint8_t> key) noexcept {
    if (!impl_) {
        return Status::not_ready;
    }
    std::scoped_lock lock(impl_->mutex);
    if (!impl_->area.validate(lease)) {
        return Status::unauthorized;
    }
    if (impl_->monitor != nullptr && !impl_->monitor->allows_key_operation()) {
        return Status::monitor_blocked;
    }

    wipe_schedule(impl_->schedule);
    impl_->ready = false;
    impl_->key_bits = 0;
    impl_->key_id = 0;
    if (key.size() != kAes128KeySize && key.size() != kAes256KeySize) {
        return Status::invalid_size;
    }

    RandomPool pool(impl_->random);
    std::size_t bits = 0;
    if (!expand_key(key, impl_->schedule, pool, bits)) {
        wipe_schedule(impl_->schedule);
        return Status::random_failure;
    }
    impl_->key_bits = bits;
    impl_->key_id = random_key_id(pool);
    impl_->ready = true;
    return Status::ok;
}

void ProtectedAes::erase() noexcept {
    if (!impl_) {
        return;
    }
    std::scoped_lock lock(impl_->mutex);
    wipe_schedule(impl_->schedule);
    impl_->ready = false;
    impl_->key_bits = 0;
    impl_->key_id = 0;
}

} // namespace netsec
