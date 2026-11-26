#include "iommu_ats/HostPagePool.hpp"

#include <algorithm>

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

} // namespace

void HostPagePool::addFrame(PhysicalAddress physicalBase, u64 size,
                            PageOwner owner) {
    PhysicalFrame frame;
    frame.physicalBase = physicalBase;
    frame.size = size;
    frame.owner = owner;
    frame.allocationGeneration = nextGeneration_++;
    frames_[physicalBase] = frame;
    if (owner.kind == PageOwnerKind::Free) {
        freeLifo_.push_back(physicalBase);
    }
}

const PhysicalFrame* HostPagePool::frame(PhysicalAddress pa) const {
    const auto it = frames_.find(pa & ~(PAGE_SIZE_4K - 1ULL));
    return it == frames_.end() ? nullptr : &it->second;
}

PhysicalFrame* HostPagePool::frame(PhysicalAddress pa) {
    const auto it = frames_.find(pa & ~(PAGE_SIZE_4K - 1ULL));
    return it == frames_.end() ? nullptr : &it->second;
}

PageOwner HostPagePool::owner(PhysicalAddress pa) const {
    const PhysicalFrame* found = frame(pa);
    return found == nullptr ? PageOwner{PageOwnerKind::Free, 0}
                            : found->owner;
}

bool HostPagePool::releaseToFreePool(PhysicalAddress pa) {
    PhysicalFrame* found = frame(pa);
    if (found == nullptr || found->owner.kind == PageOwnerKind::Free) {
        return false;
    }
    found->owner = {PageOwnerKind::Free, 0};
    freeLifo_.push_back(found->physicalBase);
    return true;
}

std::optional<PhysicalAddress> HostPagePool::allocate(PageOwner owner) {
    while (!freeLifo_.empty()) {
        const PhysicalAddress pa = freeLifo_.back();
        freeLifo_.pop_back();
        auto it = frames_.find(pa);
        if (it == frames_.end() ||
            it->second.owner.kind != PageOwnerKind::Free) {
            continue;
        }
        it->second.owner = owner;
        it->second.allocationGeneration = nextGeneration_++;
        return pa;
    }
    return std::nullopt;
}

std::optional<PhysicalAddress> HostPagePool::allocateSpecific(
    PhysicalAddress pa, PageOwner owner) {
    auto it = frames_.find(pa & ~(PAGE_SIZE_4K - 1ULL));
    if (it == frames_.end() || it->second.owner.kind != PageOwnerKind::Free) {
        return std::nullopt;
    }
    const PhysicalAddress base = it->second.physicalBase;
    freeLifo_.erase(std::remove(freeLifo_.begin(), freeLifo_.end(), base),
                   freeLifo_.end());
    it->second.owner = owner;
    it->second.allocationGeneration = nextGeneration_++;
    return base;
}

void HostPagePool::recordWrite(PhysicalAddress pa, DomainId writer,
                               Bdf bdf) {
    PhysicalFrame* found = frame(pa);
    if (found == nullptr) {
        return;
    }
    ++found->writeCount;
    found->lastWriterDomain = writer;
    found->lastWriterBdf = bdf;
}

bool HostPagePool::isOwnedByRequester(PhysicalAddress pa,
                                      DomainId domain) const {
    const PhysicalFrame* found = frame(pa);
    if (found == nullptr) {
        return false;
    }
    return found->owner.kind == PageOwnerKind::Guest &&
           found->owner.domain == domain;
}

std::string HostPagePool::canonicalState() const {
    std::string result = "pool{frames=";
    for (const auto& [pa, frame] : frames_) {
        result += "{pa=";
        appendHex(result, pa);
        result += ",owner=";
        result += std::to_string(static_cast<int>(frame.owner.kind));
        result += ",dom=";
        result += std::to_string(frame.owner.domain);
        result += ",gen=";
        result += std::to_string(frame.allocationGeneration);
        result += ",w=";
        result += std::to_string(frame.writeCount);
        result += ",lw=";
        result += std::to_string(frame.lastWriterDomain);
        result += ",";
        result += frame.lastWriterBdf.toString();
        result += "};";
    }
    result += ",free=[";
    for (const PhysicalAddress pa : freeLifo_) {
        appendHex(result, pa);
        result.push_back(',');
    }
    result += "]}";
    return result;
}

} // namespace iommu_ats
