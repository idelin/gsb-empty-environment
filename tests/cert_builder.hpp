#pragma once

#include <cstdint>
#include <string>
#include <string_view>
#include <vector>

#include "netsec/wire.hpp"

namespace netsec::testutil {

struct NamedOption {
    std::string name;
    std::string value;
};

// In-memory Ed25519 v01 certificate builder mirroring the SSH wire format.
// Test-only: signatures are fixed structural blobs, not real Ed25519.
class CertBuilder {
public:
    CertBuilder();

    CertBuilder& serial(std::uint64_t value);
    CertBuilder& userCertificate();
    CertBuilder& hostCertificate();
    CertBuilder& principal(std::string principal);
    CertBuilder& validity(std::uint64_t after, std::uint64_t before);
    CertBuilder& keyId(std::string id);
    CertBuilder& criticalOption(std::string name, std::string value = "");
    CertBuilder& extension(std::string name, std::string value = "");
    CertBuilder& caKey(std::string_view rawCaKey);
    CertBuilder& malformedSignature();

    std::string build() const;

private:
    std::uint64_t serial_ = 1;
    std::uint32_t type_ = 1;
    std::vector<std::string> principals_;
    std::uint64_t validAfter_ = 1000;
    std::uint64_t validBefore_ = 2000;
    std::string keyId_ = "test-key";
    std::vector<NamedOption> critical_;
    std::vector<NamedOption> extensions_;
    std::string caKey_;
    bool malformedSignature_ = false;
};

// Builds a raw public-key blob (type string + key bytes) usable both as a
// trust-store CA entry and as the embedded certificate CA key.
std::string makeKeyBlob(std::string_view seedName);

// Builds a structurally valid (non-cryptographic) SSH signature blob.
std::string makeSignature(std::string_view marker = "sig");

}  // namespace netsec::testutil
