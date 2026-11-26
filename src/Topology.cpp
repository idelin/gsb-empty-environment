#include "iommu_ats/Topology.hpp"

namespace iommu_ats {

void AliasTopology::addGroup(u32 id, std::vector<Bdf> bdrs) {
    AliasGroup group{id, std::move(bdrs)};
    for (const Bdf bdf : group.bdrs) {
        index_[bdf] = id;
    }
    groups_[id] = std::move(group);
}

const AliasGroup* AliasTopology::groupOf(Bdf bdf) const {
    const auto found = index_.find(bdf);
    if (found == index_.end()) {
        return nullptr;
    }
    const auto group = groups_.find(found->second);
    return group == groups_.end() ? nullptr : &group->second;
}

std::vector<Bdf> AliasTopology::aliasesOf(Bdf bdf) const {
    const AliasGroup* group = groupOf(bdf);
    if (group == nullptr) {
        return {bdf};
    }
    return group->bdrs;
}

bool AliasTopology::areAliases(Bdf a, Bdf b) const {
    const AliasGroup* first = groupOf(a);
    const AliasGroup* second = groupOf(b);
    return first != nullptr && second != nullptr && first->id == second->id;
}

std::string AliasTopology::canonicalState() const {
    std::string result = "aliases{";
    for (const auto& [id, group] : groups_) {
        result += std::to_string(id);
        result += ":[";
        for (const Bdf bdf : group.bdrs) {
            result += bdf.toString();
            result.push_back(',');
        }
        result += "];";
    }
    result += "}";
    return result;
}

void PasidDomainTable::bind(Bdf bdf, Pasid pasid, DomainId domain) {
    table_[Key{bdf, pasid}] = domain;
}

void PasidDomainTable::bindGroup(const AliasGroup& group, Pasid pasid,
                                 DomainId domain) {
    for (const Bdf bdf : group.bdrs) {
        bind(bdf, pasid, domain);
    }
}

std::optional<DomainId> PasidDomainTable::lookup(Bdf bdf,
                                                 Pasid pasid) const {
    const auto found = table_.find(Key{bdf, pasid});
    if (found == table_.end()) {
        return std::nullopt;
    }
    return found->second;
}

std::string PasidDomainTable::canonicalState() const {
    std::string result = "pasidTable{";
    for (const auto& [key, domain] : table_) {
        result += key.bdf.toString();
        result.push_back(':');
        result += std::to_string(key.pasid);
        result += "=>d";
        result += std::to_string(domain);
        result.push_back(',');
    }
    result += "}";
    return result;
}

} // namespace iommu_ats
