#pragma once

#include <cstdint>
#include <mutex>
#include <string>
#include <unordered_set>
#include <vector>

namespace netsec {

// Trusted certificate authority entry. The key is the raw SSH public-key blob
// exactly as embedded in a certificate's signature-key field.
struct TrustedCa {
    std::string keyBlob;
    // Optional principals this CA is allowed to certify; empty means the CA may
    // certify any principal (certificate principals are still enforced).
    std::vector<std::string> allowedPrincipals;
};

// Generation-tagged trust and revocation store.
//
// Every mutation (CA rotation, revocation list change) bumps the generation so
// long-lived sessions can detect that policy moved underneath them and
// re-evaluate on the next channel open / rekey. The store is thread-safe.
class TrustStore {
public:
    TrustStore() = default;

 void addTrustedCa(TrustedCa ca);
    void removeTrustedCa(const std::string& keyBlob);
    void revoke(std::uint64_t serial);
    void unrevoke(std::uint64_t serial);

    std::uint64_t generation() const;

    // Snapshot used by a single authorization evaluation.
    struct Snapshot {
        std::uint64_t generation = 0;
        std::vector<TrustedCa> cas;
        std::unordered_set<std::uint64_t> revokedSerials;
    };

    Snapshot snapshot() const;

private:
    mutable std::mutex mutex_;
    std::vector<TrustedCa> cas_;
    std::unordered_set<std::uint64_t> revokedSerials_;
    std::uint64_t generation_ = 1;
};

}  // namespace netsec
