#pragma once

#include "iommu_ats/Types.hpp"

#include <map>
#include <unordered_map>
#include <vector>

namespace iommu_ats {

struct AliasGroup {
    u32 id = 0;
    std::vector<Bdf> bdrs;
};

class AliasTopology {
public:
    void addGroup(u32 id, std::vector<Bdf> bdrs);
    [[nodiscard]] const AliasGroup* groupOf(Bdf bdf) const;
    [[nodiscard]] std::vector<Bdf> aliasesOf(Bdf bdf) const;
    [[nodiscard]] bool areAliases(Bdf a, Bdf b) const;
    [[nodiscard]] std::string canonicalState() const;

private:
    std::map<u32, AliasGroup> groups_;
    std::unordered_map<Bdf, u32, BdfHash> index_;
};

class PasidDomainTable {
public:
    void bind(Bdf bdf, Pasid pasid, DomainId domain);
    void bindGroup(const AliasGroup& group, Pasid pasid, DomainId domain);
    [[nodiscard]] std::optional<DomainId> lookup(Bdf bdf,
                                                 Pasid pasid) const;
    [[nodiscard]] std::string canonicalState() const;

private:
    struct Key {
        Bdf bdf;
        Pasid pasid;

        [[nodiscard]] bool operator==(const Key& other) const noexcept {
            return bdf == other.bdf && pasid == other.pasid;
        }
    };

    struct KeyHash {
        [[nodiscard]] std::size_t operator()(const Key& key) const noexcept {
            std::size_t h1 = BdfHash{}(key.bdf);
            std::size_t h2 = std::hash<Pasid>{}(key.pasid);
            return h1 ^ (h2 + 0x9e3779b97f4a7c15ULL + (h1 << 6U) +
                         (h1 >> 2U));
        }
    };

    std::unordered_map<Key, DomainId, KeyHash> table_;
};

} // namespace iommu_ats
