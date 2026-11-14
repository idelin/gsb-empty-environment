#include "netsec.hpp"

#include <array>
#include <cmath>
#include <cstdint>
#include <iostream>
#include <limits>
#include <random>
#include <string>
#include <vector>

namespace {

int failures = 0;

#define CHECK(condition)                                                               \
    do {                                                                               \
        if (!(condition)) {                                                            \
            std::cerr << __FILE__ << ':' << __LINE__                                   \
                      << ": check failed: " #condition << '\n';                        \
            ++failures;                                                                \
        }                                                                              \
    } while (false)

using Bytes16 = std::array<std::uint8_t, netsec::kAesBlockSize>;

Bytes16 bytes_from_hex(const char* hex) {
    Bytes16 output{};
    for (std::size_t i = 0; i < output.size(); ++i) {
        const auto nibble = [](char value) -> unsigned {
            if (value >= '0' && value <= '9') {
                return static_cast<unsigned>(value - '0');
            }
            if (value >= 'a' && value <= 'f') {
                return static_cast<unsigned>(value - 'a' + 10);
            }
            if (value >= 'A' && value <= 'F') {
                return static_cast<unsigned>(value - 'A' + 10);
            }
            return 0;
        };
        output[i] = static_cast<std::uint8_t>(
            (nibble(hex[2 * i]) << 4U) | nibble(hex[2 * i + 1]));
    }
    return output;
}

Bytes16 bytes_from_sequence(unsigned start) {
    Bytes16 output{};
    for (std::size_t i = 0; i < output.size(); ++i) {
        output[i] = static_cast<std::uint8_t>(start + i);
    }
    return output;
}

bool equal(const Bytes16& left, const Bytes16& right) {
    return left == right;
}

unsigned hamming_weight(std::uint8_t value) {
    unsigned count = 0;
    while (value != 0) {
        count += value & 1U;
        value = static_cast<std::uint8_t>(value >> 1U);
    }
    return count;
}

netsec::EmTrace make_trace(
    std::uint64_t sequence,
    double rms,
    double peak,
    const std::vector<double>& spectrum) {
    netsec::EmTrace trace;
    trace.sequence = sequence;
    trace.rms = rms;
    trace.peak = peak;
    trace.spectrum = spectrum;
    return trace;
}

struct CryptoFixture {
    netsec::DeterministicRandomSource random{0xC0FFEE123456789ULL};
    netsec::PhysicalProtectionArea area{"enclave-lab-a", random};
    netsec::ProtectionLease lease = area.lease(std::chrono::seconds(30));
};

void test_aes_vectors_and_masking() {
    CryptoFixture fixture;
    const Bytes16 plaintext = bytes_from_hex("00112233445566778899aabbccddeeff");
    const Bytes16 expected128 = bytes_from_hex("69c4e0d86a7b0430d8cdb78070b4c55a");
    const auto key128 = bytes_from_sequence(0x00);

    netsec::ProtectedAes aes128(
        fixture.area, fixture.lease, key128, fixture.random, nullptr);
    CHECK(aes128.ready());
    CHECK(aes128.key_bits() == 128);

    Bytes16 ciphertext{};
    netsec::OperationReport first_report;
    CHECK(aes128.encrypt_block(
              plaintext, ciphertext, fixture.lease, &first_report) == netsec::Status::ok);
    CHECK(equal(ciphertext, expected128));
    CHECK(first_report.randomized_orders == 19);
    CHECK(first_report.mask_refreshes == 160);
    CHECK(first_report.random_bytes_consumed > 100000);
    CHECK(first_report.key_area_authorized);
    CHECK(first_report.monitor_allowed);
    CHECK(first_report.monitor_status == netsec::EmStatus::nominal);
    CHECK(aes128.key_id() != 0);

    Bytes16 decrypted{};
    CHECK(aes128.decrypt_block(
              ciphertext, decrypted, fixture.lease, nullptr) == netsec::Status::ok);
    CHECK(decrypted == plaintext);

    // Different fresh mask streams must not change the public result.
    for (int i = 0; i < 3; ++i) {
        Bytes16 repeated{};
        netsec::OperationReport report;
        CHECK(aes128.encrypt_block(
                  plaintext, repeated, fixture.lease, &report) == netsec::Status::ok);
        CHECK(equal(repeated, expected128));
        CHECK(report.operation_id != first_report.operation_id);
        CHECK(report.random_bytes_consumed != 0);
    }

    std::array<std::uint8_t, 32> key256{};
    for (std::size_t i = 0; i < key256.size(); ++i) {
        key256[i] = static_cast<std::uint8_t>(i);
    }
    netsec::ProtectedAes aes256(
        fixture.area, fixture.lease, key256, fixture.random, nullptr);
    CHECK(aes256.key_bits() == 256);
    Bytes16 ciphertext256{};
    netsec::OperationReport report256;
    CHECK(aes256.encrypt_block(
              plaintext, ciphertext256, fixture.lease, &report256) == netsec::Status::ok);
    CHECK(equal(ciphertext256, bytes_from_hex("8ea2b7ca516745bfeafc49904b496089")));
    CHECK(report256.randomized_orders == 27);
    CHECK(report256.mask_refreshes == 224);

    Bytes16 decrypted256{};
    CHECK(aes256.decrypt_block(
              ciphertext256, decrypted256, fixture.lease, nullptr) == netsec::Status::ok);
    CHECK(decrypted256 == plaintext);

    CHECK(aes128.encrypt(plaintext, ciphertext, fixture.lease) == netsec::Status::ok);
    const Bytes16 before_rotation = ciphertext;
    const std::uint64_t key_id_before_rotation = aes128.key_id();
    CHECK(aes128.rotate_key(fixture.lease, key256) == netsec::Status::ok);
    CHECK(aes128.key_bits() == 256);
    CHECK(aes128.key_id() != key_id_before_rotation);
    CHECK(aes128.encrypt(plaintext, ciphertext, fixture.lease) == netsec::Status::ok);
    CHECK(ciphertext == ciphertext256);
    CHECK(ciphertext != before_rotation);

    CHECK(aes128.encrypt(
              std::span<const std::uint8_t>(plaintext).first(15),
              std::span<std::uint8_t>(ciphertext).first(15),
              fixture.lease) == netsec::Status::invalid_size);

    aes128.erase();
    CHECK(!aes128.ready());
    CHECK(aes128.encrypt(plaintext, ciphertext, fixture.lease) == netsec::Status::not_ready);
}

void test_physical_area_authorization() {
    CryptoFixture fixture;
    const auto key = bytes_from_sequence(0x10);
    const Bytes16 plaintext = bytes_from_sequence(0x40);

    netsec::ProtectedAes aes(
        fixture.area, fixture.lease, key, fixture.random, nullptr);
    Bytes16 ciphertext{};
    CHECK(aes.encrypt_block(plaintext, ciphertext, fixture.lease) == netsec::Status::ok);

    const netsec::ProtectionLease stale = fixture.lease;
    const netsec::ProtectionLease current = fixture.area.lease(std::chrono::seconds(30));
    CHECK(current.valid());
    CHECK(!fixture.area.validate(stale));
    CHECK(aes.encrypt_block(plaintext, ciphertext, stale) == netsec::Status::unauthorized);
    CHECK(aes.encrypt_block(plaintext, ciphertext, current) == netsec::Status::ok);

    netsec::DeterministicRandomSource other_random{77};
    netsec::PhysicalProtectionArea other_area{"enclave-lab-b", other_random};
    const auto other_lease = other_area.lease();
    CHECK(aes.encrypt_block(plaintext, ciphertext, other_lease) == netsec::Status::unauthorized);

    fixture.area.revoke();
    CHECK(!fixture.area.active());
    CHECK(aes.encrypt_block(plaintext, ciphertext, current) == netsec::Status::unauthorized);
    CHECK(!fixture.area.lease().valid());
}

void test_em_monitor_and_key_gating() {
    netsec::EmMonitorConfig config;
    config.warmup_samples = 16;
    config.rms_limit = 1.0;
    config.peak_limit = 3.0;
    config.intensity_z_limit = 6.0;
    config.spectral_z_limit = 6.0;
    netsec::EmMonitor monitor(config);

    std::mt19937_64 noise(1234);
    std::uniform_real_distribution<double> jitter(-0.002, 0.002);
    std::vector<netsec::EmTrace> benign;
    for (std::uint64_t i = 0; i < 32; ++i) {
        std::vector<double> spectrum(16);
        for (double& value : spectrum) {
            value = 0.20 + jitter(noise);
        }
        benign.push_back(make_trace(i, 0.12 + jitter(noise), 0.35 + jitter(noise), spectrum));
    }
    const auto calibration = monitor.calibrate(benign);
    CHECK(calibration.status == netsec::EmStatus::nominal);
    CHECK(calibration.key_operations_allowed);
    CHECK(monitor.allows_key_operation());

    CryptoFixture fixture;
    const auto key = bytes_from_sequence(0x20);
    const Bytes16 plaintext = bytes_from_sequence(0x70);
    netsec::ProtectedAes aes(
        fixture.area, fixture.lease, key, fixture.random, &monitor);
    Bytes16 ciphertext{};
    CHECK(aes.encrypt_block(plaintext, ciphertext, fixture.lease) == netsec::Status::ok);

    const auto hot = monitor.ingest(2.0, 2.5, benign.front().spectrum);
    CHECK(hot.status == netsec::EmStatus::intensity_anomaly);
    CHECK(!hot.key_operations_allowed);
    CHECK(!monitor.allows_key_operation());
    netsec::OperationReport blocked_report;
    CHECK(aes.encrypt_block(plaintext, ciphertext, fixture.lease, &blocked_report) ==
          netsec::Status::monitor_blocked);
    CHECK(!blocked_report.monitor_allowed);
    CHECK(blocked_report.monitor_status == netsec::EmStatus::intensity_anomaly);

    // A trusted calibration path rearms the monitor.
    CHECK(monitor.calibrate(benign).status == netsec::EmStatus::nominal);
    CHECK(aes.encrypt_block(plaintext, ciphertext, fixture.lease) == netsec::Status::ok);

    std::vector<double> spectral_spike = benign.front().spectrum;
    spectral_spike[5] += 0.75;
    const auto spectral = monitor.ingest(0.12, 0.35, spectral_spike);
    CHECK(spectral.status == netsec::EmStatus::spectral_anomaly);
    CHECK(std::abs(spectral.spectral_z) > config.spectral_z_limit);
    CHECK(aes.encrypt_block(plaintext, ciphertext, fixture.lease) ==
          netsec::Status::monitor_blocked);

    monitor.reset();
    const auto nan_trace = monitor.ingest(
        std::numeric_limits<double>::quiet_NaN(), 0.2, benign.front().spectrum);
    CHECK(nan_trace.status == netsec::EmStatus::sensor_fault);
    CHECK(!monitor.allows_key_operation());
}

class SyntheticSensor final : public netsec::EmSensor {
public:
    explicit SyntheticSensor(std::size_t fail_at)
        : fail_at_(fail_at) {}

    bool capture(netsec::EmTrace& trace) noexcept override {
        if (next_ == fail_at_) {
            return false;
        }
        trace.sequence = next_;
        trace.rms = 0.1;
        trace.peak = 0.3;
        trace.spectrum.assign(8, 0.125);
        ++next_;
        return true;
    }

private:
    std::size_t next_ = 0;
    std::size_t fail_at_ = 0;
};

void test_em_collection() {
    SyntheticSensor sensor(100);
    netsec::EmMonitor monitor;
    netsec::EmCollector collector(sensor, &monitor);
    std::vector<netsec::EmTrace> traces;
    CHECK(collector.collect(8, traces));
    CHECK(traces.size() == 8);
    CHECK(traces.front().sequence == 0);
    CHECK(traces.back().sequence == 7);
    CHECK(monitor.status() == netsec::EmStatus::nominal);

    SyntheticSensor failing_sensor(3);
    netsec::EmCollector failing_collector(failing_sensor, nullptr);
    CHECK(!failing_collector.collect(8, traces));
    CHECK(traces.size() == 3);
}

void test_template_recovery() {
    constexpr std::size_t classes = 4;
    constexpr std::size_t per_class = 80;
    std::mt19937_64 random(0x5EED);
    std::normal_distribution<double> noise(0.0, 0.18);
    std::uniform_int_distribution<unsigned> mask_distribution(0, 255);

    std::vector<netsec::EmTrace> unprotected_train;
    std::vector<netsec::EmTrace> unprotected_test;
    std::vector<netsec::EmTrace> protected_train;
    std::vector<netsec::EmTrace> protected_test;
    std::vector<std::uint8_t> unprotected_train_labels;
    std::vector<std::uint8_t> unprotected_test_labels;
    std::vector<std::uint8_t> protected_train_labels;
    std::vector<std::uint8_t> protected_test_labels;

    const auto append = [](std::vector<netsec::EmTrace>& traces,
                           std::vector<std::uint8_t>& labels,
                           std::uint8_t label,
                           double point) {
        traces.push_back(make_trace(traces.size(), 0.1, 0.2, {point}));
        labels.push_back(label);
    };

    for (std::size_t label = 0; label < classes; ++label) {
        for (std::size_t i = 0; i < per_class; ++i) {
            // A first-order single-point leakage model. The protected trace
            // observes HW(value xor fresh_mask); the mask is independent of the
            // secret intermediate and therefore every class has the same
            // marginal distribution.
            const double plaintext = 1.0 + static_cast<double>(label) * 1.5 + noise(random);
            const double protected_point =
                static_cast<double>(hamming_weight(
                    static_cast<std::uint8_t>(
                        (label + 0x31U) ^ mask_distribution(random)))) + noise(random);
            append(unprotected_train, unprotected_train_labels,
                   static_cast<std::uint8_t>(label), plaintext);
            append(protected_train, protected_train_labels,
                   static_cast<std::uint8_t>(label), protected_point);

            const double plaintext_test =
                1.0 + static_cast<double>(label) * 1.5 + noise(random);
            const double protected_test_point =
                static_cast<double>(hamming_weight(
                    static_cast<std::uint8_t>(
                        (label + 0x31U) ^ mask_distribution(random)))) + noise(random);
            append(unprotected_test, unprotected_test_labels,
                   static_cast<std::uint8_t>(label), plaintext_test);
            append(protected_test, protected_test_labels,
                   static_cast<std::uint8_t>(label), protected_test_point);
        }
    }

    netsec::TemplateAnalyzer unprotected_templates;
    CHECK(unprotected_templates.train(unprotected_train, unprotected_train_labels));
    const double unprotected_accuracy =
        unprotected_templates.evaluate(unprotected_test, unprotected_test_labels);
    CHECK(unprotected_accuracy > 0.95);

    netsec::TemplateAnalyzer protected_templates;
    CHECK(protected_templates.train(protected_train, protected_train_labels));
    const double protected_accuracy =
        protected_templates.evaluate(protected_test, protected_test_labels);
    CHECK(protected_accuracy < 0.55);
    std::cout << "template accuracy: unprotected=" << unprotected_accuracy
              << " masked=" << protected_accuracy << '\n';
}

void test_secure_erase() {
    std::array<std::uint8_t, 32> secret{};
    secret.fill(0xA5);
    netsec::secure_erase(secret.data(), secret.size());
    for (const auto value : secret) {
        CHECK(value == 0);
    }
}

} // namespace

int main() {
    test_aes_vectors_and_masking();
    test_physical_area_authorization();
    test_em_monitor_and_key_gating();
    test_em_collection();
    test_template_recovery();
    test_secure_erase();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all netsec tests passed\n";
    return 0;
}
