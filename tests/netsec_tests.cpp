#include "netsec.hpp"

#include <cstdint>
#include <iomanip>
#include <iostream>
#include <span>
#include <sstream>
#include <stdexcept>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using netsec::Bytes;
using netsec::EventDigest;
using netsec::HashAlgorithm;
using netsec::MeasurementEvent;
using netsec::MeasurementLog;
using netsec::MeasurementPolicy;
using netsec::PcrValue;
using netsec::Quote;
using netsec::QuotePolicy;
using netsec::RsaPublicKey;
using netsec::ValidationError;
using netsec::VerificationResult;

constexpr std::string_view kModulusHex =
    "CBCBB950C448BB4F8B290A22402AC3A7732749FFC55193ED18E6C8D90BB39E24"
    "F8FBD7E7CA2B105558F3D02F34DC13D26E6AE250070DDA51857A11900717D024"
    "303536DCA4A4FAF15F50329BB8A6FCCE294BC04A3CCB9FCBDF128D3296CA1EFD"
    "0854BA186D684F1C5A02AA23CA1E9A6BF13C28ADDBFCD793B48C947739B3FE96"
    "6353C8610DBC1D916CF64E951F1718C22176CE2CBA069325BAA4849CABD588B5"
    "D24112E989ADD2A5CC7D8AA787DAE2F5F99D8868F57D6590FD11DCB231DF4EB"
    "DB3AAF26BFB5AB6128F48CED9ECE04D4C62882B54C31B3E83D816F7582261BA"
    "CBC05A4870BD9F285A70AB056EE15980C98F32EB840ECFC8672110AB8069B3B"
    "2D3";

constexpr std::string_view kSignatureHex =
    "1153e10c50f8eeff7fc82e321bceec329790d686ffeb7efcff024689b7693d7b"
    "9c70eece061243b56bb6bf433f9cbb1dd7d5bd7a5b1b4144058c817ee01d9683"
    "c80a564812cad8ab958289ff078e94514b5653bdd1c0dd74c198e95a6e28331d"
    "7cd7f18c49ee344d1b2328411f93f12c8e2bc714fab40385c34ca5ef8bfdb19b"
    "7ee0218df07623866be2f94906563b318ff6859668f5179ed240d1b246ca2e68"
    "da4de41e40cf2bd047b51739272ccb15b1a1ab26d3c37598e08ed53551a47c44"
    "f6d16b4e6010b56c826a63b1859c9c10ee4eef89372f431c88fb5251066d0e40"
    "30470bd3bb534d7802e2da98a8848c89e1c2e9f71a24ddabb18f30de0d41e562";

Bytes from_hex(std::string_view hex) {
    if ((hex.size() % 2U) != 0U) {
        throw std::invalid_argument("odd-length hex");
    }
    auto nibble = [](char value) -> std::uint8_t {
        if (value >= '0' && value <= '9') {
            return static_cast<std::uint8_t>(value - '0');
        }
        if (value >= 'a' && value <= 'f') {
            return static_cast<std::uint8_t>(value - 'a' + 10);
        }
        if (value >= 'A' && value <= 'F') {
            return static_cast<std::uint8_t>(value - 'A' + 10);
        }
        throw std::invalid_argument("invalid hex");
    };

    Bytes output;
    output.reserve(hex.size() / 2U);
    for (std::size_t index = 0; index < hex.size(); index += 2U) {
        output.push_back(static_cast<std::uint8_t>(
            (nibble(hex[index]) << 4U) | nibble(hex[index + 1U])));
    }
    return output;
}

std::string to_hex(std::span<const std::uint8_t> value) {
    std::ostringstream output;
    output << std::hex << std::setfill('0');
    for (std::uint8_t byte : value) {
        output << std::setw(2) << static_cast<unsigned int>(byte);
    }
    return output.str();
}

Bytes bytes(std::string_view value) {
    return Bytes(value.begin(), value.end());
}

EventDigest digest_for(HashAlgorithm algorithm, std::string_view label) {
    return EventDigest{algorithm, netsec::compute_digest(algorithm, bytes(label))};
}

MeasurementEvent event_for(std::string_view type,
                           std::uint32_t pcr,
                           std::string_view label) {
    return MeasurementEvent{
        std::string(type),
        pcr,
        {digest_for(HashAlgorithm::Sha256, label),
         digest_for(HashAlgorithm::Sha384, label)}};
}

struct Fixture {
    MeasurementPolicy policy;
    MeasurementLog log;
};

Fixture make_fixture() {
    Fixture fixture;
    fixture.log.events = {
        event_for("firmware", 0, "firmware-2026.11"),
        event_for("bootloader", 1, "bootloader-7.4"),
        event_for("kernel", 2, "kernel-6.9.1"),
        event_for("initramfs", 3, "initramfs-2026.11"),
        event_for("userspace", 4, "userspace-2026.11"),
    };
    fixture.policy.id = "boot-chain-v1";
    fixture.policy.pcr_banks = {HashAlgorithm::Sha256, HashAlgorithm::Sha384};
    fixture.policy.expected_events = fixture.log.events;
    return fixture;
}

RsaPublicKey test_ak() {
    return RsaPublicKey{from_hex(kModulusHex), 65537U};
}

Quote make_quote(const std::vector<PcrValue>& pcrs) {
    Quote quote;
    quote.nonce = bytes("verifier-nonce-2026-11-14");
    quote.ak_id = "ak-primary";
    quote.policy_id = "boot-chain-v1";
    quote.signature_hash = HashAlgorithm::Sha256;
    quote.pcrs = pcrs;
    quote.signature = from_hex(kSignatureHex);
    return quote;
}

QuotePolicy make_quote_policy() {
    QuotePolicy policy;
    policy.expected_nonce = bytes("verifier-nonce-2026-11-14");
    policy.policy_id = "boot-chain-v1";
    policy.expected_ak_id = "ak-primary";
    policy.trusted_aks.emplace("ak-primary", test_ak());
    return policy;
}

class TestRunner {
public:
    void expect(bool condition, std::string_view name) {
        if (condition) {
            std::cout << "[PASS] " << name << '\n';
        } else {
            ++failures_;
            std::cout << "[FAIL] " << name << '\n';
        }
    }

    void expect_error(const VerificationResult& result,
                      ValidationError expected,
                      std::string_view name) {
        if (result.error == expected) {
            std::cout << "[PASS] " << name << '\n';
        } else {
            ++failures_;
            std::cout << "[FAIL] " << name << " (" << result.detail << ")\n";
        }
    }

    [[nodiscard]] int failures() const noexcept {
        return failures_;
    }

private:
    int failures_ = 0;
};

void test_hash_vectors(TestRunner& runner) {
    runner.expect(
        to_hex(netsec::sha1(bytes("abc"))) ==
            "a9993e364706816aba3e25717850c26c9cd0d89d",
        "SHA-1 test vector");
    runner.expect(
        to_hex(netsec::sha256(bytes("abc"))) ==
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
        "SHA-256 test vector");
    runner.expect(
        to_hex(netsec::sha256({})) ==
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        "SHA-256 empty input");
    const std::string_view boundary =
        "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq";
    runner.expect(
        to_hex(netsec::sha256(bytes(boundary))) ==
            "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
        "SHA-256 padding boundary");
    runner.expect(
        to_hex(netsec::sha384(bytes("abc"))) ==
            "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed"
            "8086072ba1e7cc2358baeca134c825a7",
        "SHA-384 test vector");
    runner.expect(
        to_hex(netsec::sha512(bytes("abc"))) ==
            "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a"
            "2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f",
        "SHA-512 test vector");
    runner.expect(
        to_hex(netsec::sha512(bytes(boundary))) ==
            "204a8fc6dda82f0a0ced7beb8e08a41657c16ef468b228a8279be331a703c335"
            "96fd15c13b1b07f9aa1d3bea57789ca031ad85c7a71dd70354ec631238ca3445",
        "SHA-512 padding boundary");
}

void test_chain_attacks(TestRunner& runner) {
    Fixture fixture = make_fixture();
    const VerificationResult valid =
        netsec::verify_measurement_log(fixture.log, fixture.policy);
    runner.expect(valid.ok() && valid.computed_pcrs.size() == 10U,
                  "valid log replays every PCR bank");

    MeasurementLog swapped = fixture.log;
    std::swap(swapped.events[1], swapped.events[2]);
    runner.expect_error(
        netsec::verify_measurement_log(swapped, fixture.policy),
        ValidationError::EventOrderMismatch,
        "out-of-order boot events are rejected");

    MeasurementLog missing = fixture.log;
    missing.events.erase(missing.events.begin() + 2);
    runner.expect_error(
        netsec::verify_measurement_log(missing, fixture.policy),
        ValidationError::MissingEvent,
        "a missing boot event is rejected");

    MeasurementLog replaced = fixture.log;
    replaced.events[2].digests[0].value[0] ^= 0x80U;
    runner.expect_error(
        netsec::verify_measurement_log(replaced, fixture.policy),
        ValidationError::DigestMismatch,
        "a substituted event digest is rejected");

    MeasurementLog unknown = fixture.log;
    unknown.events[2].type = "unmeasured-component";
    runner.expect_error(
        netsec::verify_measurement_log(unknown, fixture.policy),
        ValidationError::UnknownEventType,
        "an unknown event type is rejected");

    MeasurementLog extra = fixture.log;
    extra.events.push_back(event_for("userspace", 4, "injected-component"));
    runner.expect_error(
        netsec::verify_measurement_log(extra, fixture.policy),
        ValidationError::ExtraEvent,
        "an inserted unmeasured event is rejected");

    MeasurementLog wrong_bank = fixture.log;
    wrong_bank.events[3].digests[1] =
        digest_for(HashAlgorithm::Sha512, "initramfs-2026.11");
    runner.expect_error(
        netsec::verify_measurement_log(wrong_bank, fixture.policy),
        ValidationError::DigestAlgorithmMismatch,
        "a PCR bank or algorithm mismatch is rejected");

    std::vector<PcrValue> bad_pcrs = valid.computed_pcrs;
    bad_pcrs.front().value[0] ^= 0xffU;
    runner.expect_error(
        netsec::verify_measurement_log(fixture.log, fixture.policy, bad_pcrs),
        ValidationError::PcrValueMismatch,
        "a PCR value that differs from the replay is rejected");
}

void test_quotes(TestRunner& runner) {
    Fixture fixture = make_fixture();
    const VerificationResult chain =
        netsec::verify_measurement_log(fixture.log, fixture.policy);
    if (!chain.ok()) {
        runner.expect(false, "quote fixture requires a valid chain");
        return;
    }

    const Quote quote = make_quote(chain.computed_pcrs);
    const QuotePolicy policy = make_quote_policy();
    const VerificationResult valid_quote =
        netsec::verify_quote(quote, policy, chain.computed_pcrs);
    if (!valid_quote.ok()) {
        std::cout << "quote failure detail: " << valid_quote.detail << '\n';
    }
    runner.expect(valid_quote.ok(),
                  "valid quote nonce, AK, policy, PCRs, and signature");
    runner.expect(
        netsec::verify_attestation(fixture.log, fixture.policy, quote, policy).ok(),
        "complete attestation verifies");

    Quote bad_nonce = quote;
    bad_nonce.nonce = bytes("replayed-nonce");
    runner.expect_error(
        netsec::verify_attestation(fixture.log, fixture.policy, bad_nonce, policy),
        ValidationError::QuoteNonceMismatch,
        "a stale or mismatched quote nonce is rejected");

    Quote bad_ak = quote;
    bad_ak.ak_id = "ak-untrusted";
    runner.expect_error(
        netsec::verify_attestation(fixture.log, fixture.policy, bad_ak, policy),
        ValidationError::UntrustedAk,
        "an untrusted attestation key is rejected");

    Quote bad_policy = quote;
    bad_policy.policy_id = "other-policy";
    runner.expect_error(
        netsec::verify_attestation(fixture.log, fixture.policy, bad_policy, policy),
        ValidationError::QuotePolicyMismatch,
        "a quote for another policy is rejected");

    Quote bad_pcr = quote;
    bad_pcr.pcrs.front().value[0] ^= 0x01U;
    runner.expect_error(
        netsec::verify_attestation(fixture.log, fixture.policy, bad_pcr, policy),
        ValidationError::PcrValueMismatch,
        "quote PCR values must match the replayed chain");

    if (quote.signature.size() >= 2U) {
        Quote bad_signature = quote;
        bad_signature.signature.back() ^= 0x01U;
        runner.expect_error(
            netsec::verify_quote(bad_signature, policy, chain.computed_pcrs),
            ValidationError::QuoteSignatureInvalid,
            "a forged quote signature is rejected");
    }

    MeasurementLog swapped = fixture.log;
    std::swap(swapped.events[1], swapped.events[2]);
    runner.expect_error(
        netsec::verify_attestation(swapped, fixture.policy, quote, policy),
        ValidationError::EventOrderMismatch,
        "a valid quote cannot mask a reordered measurement log");
}

}  // namespace

int main() {
    TestRunner runner;
    test_hash_vectors(runner);
    test_chain_attacks(runner);
    test_quotes(runner);
    if (runner.failures() != 0) {
        std::cout << runner.failures() << " test(s) failed\n";
        return 1;
    }
    std::cout << "all tests passed\n";
    return 0;
}
