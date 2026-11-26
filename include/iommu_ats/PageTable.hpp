#pragma once

#include "iommu_ats/Types.hpp"

#include <array>
#include <memory>
#include <optional>
#include <string>
#include <vector>

namespace iommu_ats {

struct TranslationSegment {
    Iova iovaBase = 0;
    Iova iovaEnd = 0;
    PhysicalAddress physicalBase = 0;
    u64 pageSize = PAGE_SIZE_4K;
    AccessPermissions permissions{};

    [[nodiscard]] constexpr bool contains(Iova address) const noexcept {
        return address >= iovaBase && address < iovaEnd;
    }

    [[nodiscard]] constexpr PhysicalAddress physicalAt(
        Iova address) const noexcept {
        return physicalBase + (address - iovaBase);
    }
};

struct RangeTranslation {
    bool mapped = false;
    bool permitted = false;
    std::vector<TranslationSegment> segments;
    std::string fault;
};

struct PageTableWalkEntry {
    int level = 4;
    std::size_t index = 0;
    PhysicalAddress tablePhysicalAddress = 0;
    PhysicalAddress referencedPhysicalAddress = 0;
    bool physicalAddressValid = false;
    bool leaf = false;
    bool largePage = false;
    u64 pageSize = 0;
    AccessPermissions permissions = AccessPermissions::none();
};

class PageTable {
public:
    static constexpr std::size_t ENTRIES_PER_LEVEL = 512;

    explicit PageTable(PhysicalAddress rootPhysicalAddress = PAGE_SIZE_4K);
    ~PageTable();

    PageTable(const PageTable& other);
    PageTable& operator=(const PageTable& other);
    PageTable(PageTable&& other) noexcept;
    PageTable& operator=(PageTable&& other) noexcept;

    [[nodiscard]] PhysicalAddress rootPhysicalAddress() const noexcept;
    [[nodiscard]] bool rootPhysicalAddressValid() const noexcept;

    bool map(Iova iova, PhysicalAddress pa, u64 size,
             AccessPermissions permissions, std::string* error = nullptr);
    bool unmap(Iova iova, u64 size, std::string* error = nullptr);
    std::optional<u64> unmapLeaf(Iova iova, std::string* error = nullptr);

    [[nodiscard]] std::optional<TranslationSegment> translate(
        Iova iova) const;
    [[nodiscard]] std::vector<PageTableWalkEntry> walk(Iova iova) const;
    [[nodiscard]] RangeTranslation translateRange(
        Iova iova, u64 length, DmaOp op) const;
    [[nodiscard]] RangeTranslation translateRangeForPermissions(
        Iova iova, u64 length, AccessPermissions required) const;

    [[nodiscard]] std::string canonicalState() const;

private:
    struct Node;

    struct Entry {
        PhysicalAddress physicalAddress = 0;
        bool valid = false;
        bool largePage = false;
        u64 pageSize = 0;
        AccessPermissions permissions = AccessPermissions::none();
        std::unique_ptr<Node> next;
    };

    struct Node {
        PhysicalAddress physicalAddress = 0;
        std::array<Entry, ENTRIES_PER_LEVEL> entries{};
    };

    std::unique_ptr<Node> root_;
    PhysicalAddress nextTablePhysicalAddress_;

    [[nodiscard]] static std::array<std::size_t, 4> indexes(Iova iova);
    [[nodiscard]] static bool supportedSize(u64 size) noexcept;
    [[nodiscard]] static u64 baseMaskForSize(u64 size) noexcept;
    Entry* walkToLeaf(Iova iova, u64 size, bool create,
                      std::string* error);
    void canonicalNode(const Node& node, std::string& out) const;
    [[nodiscard]] static std::unique_ptr<Node> cloneNode(const Node& node);
};

} // namespace iommu_ats
