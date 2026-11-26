#include "iommu_ats/TranslationCache.hpp"

#include <algorithm>
#include <utility>

namespace iommu_ats {
namespace {

void appendHex(std::string& out, u64 value) {
    static constexpr char digits[] = "0123456789abcdef";
    out.push_back('0');
    out.push_back('x');
    for (int shift = 60; shift >= 0; shift -= 4) {
        out.push_back(digits[(value >> static_cast<unsigned>(shift)) & 0xFU]);
    }
}

bool sameTranslationKey(const CacheEntry& a, const CacheEntry& b) {
    return a.domain == b.domain && a.pasid == b.pasid &&
           a.iovaBase == b.iovaBase && a.size == b.size;
}

} // namespace

TranslationCache::TranslationCache(std::string name)
    : name_(std::move(name)) {}

void TranslationCache::insert(CacheEntry entry) {
    for (CacheEntry& existing : entries_) {
        if (sameTranslationKey(existing, entry)) {
            existing = std::move(entry);
            return;
        }
    }
    entries_.push_back(std::move(entry));
}

std::vector<CacheHit> TranslationCache::lookup(
    DomainId domain, Pasid pasid, Iova iova, u64 length, DmaOp op) const {
    std::vector<CacheHit> hits;
    for (std::size_t index = 0; index < entries_.size(); ++index) {
        const CacheEntry& entry = entries_[index];
        if (entry.domain == domain && entry.pasid == pasid &&
            entry.overlaps(iova, length) && entry.permits(op)) {
            hits.push_back({index, entry});
        }
    }
    return hits;
}

std::vector<CacheHit> TranslationCache::lookupOverlap(
    DomainId domain, Pasid pasid, Iova iova, u64 length) const {
    std::vector<CacheHit> hits;
    for (std::size_t index = 0; index < entries_.size(); ++index) {
        const CacheEntry& entry = entries_[index];
        if (entry.domain == domain && entry.pasid == pasid &&
            entry.overlaps(iova, length)) {
            hits.push_back({index, entry});
        }
    }
    return hits;
}

std::size_t TranslationCache::invalidateDomain(DomainId domain) {
    const auto before = entries_.size();
    entries_.erase(std::remove_if(entries_.begin(), entries_.end(),
                                  [domain](const CacheEntry& entry) {
                                      return entry.domain == domain;
                                  }),
                   entries_.end());
    return before - entries_.size();
}

std::size_t TranslationCache::invalidatePasid(DomainId domain,
                                               Pasid pasid) {
    const auto before = entries_.size();
    entries_.erase(std::remove_if(entries_.begin(), entries_.end(),
                                  [domain, pasid](const CacheEntry& entry) {
                                      return entry.domain == domain &&
                                             entry.pasid == pasid;
                                  }),
                   entries_.end());
    return before - entries_.size();
}

std::size_t TranslationCache::invalidateRange(DomainId domain, Pasid pasid,
                                              Iova iovaBase, u64 size) {
    const Iova end = iovaBase + size;
    const auto before = entries_.size();
    entries_.erase(std::remove_if(entries_.begin(), entries_.end(),
                                  [&](const CacheEntry& entry) {
                                      return entry.domain == domain &&
                                             entry.pasid == pasid &&
                                             iovaBase < entry.iovaEnd() &&
                                             entry.iovaBase < end;
                                  }),
                   entries_.end());
    return before - entries_.size();
}

std::size_t TranslationCache::invalidateTicket(TicketId ticketId) {
    const auto before = entries_.size();
    entries_.erase(std::remove_if(entries_.begin(), entries_.end(),
                                  [ticketId](const CacheEntry& entry) {
                                      return entry.ticketId == ticketId;
                                  }),
                   entries_.end());
    return before - entries_.size();
}

void TranslationCache::clear() {
    entries_.clear();
}

const std::vector<CacheEntry>& TranslationCache::entries() const noexcept {
    return entries_;
}

std::size_t TranslationCache::size() const noexcept {
    return entries_.size();
}

bool TranslationCache::empty() const noexcept {
    return entries_.empty();
}

std::string TranslationCache::canonicalState() const {
    std::string result = name_ + "[";
    for (const CacheEntry& entry : entries_) {
        result += "{d=";
        result += std::to_string(entry.domain);
        result += ",p=";
        result += std::to_string(entry.pasid);
        result += ",i=";
        appendHex(result, entry.iovaBase);
        result += ",s=";
        result += std::to_string(entry.size);
        result += ",pa=";
        appendHex(result, entry.physicalBase);
        result += ",perm=";
        result.push_back(entry.permissions.read ? 'r' : '-');
        result.push_back(entry.permissions.write ? 'w' : '-');
        result.push_back(entry.permissions.execute ? 'x' : '-');
        result += ",t=";
        result += std::to_string(entry.ticketId);
        result += "};";
    }
    result += "]";
    return result;
}

} // namespace iommu_ats
