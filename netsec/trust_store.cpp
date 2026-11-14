#include "netsec/trust_store.hpp"

#include <utility>

namespace netsec {

void TrustStore::addTrustedCa(TrustedCa ca) {
    std::lock_guard lock(mutex_);
    for (const auto& existing : cas_) {
        if (existing.keyBlob == ca.keyBlob) {
            return;
        }
    }
    cas_.push_back(std::move(ca));
    ++generation_;
}

void TrustStore::removeTrustedCa(const std::string& keyBlob) {
    std::lock_guard lock(mutex_);
    std::size_t write = 0;
    bool removed = false;
    for (std::size_t read = 0; read < cas_.size(); ++read) {
        if (cas_[read].keyBlob == keyBlob) {
            removed = true;
            continue;
        }
        cas_[write++] = std::move(cas_[read]);
    }
    cas_.resize(write);
    if (removed) {
        ++generation_;
    }
}

void TrustStore::revoke(std::uint64_t serial) {
    std::lock_guard lock(mutex_);
    if (revokedSerials_.insert(serial).second) {
        ++generation_;
    }
}

void TrustStore::unrevoke(std::uint64_t serial) {
    std::lock_guard lock(mutex_);
    if (revokedSerials_.erase(serial) > 0) {
        ++generation_;
    }
}

std::uint64_t TrustStore::generation() const {
    std::lock_guard lock(mutex_);
    return generation_;
}

TrustStore::Snapshot TrustStore::snapshot() const {
    std::lock_guard lock(mutex_);
    return Snapshot{generation_, cas_, revokedSerials_};
}

}  // namespace netsec
