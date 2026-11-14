#pragma once

#include <array>
#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <span>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

inline constexpr std::size_t kAesBlockSize = 16;
inline constexpr std::size_t kAes128KeySize = 16;
inline constexpr std::size_t kAes256KeySize = 32;

enum class Status : std::uint8_t {
    ok = 0,
    invalid_argument,
    invalid_size,
    unauthorized,
    monitor_blocked,
    random_failure,
    not_ready,
};

[[nodiscard]] std::string_view to_string(Status status) noexcept;

// Volatile overwrite followed by a compiler barrier. The buffer may still have
// copies in registers, caches, swap, or crash dumps; callers that need stronger
// guarantees must combine this with locked pages and hardware isolation.
void secure_erase(void* data, std::size_t size) noexcept;
void secure_erase(std::span<std::uint8_t> data) noexcept;

class RandomSource {
public:
    virtual ~RandomSource() = default;
    virtual bool fill(std::span<std::uint8_t> output) noexcept = 0;
};

// Uses getrandom(2), arc4random_buf(3), or BCryptGenRandom depending on the
// target platform.
class SystemRandomSource final : public RandomSource {
public:
    bool fill(std::span<std::uint8_t> output) noexcept override;
};

// Deterministic test source. It is deliberately not suitable for production.
class DeterministicRandomSource final : public RandomSource {
public:
    explicit DeterministicRandomSource(std::uint64_t seed) noexcept;
    bool fill(std::span<std::uint8_t> output) noexcept override;
    void reseed(std::uint64_t seed) noexcept;

private:
    std::uint64_t state_;
};

// A lease is a short-lived capability proving that the caller is executing in
// the physical protection area represented by the issuing zone. A new lease
// supersedes older leases for the same zone.
class ProtectionLease {
public:
    ProtectionLease() noexcept = default;
    [[nodiscard]] bool valid() const noexcept;
    [[nodiscard]] std::chrono::steady_clock::time_point expires_at() const noexcept;

private:
    friend class PhysicalProtectionArea;

    std::uint64_t zone_id_ = 0;
    std::array<std::uint8_t, 16> proof_{};
    std::chrono::steady_clock::time_point expires_{};
};

// Models an attested physical boundary. The capability check is meaningful
// only when the zone is created by trusted boot/enclave code; this component
// cannot prove the physical location of a process by itself.
class PhysicalProtectionArea {
public:
    // Copies share the same zone identity/capability and observe the same
    // revocation state.
    PhysicalProtectionArea(std::string region_id, RandomSource& random);
    ~PhysicalProtectionArea();

    PhysicalProtectionArea(const PhysicalProtectionArea&) = default;
    PhysicalProtectionArea& operator=(const PhysicalProtectionArea&) = default;
    PhysicalProtectionArea(PhysicalProtectionArea&&) noexcept = default;
    PhysicalProtectionArea& operator=(PhysicalProtectionArea&&) noexcept = default;

    [[nodiscard]] std::uint64_t id() const noexcept;
    [[nodiscard]] std::string_view region_id() const noexcept;
    [[nodiscard]] bool active() const noexcept;

    [[nodiscard]] ProtectionLease lease(
        std::chrono::milliseconds ttl = std::chrono::seconds(5)) noexcept;
    [[nodiscard]] bool validate(const ProtectionLease& lease) const noexcept;
    void revoke() noexcept;

private:
    struct Impl;
    std::shared_ptr<Impl> impl_;
};

enum class EmStatus : std::uint8_t {
    calibrating = 0,
    nominal,
    intensity_anomaly,
    spectral_anomaly,
    sensor_fault,
    locked_out,
};

[[nodiscard]] std::string_view to_string(EmStatus status) noexcept;

struct EmTrace {
    std::uint64_t sequence = 0;
    double rms = 0.0;
    double peak = 0.0;
    std::vector<double> spectrum;
};

struct EmMonitorConfig {
    // The monitor remains in the calibrating state until this many benign
    // traces have been observed (or calibrate() is called explicitly).
    std::size_t warmup_samples = 8;
    double rms_limit = 1.0;
    double peak_limit = 4.0;
    double intensity_z_limit = 6.0;
    double spectral_z_limit = 6.0;
    double variance_floor = 1.0e-9;
    // Locking is recommended. If disabled, a later nominal sample can re-arm
    // the monitor, which is only appropriate for benign lab environments.
    bool lock_on_anomaly = true;
};

struct EmObservation {
    EmStatus status = EmStatus::calibrating;
    std::uint64_t sequence = 0;
    std::size_t baseline_samples = 0;
    double rms = 0.0;
    double peak = 0.0;
    double intensity_z = 0.0;
    double spectral_z = 0.0;
    bool key_operations_allowed = false;
};

// Continuous EM/spectrum health monitor. Once an anomaly is observed the
// monitor latches and rejects key use until reset() is called from a trusted
// calibration path.
class EmMonitor {
public:
    explicit EmMonitor(EmMonitorConfig config = {});
    ~EmMonitor();

    EmMonitor(const EmMonitor&) = delete;
    EmMonitor& operator=(const EmMonitor&) = delete;
    EmMonitor(EmMonitor&&) noexcept;
    EmMonitor& operator=(EmMonitor&&) noexcept;

    [[nodiscard]] EmObservation ingest(const EmTrace& trace);
    [[nodiscard]] EmObservation ingest(
        double rms, double peak, std::span<const double> spectrum);
    [[nodiscard]] EmObservation calibrate(std::span<const EmTrace> benign_traces);

    [[nodiscard]] bool allows_key_operation() const noexcept;
    [[nodiscard]] EmStatus status() const noexcept;
    [[nodiscard]] EmObservation snapshot() const noexcept;
    [[nodiscard]] const EmMonitorConfig& config() const noexcept;

    void reset() noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

class EmSensor {
public:
    virtual ~EmSensor() = default;
    // Capture one complete spectrum. Returning false indicates a sensor or
    // acquisition failure and must fail closed at the caller.
    virtual bool capture(EmTrace& trace) noexcept = 0;
};

class EmCollector {
public:
    explicit EmCollector(EmSensor& sensor, EmMonitor* monitor = nullptr) noexcept;
    [[nodiscard]] bool collect(std::size_t count, std::vector<EmTrace>& output);

private:
    EmSensor& sensor_;
    EmMonitor* monitor_;
};

// Diagonal-Gaussian template classifier used by leakage evaluation. It is a
// lab/verification helper, not part of the key-use path.
class TemplateAnalyzer {
public:
    TemplateAnalyzer();
    ~TemplateAnalyzer();

    TemplateAnalyzer(const TemplateAnalyzer&) = delete;
    TemplateAnalyzer& operator=(const TemplateAnalyzer&) = delete;
    TemplateAnalyzer(TemplateAnalyzer&&) noexcept;
    TemplateAnalyzer& operator=(TemplateAnalyzer&&) noexcept;

    [[nodiscard]] bool train(
        std::span<const EmTrace> traces, std::span<const std::uint8_t> labels);
    [[nodiscard]] std::uint8_t classify(const EmTrace& trace) const;
    [[nodiscard]] double evaluate(
        std::span<const EmTrace> traces,
        std::span<const std::uint8_t> labels) const;
    [[nodiscard]] std::size_t trained_classes() const noexcept;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

struct OperationReport {
    std::uint64_t operation_id = 0;
    std::uint64_t randomized_orders = 0;
    std::uint64_t mask_refreshes = 0;
    std::uint64_t random_bytes_consumed = 0;
    EmStatus monitor_status = EmStatus::nominal;
    bool key_area_authorized = false;
    bool monitor_allowed = true;
};

// AES-128/AES-256 with a two-share Boolean masked state and masked key
// schedule. SubBytes uses a full 256-entry constant-address scan under an
// ISW-style masked equality; MixColumns columns and independent S-boxes are
// visited in fresh random permutations.
class ProtectedAes {
public:
    // Throws std::invalid_argument for a bad key size and std::runtime_error if
    // the random source fails during key import. `random` and `monitor` must
    // outlive this object. Calls on one ProtectedAes instance are serialized,
    // but a non-thread-safe RandomSource must not be shared by concurrent
    // instances.
    ProtectedAes(
        PhysicalProtectionArea area,
        const ProtectionLease& lease,
        std::span<const std::uint8_t> key,
        RandomSource& random,
        EmMonitor* monitor = nullptr);
    ~ProtectedAes();

    ProtectedAes(const ProtectedAes&) = delete;
    ProtectedAes& operator=(const ProtectedAes&) = delete;
    ProtectedAes(ProtectedAes&&) noexcept;
    ProtectedAes& operator=(ProtectedAes&&) noexcept;

    [[nodiscard]] bool ready() const noexcept;
    [[nodiscard]] std::size_t key_bits() const noexcept;
    [[nodiscard]] std::uint64_t key_id() const noexcept;

    [[nodiscard]] Status encrypt(
        std::span<const std::uint8_t> plaintext,
        std::span<std::uint8_t> ciphertext,
        const ProtectionLease& lease,
        OperationReport* report = nullptr) noexcept;
    [[nodiscard]] Status decrypt(
        std::span<const std::uint8_t> ciphertext,
        std::span<std::uint8_t> plaintext,
        const ProtectionLease& lease,
        OperationReport* report = nullptr) noexcept;

    [[nodiscard]] Status encrypt_block(
        const std::array<std::uint8_t, kAesBlockSize>& plaintext,
        std::array<std::uint8_t, kAesBlockSize>& ciphertext,
        const ProtectionLease& lease,
        OperationReport* report = nullptr) noexcept;
    [[nodiscard]] Status decrypt_block(
        const std::array<std::uint8_t, kAesBlockSize>& ciphertext,
        std::array<std::uint8_t, kAesBlockSize>& plaintext,
        const ProtectionLease& lease,
        OperationReport* report = nullptr) noexcept;

    // Replaces the expanded key schedule. The old schedule is wiped even if the
    // new key is rejected.
    [[nodiscard]] Status rotate_key(
        const ProtectionLease& lease,
        std::span<const std::uint8_t> key) noexcept;
    void erase() noexcept;

private:
    [[nodiscard]] Status process(
        std::span<const std::uint8_t> input,
        std::span<std::uint8_t> output,
        const ProtectionLease& lease,
        OperationReport* report,
        bool decrypting) noexcept;

    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using MaskedAes = ProtectedAes;
using SideChannelProtectedAes = ProtectedAes;

} // namespace netsec
