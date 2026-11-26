#include "iommu_ats/PageTable.hpp"

#include <algorithm>
#include <limits>
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

bool overflowingAdd(u64 a, u64 b, u64* result) {
    if (a > std::numeric_limits<u64>::max() - b) {
        return true;
    }
    *result = a + b;
    return false;
}

} // namespace

PageTable::PageTable(PhysicalAddress rootPhysicalAddress)
    : root_(std::make_unique<Node>()),
      nextTablePhysicalAddress_(rootPhysicalAddress + PAGE_SIZE_4K) {
    root_->physicalAddress = rootPhysicalAddress;
}

PageTable::~PageTable() = default;

std::unique_ptr<PageTable::Node> PageTable::cloneNode(const Node& node) {
    auto copy = std::make_unique<Node>();
    copy->physicalAddress = node.physicalAddress;
    for (std::size_t i = 0; i < ENTRIES_PER_LEVEL; ++i) {
        const Entry& source = node.entries[i];
        Entry& target = copy->entries[i];
        target.physicalAddress = source.physicalAddress;
        target.valid = source.valid;
        target.largePage = source.largePage;
        target.pageSize = source.pageSize;
        target.permissions = source.permissions;
        if (source.next) {
            target.next = cloneNode(*source.next);
        }
    }
    return copy;
}

PageTable::PageTable(const PageTable& other)
    : root_(other.root_ ? cloneNode(*other.root_) : std::make_unique<Node>()),
      nextTablePhysicalAddress_(other.nextTablePhysicalAddress_) {}

PageTable& PageTable::operator=(const PageTable& other) {
    if (this != &other) {
        PageTable copy(other);
        *this = std::move(copy);
    }
    return *this;
}

PageTable::PageTable(PageTable&&) noexcept = default;
PageTable& PageTable::operator=(PageTable&&) noexcept = default;

PhysicalAddress PageTable::rootPhysicalAddress() const noexcept {
    return root_ ? root_->physicalAddress : 0;
}

bool PageTable::rootPhysicalAddressValid() const noexcept {
    return static_cast<bool>(root_);
}

std::array<std::size_t, 4> PageTable::indexes(Iova iova) {
    return {static_cast<std::size_t>((iova >> 39U) & 0x1FFU),
            static_cast<std::size_t>((iova >> 30U) & 0x1FFU),
            static_cast<std::size_t>((iova >> 21U) & 0x1FFU),
            static_cast<std::size_t>((iova >> 12U) & 0x1FFU)};
}

bool PageTable::supportedSize(u64 size) noexcept {
    return size == PAGE_SIZE_4K || size == PAGE_SIZE_2M ||
           size == PAGE_SIZE_1G;
}

u64 PageTable::baseMaskForSize(u64 size) noexcept {
    return ~(size - 1ULL);
}

PageTable::Entry* PageTable::walkToLeaf(Iova iova, u64 size, bool create,
                                        std::string* error) {
    auto fail = [error](const char* message) -> Entry* {
        if (error != nullptr) {
            *error = message;
        }
        return nullptr;
    };

    if ((iova >> 48U) != 0) {
        return fail("IOVA is outside the modeled 48-bit address space");
    }
    if (!supportedSize(size)) {
        return fail("unsupported page size");
    }
    if ((iova & (size - 1ULL)) != 0) {
        return fail("IOVA is not aligned to page size");
    }

    const auto idx = indexes(iova);
    Node* node = root_.get();
    if (node == nullptr) {
        return fail("page table root is missing valid physical address");
    }

    for (int position = 0; position < 4; ++position) {
        Entry* entry = &node->entries[idx[static_cast<std::size_t>(position)]];
        const bool terminalHere =
            (position == 1 && size == PAGE_SIZE_1G) ||
            (position == 2 && size == PAGE_SIZE_2M) || position == 3;
        if (terminalHere) {
            return entry;
        }

        if (!entry->valid || !entry->next) {
            if (!create) {
                return fail("page table walk encountered an invalid entry");
            }
            if (entry->valid && !entry->next) {
                return fail("page table entry is a large page");
            }
            auto child = std::make_unique<Node>();
            child->physicalAddress = nextTablePhysicalAddress_;
            nextTablePhysicalAddress_ += PAGE_SIZE_4K;
            entry->valid = true;
            entry->largePage = false;
            entry->pageSize = 0;
            entry->physicalAddress = child->physicalAddress;
            entry->permissions = AccessPermissions::all();
            entry->next = std::move(child);
        } else if (entry->largePage) {
            return fail("cannot walk through a large-page entry");
        }

        node = entry->next.get();
    }

    return fail("page table walk did not reach a leaf");
}

bool PageTable::map(Iova iova, PhysicalAddress pa, u64 size,
                    AccessPermissions permissions, std::string* error) {
    if (!permissions.read && !permissions.execute) {
        if (error != nullptr) {
            *error = "a mapped page must be readable or executable";
        }
        return false;
    }
    if ((pa & (size - 1ULL)) != 0) {
        if (error != nullptr) {
            *error = "physical address is not aligned to page size";
        }
        return false;
    }
    u64 ignored_end = 0;
    if (overflowingAdd(pa, size, &ignored_end) || (pa >> 52U) != 0) {
        if (error != nullptr) {
            *error = "physical address is outside the modeled 52-bit space";
        }
        return false;
    }

    Entry* entry = walkToLeaf(iova, size, true, error);
    if (entry == nullptr) {
        return false;
    }

    Entry replacement;
    replacement.physicalAddress = pa;
    replacement.valid = true;
    replacement.largePage = size != PAGE_SIZE_4K;
    replacement.pageSize = size;
    replacement.permissions = permissions;
    *entry = std::move(replacement);
    return true;
}

bool PageTable::unmap(Iova iova, u64 size, std::string* error) {
    Entry* entry = walkToLeaf(iova, size, false, error);
    if (entry == nullptr || !entry->valid) {
        if (error != nullptr && error->empty()) {
            *error = "page is not mapped";
        }
        return false;
    }
    if (entry->pageSize != size) {
        if (error != nullptr) {
            *error = "page size does not match mapped large page";
        }
        return false;
    }
    *entry = Entry{};
    return true;
}

std::optional<u64> PageTable::unmapLeaf(Iova iova, std::string* error) {
    auto segment = translate(iova);
    if (!segment) {
        if (error != nullptr) {
            *error = "page is not mapped";
        }
        return std::nullopt;
    }
    if (!unmap(segment->iovaBase, segment->pageSize, error)) {
        return std::nullopt;
    }
    return segment->pageSize;
}

std::optional<TranslationSegment> PageTable::translate(Iova iova) const {
    if ((iova >> 48U) != 0 || root_ == nullptr) {
        return std::nullopt;
    }

    const auto idx = indexes(iova);
    const Node* node = root_.get();
    for (int position = 0; position < 4; ++position) {
        const Entry& entry = node->entries[idx[static_cast<std::size_t>(position)]];
        if (!entry.valid) {
            return std::nullopt;
        }

        const bool terminal = position == 3 || entry.largePage;
        if (terminal) {
            const u64 size = entry.largePage ? entry.pageSize : PAGE_SIZE_4K;
            if (!supportedSize(size)) {
                return std::nullopt;
            }
            const u64 base = iova & baseMaskForSize(size);
            return TranslationSegment{
                base, base + size, entry.physicalAddress, size,
                entry.permissions};
        }

        if (!entry.next) {
            return std::nullopt;
        }
        node = entry.next.get();
    }
    return std::nullopt;
}

std::vector<PageTableWalkEntry> PageTable::walk(Iova iova) const {
    std::vector<PageTableWalkEntry> result;
    if ((iova >> 48U) != 0 || root_ == nullptr) {
        return result;
    }

    const auto idx = indexes(iova);
    const Node* node = root_.get();
    for (int position = 0; position < 4; ++position) {
        const std::size_t index = idx[static_cast<std::size_t>(position)];
        const Entry& entry = node->entries[index];
        const bool leaf = position == 3 || entry.largePage;

        PageTableWalkEntry view;
        view.level = 4 - position;
        view.index = index;
        view.tablePhysicalAddress = node->physicalAddress;
        view.referencedPhysicalAddress = entry.physicalAddress;
        view.physicalAddressValid = entry.valid;
        view.leaf = leaf;
        view.largePage = entry.largePage;
        view.pageSize = entry.pageSize;
        view.permissions = entry.permissions;
        result.push_back(view);

        if (!entry.valid || leaf || !entry.next) {
            break;
        }
        node = entry.next.get();
    }
    return result;
}

RangeTranslation PageTable::translateRange(Iova iova, u64 length,
                                           DmaOp op) const {
    RangeTranslation result;
    u64 end = 0;
    if (length == 0 || overflowingAdd(iova, length, &end)) {
        result.fault = "zero-length or overflowing DMA range";
        return result;
    }

    Iova current = iova;
    while (current < end) {
        auto segment = translate(current);
        if (!segment) {
            result.fault = "IOVA is not mapped in current page table";
            result.mapped = false;
            result.permitted = false;
            return result;
        }
        const Iova chunk_end = std::min(end, segment->iovaEnd);
        result.segments.push_back(*segment);
        if (!segment->permissions.allows(op)) {
            result.mapped = true;
            result.permitted = false;
            result.fault = "current page table denies requested DMA operation";
            return result;
        }
        current = chunk_end;
    }

    result.mapped = true;
    result.permitted = true;
    return result;
}

RangeTranslation PageTable::translateRangeForPermissions(
    Iova iova, u64 length, AccessPermissions required) const {
    RangeTranslation result;
    u64 end = 0;
    if (length == 0 || overflowingAdd(iova, length, &end)) {
        result.fault = "zero-length or overflowing range";
        return result;
    }

    Iova current = iova;
    while (current < end) {
        auto segment = translate(current);
        if (!segment) {
            result.fault = "IOVA is not mapped";
            return result;
        }
        const Iova chunk_end = std::min(end, segment->iovaEnd);
        result.segments.push_back(*segment);
        const bool ok = (!required.read || segment->permissions.read) &&
                        (!required.write || segment->permissions.write) &&
                        (!required.execute || segment->permissions.execute);
        if (!ok) {
            result.mapped = true;
            result.permitted = false;
            result.fault = "page permissions deny requested access";
            return result;
        }
        current = chunk_end;
    }

    result.mapped = true;
    result.permitted = true;
    return result;
}

void PageTable::canonicalNode(const Node& node, std::string& out) const {
    appendHex(out, node.physicalAddress);
    out.push_back('{');
    for (std::size_t i = 0; i < ENTRIES_PER_LEVEL; ++i) {
        const Entry& entry = node.entries[i];
        if (!entry.valid) {
            continue;
        }
        out += "i";
        out += std::to_string(i);
        out += ":v1,pa";
        appendHex(out, entry.physicalAddress);
        out += ",sz";
        out += std::to_string(entry.pageSize);
        out += ",p";
        out.push_back(entry.permissions.read ? 'r' : '-');
        out.push_back(entry.permissions.write ? 'w' : '-');
        out.push_back(entry.permissions.execute ? 'x' : '-');
        if (entry.largePage) {
            out += ",large";
        }
        out.push_back(';');
        if (entry.next) {
            canonicalNode(*entry.next, out);
        }
    }
    out.push_back('}');
}

std::string PageTable::canonicalState() const {
    std::string result = "pt(root=";
    if (!root_) {
        result += "null)";
        return result;
    }
    canonicalNode(*root_, result);
    result.push_back(')');
    return result;
}

} // namespace iommu_ats
