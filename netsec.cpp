#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <bit>
#include <chrono>
#include <cmath>
#include <random>
#include <sstream>
#include <stdexcept>
#include <utility>

namespace netsec {
namespace {

constexpr std::array<std::uint32_t, 64> kSha256Constants = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
    0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
    0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
    0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
    0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
    0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
    0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
    0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

constexpr std::array<std::uint32_t, 8> kSha256InitialState = {
    0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
    0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};

std::array<std::uint32_t, 8> sha256State(std::string_view input) {
  std::vector<std::uint8_t> message(input.begin(), input.end());
  const std::uint64_t bit_length =
      static_cast<std::uint64_t>(message.size()) * 8U;
  message.push_back(0x80U);
  while ((message.size() % 64U) != 56U) {
    message.push_back(0U);
  }
  for (int shift = 56; shift >= 0; shift -= 8) {
    message.push_back(
        static_cast<std::uint8_t>((bit_length >> shift) & 0xffU));
  }

  std::array<std::uint32_t, 8> state = kSha256InitialState;
  std::array<std::uint32_t, 64> words{};

  for (std::size_t offset = 0; offset < message.size(); offset += 64U) {
    for (std::size_t index = 0; index < 16U; ++index) {
      const std::size_t base = offset + index * 4U;
      words[index] = (static_cast<std::uint32_t>(message[base]) << 24U) |
                     (static_cast<std::uint32_t>(message[base + 1U]) << 16U) |
                     (static_cast<std::uint32_t>(message[base + 2U]) << 8U) |
                     static_cast<std::uint32_t>(message[base + 3U]);
    }
    for (std::size_t index = 16U; index < 64U; ++index) {
      const std::uint32_t s0 =
          std::rotr(words[index - 15U], 7U) ^
          std::rotr(words[index - 15U], 18U) ^
          (words[index - 15U] >> 3U);
      const std::uint32_t s1 =
          std::rotr(words[index - 2U], 17U) ^
          std::rotr(words[index - 2U], 19U) ^
          (words[index - 2U] >> 10U);
      words[index] = words[index - 16U] + s0 + words[index - 7U] + s1;
    }

    std::uint32_t a = state[0];
    std::uint32_t b = state[1];
    std::uint32_t c = state[2];
    std::uint32_t d = state[3];
    std::uint32_t e = state[4];
    std::uint32_t f = state[5];
    std::uint32_t g = state[6];
    std::uint32_t h = state[7];

    for (std::size_t index = 0; index < 64U; ++index) {
      const std::uint32_t s1 =
          std::rotr(e, 6U) ^ std::rotr(e, 11U) ^ std::rotr(e, 25U);
      const std::uint32_t choose = (e & f) ^ (~e & g);
      const std::uint32_t temp1 =
          h + s1 + choose + kSha256Constants[index] + words[index];
      const std::uint32_t s0 =
          std::rotr(a, 2U) ^ std::rotr(a, 13U) ^ std::rotr(a, 22U);
      const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
      const std::uint32_t temp2 = s0 + majority;

      h = g;
      g = f;
      f = e;
      e = d + temp1;
      d = c;
      c = b;
      b = a;
      a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
  }
  return state;
}

std::string bytesToHex(std::string_view bytes) {
  static constexpr char kHexDigits[] = "0123456789abcdef";
  std::string output;
  output.reserve(bytes.size() * 2U);
  for (const char byte : bytes) {
    const auto value = static_cast<unsigned char>(byte);
    output.push_back(kHexDigits[value >> 4U]);
    output.push_back(kHexDigits[value & 0x0fU]);
  }
  return output;
}

std::string digestBytes(const std::array<std::uint32_t, 8>& state) {
  std::string bytes;
  bytes.reserve(32U);
  for (const std::uint32_t word : state) {
    for (int shift = 24; shift >= 0; shift -= 8) {
      bytes.push_back(
          static_cast<char>((word >> static_cast<unsigned>(shift)) & 0xffU));
    }
  }
  return bytes;
}

std::string randomHex(std::size_t byte_count) {
  std::random_device device;
  std::string bytes;
  bytes.reserve(byte_count);
  std::uint32_t pool = 0U;
  for (std::size_t index = 0; index < byte_count; ++index) {
    if ((index % 4U) == 0U) {
      pool = device();
    }
    const unsigned shift = 24U - static_cast<unsigned>(index % 4U) * 8U;
    bytes.push_back(static_cast<char>((pool >> shift) & 0xffU));
  }
  return bytesToHex(bytes);
}

std::uint64_t currentTimeSeconds() {
  return static_cast<std::uint64_t>(
      std::chrono::duration_cast<std::chrono::seconds>(
          std::chrono::system_clock::now().time_since_epoch())
          .count());
}

void appendLengthPrefixed(std::string& output, std::string_view value) {
  output.append(std::to_string(value.size()));
  output.push_back(':');
  output.append(value);
  output.push_back('|');
}

std::string certificateCanonicalForm(const ChipCertificate& certificate) {
  std::string output;
  appendLengthPrefixed(output, certificate.version);
  appendLengthPrefixed(output, certificate.serial);
  appendLengthPrefixed(output, certificate.chip_id);
  appendLengthPrefixed(output, certificate.model);
  appendLengthPrefixed(output, certificate.physical_fingerprint_commitment);
  appendLengthPrefixed(output, certificate.challenge_key_commitment);
  appendLengthPrefixed(output, certificate.issuer);
  appendLengthPrefixed(output, std::to_string(certificate.issued_at));
  return output;
}

std::string supplyChainCanonicalForm(const SupplyChainStep& step) {
  std::string output;
  appendLengthPrefixed(output, step.chip_id);
  appendLengthPrefixed(output, std::to_string(step.sequence));
  appendLengthPrefixed(output, step.stage);
  appendLengthPrefixed(output, step.actor);
  appendLengthPrefixed(output, step.location);
  appendLengthPrefixed(output, std::to_string(step.timestamp_unix));
  appendLengthPrefixed(output, step.metadata);
  appendLengthPrefixed(output, step.previous_digest);
  return output;
}

std::string sealSupplyChainStep(std::string_view signing_key,
                                const SupplyChainStep& step) {
  return hmacSha256Hex(signing_key,
                       "SUPPLY-CHAIN/1|" + supplyChainCanonicalForm(step));
}

std::string challengeResponse(std::string_view challenge_key,
                              const ChipCertificate& certificate,
                              std::string_view challenge_hex) {
  std::string message = "CHALLENGE-RESPONSE/1|";
  appendLengthPrefixed(message, certificate.chip_id);
  appendLengthPrefixed(message, certificate.model);
  appendLengthPrefixed(message, certificate.physical_fingerprint_commitment);
  appendLengthPrefixed(message, challenge_hex);
  return hmacSha256Hex(challenge_key, message);
}

std::string performanceProbeOutput(std::string_view challenge_key,
                                   const ChipCertificate& certificate,
                                   std::string_view challenge_hex) {
  std::string message = "PERFORMANCE-PROBE/1|";
  appendLengthPrefixed(message, certificate.chip_id);
  appendLengthPrefixed(message, certificate.model);
  appendLengthPrefixed(message, challenge_hex);
  return hmacSha256Hex(challenge_key, message);
}

bool hasNewline(std::string_view value) {
  return value.find('\n') != std::string_view::npos ||
         value.find('\r') != std::string_view::npos;
}

bool validHexDigest(std::string_view value) {
  if (value.size() != 64U) {
    return false;
  }
  return std::all_of(value.begin(), value.end(), [](char character) {
    return (character >= '0' && character <= '9') ||
           (character >= 'a' && character <= 'f') ||
           (character >= 'A' && character <= 'F');
  });
}

bool sameCertificate(const ChipCertificate& lhs,
                     const ChipCertificate& rhs) {
  return lhs.version == rhs.version && lhs.serial == rhs.serial &&
         lhs.chip_id == rhs.chip_id && lhs.model == rhs.model &&
         lhs.physical_fingerprint_commitment ==
             rhs.physical_fingerprint_commitment &&
         lhs.challenge_key_commitment == rhs.challenge_key_commitment &&
         lhs.issuer == rhs.issuer && lhs.issued_at == rhs.issued_at &&
         constantTimeEqual(lhs.signature, rhs.signature);
}

AuthenticationResult reject(RejectionCode code, std::string detail) {
  AuthenticationResult result;
  result.rejection = code;
  result.detail = std::move(detail);
  return result;
}

}  // namespace

std::string sha256Hex(std::string_view input) {
  return bytesToHex(digestBytes(sha256State(input)));
}

std::string hmacSha256Hex(std::string_view key, std::string_view input) {
  constexpr std::size_t kBlockSize = 64U;
  std::string normalized_key(key);
  if (normalized_key.size() > kBlockSize) {
    normalized_key = digestBytes(sha256State(normalized_key));
  }
  normalized_key.resize(kBlockSize, '\0');

  std::string inner_pad(kBlockSize, '\0');
  std::string outer_pad(kBlockSize, '\0');
  for (std::size_t index = 0; index < kBlockSize; ++index) {
    inner_pad[index] = static_cast<char>(
        static_cast<unsigned char>(normalized_key[index]) ^ 0x36U);
    outer_pad[index] = static_cast<char>(
        static_cast<unsigned char>(normalized_key[index]) ^ 0x5cU);
  }

  const std::string inner_digest =
      digestBytes(sha256State(inner_pad + std::string(input)));
  return bytesToHex(digestBytes(sha256State(outer_pad + inner_digest)));
}

bool constantTimeEqual(std::string_view lhs, std::string_view rhs) {
  if (lhs.size() != rhs.size()) {
    return false;
  }
  unsigned char difference = 0U;
  for (std::size_t index = 0; index < lhs.size(); ++index) {
    difference |= static_cast<unsigned char>(lhs[index]) ^
                  static_cast<unsigned char>(rhs[index]);
  }
  return difference == 0U;
}

ChipCertificateAuthority::ChipCertificateAuthority(std::string issuer,
                                                   std::string signing_key)
    : issuer_(std::move(issuer)), signing_key_(std::move(signing_key)) {
  if (issuer_.empty() || signing_key_.empty()) {
    throw std::invalid_argument(
        "certificate authority issuer and signing key must not be empty");
  }
  if (hasNewline(issuer_)) {
    throw std::invalid_argument("certificate authority issuer is malformed");
  }
}

ChipCertificate ChipCertificateAuthority::issue(
    const ChipDescriptor& descriptor, std::string_view challenge_key,
    std::uint64_t issued_at) const {
  if (descriptor.chip_id.empty() || descriptor.model.empty() ||
      descriptor.physical_fingerprint.empty() || challenge_key.empty()) {
    throw std::invalid_argument(
        "chip descriptor and challenge key must not be empty");
  }
  if (hasNewline(descriptor.chip_id) || hasNewline(descriptor.model) ||
      hasNewline(descriptor.physical_fingerprint)) {
    throw std::invalid_argument("chip descriptor contains invalid characters");
  }

  ChipCertificate certificate;
  certificate.chip_id = descriptor.chip_id;
  certificate.model = descriptor.model;
  certificate.physical_fingerprint_commitment =
      sha256Hex(descriptor.physical_fingerprint);
  certificate.challenge_key_commitment = sha256Hex(challenge_key);
  certificate.issuer = issuer_;
  certificate.issued_at = issued_at;
  certificate.serial =
      sha256Hex("SERIAL/1|" + issuer_ + "|" + descriptor.chip_id + "|" +
                descriptor.model + "|" +
                std::to_string(certificate.issued_at))
          .substr(0, 32U);
  certificate.signature = hmacSha256Hex(
      signing_key_, "CERTIFICATE/1|" + certificateCanonicalForm(certificate));
  return certificate;
}

bool ChipCertificateAuthority::verify(
    const ChipCertificate& certificate) const {
  if (certificate.version != "CHIP-ATTESTATION/1" ||
      certificate.serial.empty() || certificate.chip_id.empty() ||
      certificate.model.empty() || certificate.issuer != issuer_ ||
      !validHexDigest(certificate.physical_fingerprint_commitment) ||
      !validHexDigest(certificate.challenge_key_commitment) ||
      certificate.signature.empty()) {
    return false;
  }
  if (hasNewline(certificate.chip_id) || hasNewline(certificate.model) ||
      hasNewline(certificate.serial) || hasNewline(certificate.issuer)) {
    return false;
  }

  const std::string expected = hmacSha256Hex(
      signing_key_, "CERTIFICATE/1|" + certificateCanonicalForm(certificate));
  return constantTimeEqual(expected, certificate.signature);
}

const std::string& ChipCertificateAuthority::issuer() const { return issuer_; }

SupplyChainTraceBuilder::SupplyChainTraceBuilder(std::string signing_key,
                                                 std::string chip_id)
    : signing_key_(std::move(signing_key)), chip_id_(std::move(chip_id)) {
  if (signing_key_.empty() || chip_id_.empty()) {
    throw std::invalid_argument(
        "supply chain signing key and chip id must not be empty");
  }
}

SupplyChainStep SupplyChainTraceBuilder::append(
    std::string stage, std::string actor, std::string location,
    std::uint64_t timestamp_unix, std::string metadata) {
  if (stage.empty() || actor.empty()) {
    throw std::invalid_argument(
        "supply chain stage and actor must not be empty");
  }

  SupplyChainStep step;
  step.chip_id = chip_id_;
  step.sequence = steps_.size();
  step.stage = std::move(stage);
  step.actor = std::move(actor);
  step.location = std::move(location);
  step.timestamp_unix = timestamp_unix;
  step.metadata = std::move(metadata);
  step.previous_digest =
      steps_.empty() ? std::string(64U, '0') : steps_.back().digest;
  step.digest = sealSupplyChainStep(signing_key_, step);
  steps_.push_back(step);
  return step;
}

const std::vector<SupplyChainStep>& SupplyChainTraceBuilder::steps() const {
  return steps_;
}

std::string SupplyChainTraceBuilder::headDigest() const {
  return steps_.empty() ? std::string{} : steps_.back().digest;
}

bool SupplyChainTraceBuilder::verify(std::string* error) const {
  return verifySupplyChain(steps_, signing_key_, chip_id_, error);
}

bool verifySupplyChain(const std::vector<SupplyChainStep>& steps,
                       std::string_view signing_key,
                       std::string_view expected_chip_id,
                       std::string* error) {
  auto fail = [error](std::string message) {
    if (error != nullptr) {
      *error = std::move(message);
    }
    return false;
  };

  if (signing_key.empty() || expected_chip_id.empty()) {
    return fail("supply chain verification key and chip id are required");
  }
  if (steps.empty()) {
    return fail("supply chain trace is empty");
  }

  for (std::size_t index = 0; index < steps.size(); ++index) {
    const SupplyChainStep& step = steps[index];
    if (step.chip_id != expected_chip_id) {
      return fail("supply chain step is bound to a different chip");
    }
    if (step.sequence != index) {
      return fail("supply chain sequence is not contiguous");
    }
    if (step.stage.empty() || step.actor.empty()) {
      return fail("supply chain step is missing stage or actor");
    }
    const std::string expected_previous =
        index == 0U ? std::string(64U, '0') : steps[index - 1U].digest;
    if (step.previous_digest != expected_previous) {
      return fail("supply chain previous digest does not match");
    }
    if (!validHexDigest(step.digest)) {
      return fail("supply chain digest is malformed");
    }
    const std::string expected_digest =
        sealSupplyChainStep(signing_key, step);
    if (!constantTimeEqual(expected_digest, step.digest)) {
      return fail("supply chain digest signature is invalid");
    }
  }
  return true;
}

ChipRegistry::ChipRegistry(ChipCertificateAuthority authority)
    : authority_(std::move(authority)) {}

RegistrationResult ChipRegistry::registerChip(
    ChipRegistration registration) {
  if (!authority_.verify(registration.certificate)) {
    return {false,
            "chip certificate is missing or not signed by the trust anchor"};
  }
  if (registration.certificate.chip_id.empty() ||
      registration.certificate.model.empty() ||
      registration.challenge_key.empty()) {
    return {false, "chip registration is incomplete"};
  }
  if (registration.certificate.challenge_key_commitment !=
      sha256Hex(registration.challenge_key)) {
    return {false, "challenge key does not match the certificate commitment"};
  }
  if (registration.performance_profile.nominal_cycles <= 0.0 ||
      !std::isfinite(registration.performance_profile.nominal_cycles) ||
      registration.performance_profile.tolerance_percent <= 0.0 ||
      !std::isfinite(registration.performance_profile.tolerance_percent) ||
      registration.performance_profile.minimum_samples == 0U) {
    return {false, "performance profile is invalid"};
  }

  std::string trace_error;
  if (!netsec::verifySupplyChain(registration.supply_chain,
                                 registration.challenge_key,
                                 registration.certificate.chip_id,
                                 &trace_error)) {
    return {false,
            "supply chain trace is not trustworthy: " + trace_error};
  }
  if (std::any_of(chips_.begin(), chips_.end(), [&](const StoredChip& chip) {
        return chip.registration.certificate.chip_id ==
               registration.certificate.chip_id;
      })) {
    return {false, "chip id is already registered"};
  }

  chips_.push_back(StoredChip{std::move(registration)});
  return {true, "chip registration accepted"};
}

bool ChipRegistry::isRegistered(std::string_view chip_id) const {
  return std::any_of(chips_.begin(), chips_.end(), [&](const StoredChip& chip) {
    return chip.registration.certificate.chip_id == chip_id;
  });
}

std::size_t ChipRegistry::size() const { return chips_.size(); }

bool ChipRegistry::verifySupplyChain(std::string_view chip_id,
                                     std::string* error) const {
  const auto iterator =
      std::find_if(chips_.begin(), chips_.end(), [&](const StoredChip& chip) {
        return chip.registration.certificate.chip_id == chip_id;
      });
  if (iterator == chips_.end()) {
    if (error != nullptr) {
      *error = "chip is not registered";
    }
    return false;
  }
  return netsec::verifySupplyChain(iterator->registration.supply_chain,
                                   iterator->registration.challenge_key,
                                   chip_id, error);
}

std::optional<std::string> ChipRegistry::supplyChainHead(
    std::string_view chip_id) const {
  const auto iterator =
      std::find_if(chips_.begin(), chips_.end(), [&](const StoredChip& chip) {
        return chip.registration.certificate.chip_id == chip_id;
      });
  if (iterator == chips_.end() ||
      iterator->registration.supply_chain.empty()) {
    return std::nullopt;
  }
  return iterator->registration.supply_chain.back().digest;
}

std::optional<std::vector<SupplyChainStep>> ChipRegistry::supplyChain(
    std::string_view chip_id) const {
  const auto iterator =
      std::find_if(chips_.begin(), chips_.end(), [&](const StoredChip& chip) {
        return chip.registration.certificate.chip_id == chip_id;
      });
  if (iterator == chips_.end()) {
    return std::nullopt;
  }
  return iterator->registration.supply_chain;
}

std::optional<ChipCertificate> ChipRegistry::registeredCertificate(
    std::string_view chip_id) const {
  const auto iterator =
      std::find_if(chips_.begin(), chips_.end(), [&](const StoredChip& chip) {
        return chip.registration.certificate.chip_id == chip_id;
      });
  if (iterator == chips_.end()) {
    return std::nullopt;
  }
  return iterator->registration.certificate;
}

std::string_view rejectionCodeName(RejectionCode code) {
  switch (code) {
    case RejectionCode::None:
      return "none";
    case RejectionCode::UnregisteredChip:
      return "unregistered_chip";
    case RejectionCode::ModelMismatch:
      return "model_mismatch";
    case RejectionCode::CertificateMissing:
      return "certificate_missing";
    case RejectionCode::CertificateInvalid:
      return "certificate_invalid";
    case RejectionCode::CertificateBindingMismatch:
      return "certificate_binding_mismatch";
    case RejectionCode::PhysicalFingerprintMismatch:
      return "physical_fingerprint_mismatch";
    case RejectionCode::ChallengeRejected:
      return "challenge_rejected";
    case RejectionCode::PerformanceAnomaly:
      return "performance_anomaly";
    case RejectionCode::SupplyChainUntrusted:
      return "supply_chain_untrusted";
    case RejectionCode::MalformedResponse:
      return "malformed_response";
    case RejectionCode::EndpointFailure:
      return "endpoint_failure";
  }
  return "unknown";
}

ChipAuthenticator::ChipAuthenticator(const ChipRegistry& registry,
                                     AuthenticationOptions options)
    : registry_(registry), options_(options) {
  if (options_.challenge_bytes < 16U || options_.challenge_bytes > 64U) {
    throw std::invalid_argument(
        "challenge must contain between 16 and 64 random bytes");
  }
}

AuthenticationResult ChipAuthenticator::authenticate(ChipEndpoint& endpoint) {
  const std::string challenge = randomHex(options_.challenge_bytes);
  ChipObservation observation;
  try {
    observation.descriptor = endpoint.identify();
  } catch (const std::exception& exception) {
    AuthenticationResult result =
        reject(RejectionCode::EndpointFailure,
               std::string("chip endpoint failed: ") + exception.what());
    result.challenge_hex = challenge;
    appendAudit(result, challenge, {});
    return result;
  } catch (...) {
    AuthenticationResult result =
        reject(RejectionCode::EndpointFailure, "chip endpoint failed");
    result.challenge_hex = challenge;
    appendAudit(result, challenge, {});
    return result;
  }

  if (observation.descriptor.chip_id.empty() ||
      observation.descriptor.model.empty()) {
    AuthenticationResult result =
        reject(RejectionCode::MalformedResponse,
               "chip descriptor is incomplete");
    result.challenge_hex = challenge;
    appendAudit(result, challenge, {});
    return result;
  }
  if (!registry_.isRegistered(observation.descriptor.chip_id)) {
    AuthenticationResult result =
        reject(RejectionCode::UnregisteredChip,
               "chip id is not present in the trusted registry");
    result.chip_id = observation.descriptor.chip_id;
    result.challenge_hex = challenge;
    appendAudit(result, challenge, {});
    return result;
  }

  try {
    observation.certificate = endpoint.certificate();
    if (!observation.certificate.has_value()) {
      AuthenticationResult result =
          reject(RejectionCode::CertificateMissing,
                 "chip did not provide an attestation certificate");
      result.chip_id = observation.descriptor.chip_id;
      result.challenge_hex = challenge;
      appendAudit(result, challenge, {});
      return result;
    }
    observation.challenge_response_hex =
        endpoint.respondToChallenge(challenge);
    observation.performance = endpoint.runPerformanceProbe(challenge);
  } catch (const std::exception& exception) {
    AuthenticationResult result =
        reject(RejectionCode::EndpointFailure,
               std::string("chip endpoint failed: ") + exception.what());
    result.chip_id = observation.descriptor.chip_id;
    result.challenge_hex = challenge;
    appendAudit(result, challenge, {});
    return result;
  } catch (...) {
    AuthenticationResult result =
        reject(RejectionCode::EndpointFailure, "chip endpoint failed");
    result.chip_id = observation.descriptor.chip_id;
    result.challenge_hex = challenge;
    appendAudit(result, challenge, {});
    return result;
  }

  const std::string observed_chip_id = observation.descriptor.chip_id;
  AuthenticationResult result =
      authenticateObservation(std::move(observation), challenge);
  if (result.chip_id.empty()) {
    result.chip_id = observed_chip_id;
  }
  result.challenge_hex = challenge;
  appendAudit(result, challenge, result.trace_head);
  return result;
}

AuthenticationResult ChipAuthenticator::authenticateObservation(
    ChipObservation observation, std::string challenge_hex) {
  if (observation.descriptor.chip_id.empty() ||
      observation.descriptor.model.empty()) {
    return reject(RejectionCode::MalformedResponse,
                  "chip descriptor is incomplete");
  }
  if (!registry_.isRegistered(observation.descriptor.chip_id)) {
    return reject(RejectionCode::UnregisteredChip,
                  "chip id is not present in the trusted registry");
  }
  if (!observation.certificate.has_value()) {
    return reject(RejectionCode::CertificateMissing,
                  "chip did not provide an attestation certificate");
  }

  const ChipRegistry::StoredChip* stored = nullptr;
  for (const ChipRegistry::StoredChip& chip : registry_.chips_) {
    if (chip.registration.certificate.chip_id ==
        observation.descriptor.chip_id) {
      stored = &chip;
      break;
    }
  }
  if (stored == nullptr) {
    return reject(RejectionCode::UnregisteredChip,
                  "chip registration disappeared during authentication");
  }

  const ChipCertificate& certificate = *observation.certificate;
  if (certificate.chip_id != observation.descriptor.chip_id) {
    return reject(
        RejectionCode::CertificateBindingMismatch,
        "certificate chip id is not bound to the observed chip");
  }
  if (certificate.model != observation.descriptor.model) {
    return reject(RejectionCode::ModelMismatch,
                  "observed model does not match the certificate");
  }
  if (!registry_.authority_.verify(certificate)) {
    return reject(RejectionCode::CertificateInvalid,
                  "certificate signature or issuer is invalid");
  }
  if (!sameCertificate(certificate, stored->registration.certificate)) {
    return reject(RejectionCode::CertificateBindingMismatch,
                  "presented certificate does not match the registered certificate");
  }

  const std::string physical_commitment =
      sha256Hex(observation.descriptor.physical_fingerprint);
  if (!constantTimeEqual(physical_commitment,
                         certificate.physical_fingerprint_commitment)) {
    return reject(
        RejectionCode::PhysicalFingerprintMismatch,
        "physical fingerprint does not match the certificate binding");
  }

  if (!validHexDigest(observation.performance.output_hex)) {
    return reject(RejectionCode::MalformedResponse,
                  "performance probe output is malformed or absent");
  }
  if (!validHexDigest(observation.challenge_response_hex)) {
    return reject(RejectionCode::MalformedResponse,
                  "challenge response is malformed or absent");
  }

  if (options_.require_supply_chain) {
    std::string trace_error;
    if (!verifySupplyChain(stored->registration.supply_chain,
                           stored->registration.challenge_key,
                           certificate.chip_id, &trace_error)) {
      return reject(RejectionCode::SupplyChainUntrusted,
                    "supply chain trace failed verification: " + trace_error);
    }
  }

  const std::string expected_response =
      challengeResponse(stored->registration.challenge_key, certificate,
                        challenge_hex);
  if (!constantTimeEqual(expected_response,
                         observation.challenge_response_hex)) {
    return reject(RejectionCode::ChallengeRejected,
                  "challenge response did not verify");
  }

  if (options_.require_probe_behavior) {
    const std::string expected_probe =
        performanceProbeOutput(stored->registration.challenge_key, certificate,
                               challenge_hex);
    if (!constantTimeEqual(expected_probe,
                           observation.performance.output_hex)) {
      return reject(RejectionCode::ChallengeRejected,
                    "performance probe behavior did not verify");
    }
  }

  if (options_.require_performance_fingerprint) {
    const PerformanceProfile& profile =
        stored->registration.performance_profile;
    if (observation.performance.samples < profile.minimum_samples ||
        !std::isfinite(observation.performance.cycles) ||
        observation.performance.cycles <= 0.0) {
      return reject(RejectionCode::PerformanceAnomaly,
                    "performance probe did not produce enough valid samples");
    }
    const double deviation =
        std::abs(observation.performance.cycles - profile.nominal_cycles) /
        profile.nominal_cycles * 100.0;
    if (deviation > profile.tolerance_percent) {
      std::ostringstream message;
      message << "performance fingerprint deviates by " << deviation
              << "%, allowed " << profile.tolerance_percent << "%";
      return reject(RejectionCode::PerformanceAnomaly, message.str());
    }
  }

  AuthenticationResult result;
  result.trusted = true;
  result.rejection = RejectionCode::None;
  result.detail =
      "chip identity, certificate and physical behavior are trusted";
  result.chip_id = certificate.chip_id;
  result.challenge_hex = challenge_hex;
  if (!stored->registration.supply_chain.empty()) {
    result.trace_head = stored->registration.supply_chain.back().digest;
  }
  return result;
}

void ChipAuthenticator::appendAudit(AuthenticationResult& result,
                                    std::string_view challenge,
                                    std::string_view trace_head) {
  AuditRecord record;
  record.sequence = audit_.size();
  record.timestamp_unix = currentTimeSeconds();
  record.chip_id = result.chip_id;
  record.trusted = result.trusted;
  record.rejection = result.rejection;
  record.challenge_digest = sha256Hex(challenge);
  record.trace_head = std::string(trace_head);
  record.previous_digest =
      audit_.empty() ? std::string(64U, '0') : audit_.back().digest;

  std::string canonical;
  appendLengthPrefixed(canonical, std::to_string(record.sequence));
  appendLengthPrefixed(canonical, std::to_string(record.timestamp_unix));
  appendLengthPrefixed(canonical, record.chip_id);
  appendLengthPrefixed(canonical, record.trusted ? "trusted" : "rejected");
  appendLengthPrefixed(canonical, rejectionCodeName(record.rejection));
  appendLengthPrefixed(canonical, record.challenge_digest);
  appendLengthPrefixed(canonical, record.trace_head);
  appendLengthPrefixed(canonical, record.previous_digest);
  record.digest = sha256Hex("AUDIT/1|" + canonical);

  result.audit_sequence = record.sequence;
  audit_.push_back(std::move(record));
}

const std::vector<AuditRecord>& ChipAuthenticator::auditTrail() const {
  return audit_;
}

bool ChipAuthenticator::verifyAuditTrail(std::string* error) const {
  for (std::size_t index = 0; index < audit_.size(); ++index) {
    const AuditRecord& record = audit_[index];
    if (record.sequence != index) {
      if (error != nullptr) {
        *error = "audit sequence is not contiguous";
      }
      return false;
    }
    const std::string expected_previous =
        index == 0U ? std::string(64U, '0') : audit_[index - 1U].digest;
    if (record.previous_digest != expected_previous) {
      if (error != nullptr) {
        *error = "audit previous digest does not match";
      }
      return false;
    }

    std::string canonical;
    appendLengthPrefixed(canonical, std::to_string(record.sequence));
    appendLengthPrefixed(canonical, std::to_string(record.timestamp_unix));
    appendLengthPrefixed(canonical, record.chip_id);
    appendLengthPrefixed(canonical, record.trusted ? "trusted" : "rejected");
    appendLengthPrefixed(canonical, rejectionCodeName(record.rejection));
    appendLengthPrefixed(canonical, record.challenge_digest);
    appendLengthPrefixed(canonical, record.trace_head);
    appendLengthPrefixed(canonical, record.previous_digest);
    if (!constantTimeEqual(record.digest, sha256Hex("AUDIT/1|" + canonical))) {
      if (error != nullptr) {
        *error = "audit digest is invalid";
      }
      return false;
    }
  }
  return true;
}

}  // namespace netsec
