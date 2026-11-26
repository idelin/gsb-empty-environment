#ifndef EPT_NESTED_HPP_INCLUDED
#define EPT_NESTED_HPP_INCLUDED

#include <algorithm>
#include <array>
#include <cstdint>
#include <cstdio>
#include <limits>
#include <string>
#include <utility>
#include <vector>

namespace eptn {

using Address = std::uint64_t;

inline constexpr Address kAddressBits48 = 48;
inline constexpr Address kPageSize4K = Address(1) << 12;
inline constexpr Address kPageSize2M = Address(1) << 21;
inline constexpr Address kPageSize1G = Address(1) << 30;
inline constexpr Address kPhysicalDomainEnd48 = Address(1) << 48;
inline constexpr Address kPhysicalMask48 = kPhysicalDomainEnd48 - 1;
inline constexpr Address NO_ADDRESS = std::numeric_limits<Address>::max();
inline constexpr std::uint32_t NO_NODE = std::numeric_limits<std::uint32_t>::max();

enum class PageKind : unsigned {
    Small = 1,
    Medium = 2,
    Large = 3
};

enum class WalkStatus : unsigned {
    Unmapped,
    Mapped,
    Misconfigured
};

enum class MisconfigReason : unsigned {
    None,
    WriteWithoutRead,
    ReservedBits,
    UnalignedPage,
    BadMemoryType,
    UnsupportedPageSize
};

enum class MapError : unsigned {
    Ok,
    BadGeometry,
    BadAlignment,
    AddressOutOfRange,
    BadPermissions,
    UnsupportedPageSize
};

struct Permissions {
    bool r = false;
    bool w = false;
    bool x_sup = false;
    bool x_usr = false;

    static constexpr Permissions all() noexcept {
        return {true, true, true, true};
    }

    static constexpr Permissions none() noexcept {
        return {false, false, false, false};
    }

    constexpr bool any() const noexcept {
        return r || w || x_sup || x_usr;
    }

    constexpr bool writeWithoutRead() const noexcept {
        return w && !r;
    }

    constexpr bool operator==(const Permissions& other) const noexcept {
        return r == other.r && w == other.w && x_sup == other.x_sup &&
               x_usr == other.x_usr;
    }

    constexpr bool operator!=(const Permissions& other) const noexcept {
        return !(*this == other);
    }
};

constexpr Permissions operator&(const Permissions& a,
                                const Permissions& b) noexcept {
    return {a.r && b.r, a.w && b.w, a.x_sup && b.x_sup,
            a.x_usr && b.x_usr};
}

constexpr Permissions operator|(const Permissions& a,
                                const Permissions& b) noexcept {
    return {a.r || b.r, a.w || b.w, a.x_sup || b.x_sup,
            a.x_usr || b.x_usr};
}

constexpr Permissions operator~(const Permissions& a) noexcept {
    return {!a.r, !a.w, !a.x_sup, !a.x_usr};
}

struct Geometry {
    unsigned address_bits = 48;
    unsigned offset_bits = 12;
    std::array<unsigned, 4> index_bits{{9U, 9U, 9U, 9U}};
    unsigned table_align_bits = 12;

    Address bit_r = Address(1) << 0;
    Address bit_w = Address(1) << 1;
    Address bit_x_sup = Address(1) << 2;
    Address bit_x_usr = Address(1) << 10;
    Address bit_page_size = Address(1) << 7;

    bool compact_layout = false;
    bool check_memory_type = true;

    static Geometry x86_48() {
        return Geometry{};
    }

    static Geometry mini16() {
        Geometry g;
        g.address_bits = 16;
        g.offset_bits = 8;
        g.index_bits = {{2U, 2U, 2U, 2U}};
        g.table_align_bits = 5;
        g.bit_x_usr = Address(1) << 3;
        g.bit_page_size = Address(1) << 4;
        g.compact_layout = true;
        g.check_memory_type = false;
        return g;
    }

    Address domainEnd() const noexcept {
        return Address(1) << address_bits;
    }

    Address domainMask() const noexcept {
        return domainEnd() - Address(1);
    }

    unsigned indexBits(unsigned level) const noexcept {
        return index_bits[level - 1U];
    }

    unsigned blockShift(unsigned level) const noexcept {
        unsigned shift = offset_bits;
        for (unsigned lower = 1; lower < level; ++lower) {
            shift += index_bits[lower - 1U];
        }
        return shift;
    }

    Address blockSize(unsigned level) const noexcept {
        return Address(1) << blockShift(level);
    }

    Address pageSize(PageKind kind) const noexcept {
        return blockSize(static_cast<unsigned>(kind));
    }

    unsigned entryCount(unsigned level) const noexcept {
        return 1U << indexBits(level);
    }

    unsigned indexOf(unsigned level, Address address) const noexcept {
        const unsigned shift = blockShift(level);
        const Address count = Address(1) << indexBits(level);
        return static_cast<unsigned>((address >> shift) & (count - 1U));
    }

    bool aligned(Address address, unsigned level) const noexcept {
        const Address size = blockSize(level);
        return (address & (size - Address(1))) == 0;
    }

    Permissions permissions(Address raw) const noexcept {
        return {
            (raw & bit_r) != 0,
            (raw & bit_w) != 0,
            (raw & bit_x_sup) != 0,
            (raw & bit_x_usr) != 0
        };
    }

    Address permissionBits(Permissions p) const noexcept {
        Address raw = 0;
        if (p.r) raw |= bit_r;
        if (p.w) raw |= bit_w;
        if (p.x_sup) raw |= bit_x_sup;
        if (p.x_usr) raw |= bit_x_usr;
        return raw;
    }

    Address tablePointerMask() const noexcept {
        const Address low_mask =
            (Address(1) << table_align_bits) - Address(1);
        return domainMask() & ~low_mask;
    }

    Address leafAddressMask(unsigned level) const noexcept {
        const Address low_mask = blockSize(level) - Address(1);
        return domainMask() & ~low_mask;
    }

    Address alignmentReservedMask(unsigned level) const noexcept {
        const Address block_low = blockSize(level) - Address(1);
        const Address flag_low = compact_layout
            ? ((Address(1) << table_align_bits) - Address(1))
            : Address(0xFFF);
        return block_low & ~flag_low;
    }

    Address reservedMask(unsigned level, bool leaf) const noexcept {
        Address allowed = 0;
        allowed |= bit_r | bit_w | bit_x_sup | bit_x_usr;

        if (compact_layout) {
            if (leaf || level == 1U) {
                allowed |= bit_page_size;
            }
            if (leaf) {
                allowed |= leafAddressMask(level);
            } else {
                allowed |= tablePointerMask();
            }
            return ~allowed;
        }

        allowed |= Address(1) << 8;   /* accessed */
        allowed |= Address(1) << 9;   /* dirty/ignored at non-leaf */
        allowed |= Address(1) << 11;  /* ignored */
        allowed |= Address(0xFFF) << 52; /* ignored software bits */
        allowed |= domainMask() & ~Address(0xFFF);

        if (leaf) {
            allowed |= Address(1) << 6; /* ignore PAT / leaf attribute */
            allowed |= Address(7) << 3; /* EPT memory type field */
            allowed |= bit_page_size;
            const Address table_address = domainMask() & ~Address(0xFFF);
            allowed &= ~table_address;
            allowed |= leafAddressMask(level);
        } else if (level == 1U) {
            allowed |= bit_page_size;
        }
        return ~allowed;
    }
};

struct EntryInspection {
    Permissions permissions;
    MisconfigReason reason = MisconfigReason::None;
    Address reserved_bits = 0;
    bool active = false;
    bool leaf = false;
};

inline EntryInspection inspectEntry(const Geometry& geo, Address raw,
                                    unsigned level) noexcept {
    EntryInspection result;
    result.permissions = geo.permissions(raw);
    result.active = result.permissions.any();
    if (!result.active) {
        return result;
    }

    const bool page_size = (raw & geo.bit_page_size) != 0;
    result.leaf = (level == 1U) ||
                  (page_size && (level == 2U || level == 3U));

    if (result.permissions.writeWithoutRead()) {
        result.reason = MisconfigReason::WriteWithoutRead;
        return result;
    }

    if (page_size && level == 4U) {
        result.reason = MisconfigReason::UnsupportedPageSize;
        return result;
    }

    if (result.leaf) {
        const Address alignment_bits = geo.alignmentReservedMask(level);
        if ((raw & alignment_bits) != 0) {
            result.reason = MisconfigReason::UnalignedPage;
            result.reserved_bits = raw & alignment_bits;
            return result;
        }

        if (geo.check_memory_type && !geo.compact_layout) {
            const unsigned memory_type =
                static_cast<unsigned>((raw >> 3) & Address(7));
            if (memory_type == 2U || memory_type == 3U ||
                memory_type == 7U) {
                result.reason = MisconfigReason::BadMemoryType;
                return result;
            }
        }
    }

    result.reserved_bits = raw & geo.reservedMask(level, result.leaf);
    if (result.reserved_bits != 0) {
        result.reason = MisconfigReason::ReservedBits;
    }
    return result;
}

struct WitnessStep {
    unsigned level = 0;
    unsigned index = 0;
    std::uint32_t node_id = NO_NODE;
    Address table_pa = 0;
    Address entry_pa = 0;
    Address raw = 0;
};

struct WalkWitness {
    std::array<WitnessStep, 4> steps{};
    unsigned count = 0;
};

struct WalkResult {
    WalkStatus status = WalkStatus::Unmapped;
    MisconfigReason reason = MisconfigReason::None;
    Permissions permissions{};
    Address page_base = 0;
    Address hpa = 0;
    Address reserved_bits = 0;
    unsigned leaf_level = 0;
    WalkWitness witness{};
};

enum class AccessType : unsigned {
    Read,
    Write,
    SupervisorExecute,
    UserExecute
};

struct AccessRequest {
    bool read = false;
    bool write = false;
    bool supervisor_execute = false;
    bool user_execute = false;

    static AccessRequest readAccess() { return {true, false, false, false}; }
    static AccessRequest writeAccess() { return {false, true, false, false}; }
    static AccessRequest supervisorFetch() {
        return {false, false, true, false};
    }
    static AccessRequest userFetch() {
        return {false, false, false, true};
    }
};

enum class AccessFault : unsigned {
    None,
    EptViolation,
    EptMisconfiguration
};

struct AccessDecision {
    bool allowed = false;
    AccessFault fault = AccessFault::EptViolation;
    MisconfigReason reason = MisconfigReason::None;
    Address hpa = 0;
    Permissions permissions{};
};

inline AccessDecision checkAccess(const WalkResult& walk,
                                  AccessRequest request) noexcept {
    AccessDecision decision;
    decision.reason = walk.reason;
    decision.hpa = walk.hpa;
    decision.permissions = walk.permissions;

    if (walk.status == WalkStatus::Misconfigured) {
        decision.allowed = false;
        decision.fault = AccessFault::EptMisconfiguration;
        return decision;
    }
    if (walk.status != WalkStatus::Mapped) {
        decision.allowed = false;
        decision.fault = AccessFault::EptViolation;
        return decision;
    }

    bool allowed = true;
    if (request.read && !walk.permissions.r) allowed = false;
    if (request.write && !walk.permissions.w) allowed = false;
    if (request.supervisor_execute && !walk.permissions.x_sup) allowed = false;
    if (request.user_execute && !walk.permissions.x_usr) allowed = false;

    decision.allowed = allowed;
    decision.fault = allowed ? AccessFault::None
                             : AccessFault::EptViolation;
    return decision;
}

struct FlatRange {
    Address lo = 0;
    Address hi = 0;
    WalkStatus status = WalkStatus::Unmapped;
    Address hpa_base = 0;
    Permissions permissions{};
    MisconfigReason reason = MisconfigReason::None;
    unsigned leaf_level = 0;
};

struct PageTableNode {
    unsigned level = 4;
    Address table_pa = 0;
    std::vector<Address> entries;
    std::vector<std::uint32_t> child;
};

class ICandidateEpt02 {
public:
    virtual ~ICandidateEpt02() = default;
    virtual const Geometry& geometry() const = 0;
    virtual WalkResult walk(Address virtual_address) const = 0;
    virtual std::vector<FlatRange> flatten() const = 0;
};

class EptPageTable : public ICandidateEpt02 {
public:
    explicit EptPageTable(Geometry geometry = Geometry::x86_48())
        : geometry_(std::move(geometry)) {
        next_table_pa_ = geometry_.address_bits >= 20U
            ? (Address(1) << 20)
            : (Address(1) << (geometry_.table_align_bits + 4U));
        nodes_.push_back(makeNode_(4U, allocTablePa_()));
        root_ = 0;
    }

    const Geometry& geometry() const noexcept override { return geometry_; }
    std::uint32_t rootId() const noexcept { return root_; }

    std::size_t nodeCount() const noexcept { return nodes_.size(); }

    PageTableNode& node(std::uint32_t id) { return nodes_[id]; }
    const PageTableNode& node(std::uint32_t id) const { return nodes_[id]; }

    Address& rawEntry(std::uint32_t node_id, unsigned index) {
        return nodes_[node_id].entries[index];
    }

    const Address& rawEntry(std::uint32_t node_id, unsigned index) const {
        return nodes_[node_id].entries[index];
    }

    MapError map(Address virtual_address, Address physical_address,
                 Permissions permissions, PageKind kind) {
        const unsigned leaf_level = static_cast<unsigned>(kind);
        if (leaf_level < 1U || leaf_level > 3U) {
            return MapError::UnsupportedPageSize;
        }
        if (permissions.writeWithoutRead()) {
            return MapError::BadPermissions;
        }

        const Address size = geometry_.blockSize(leaf_level);
        const Address domain_end = geometry_.domainEnd();
        if (!geometry_.aligned(virtual_address, leaf_level) ||
            !geometry_.aligned(physical_address, leaf_level)) {
            return MapError::BadAlignment;
        }
        if (virtual_address > domain_end - size ||
            physical_address > domain_end - size) {
            return MapError::AddressOutOfRange;
        }

        std::uint32_t current = root_;
        for (unsigned level = 4; level > leaf_level; --level) {
            const unsigned index = geometry_.indexOf(level, virtual_address);
            PageTableNode& current_node = nodes_[current];
            const Address raw = current_node.entries[index];
            const EntryInspection inspection =
                inspectEntry(geometry_, raw, level);

            if (inspection.leaf) {
                splitLeaf_(current, index);
            }

            std::uint32_t child_id = current_node.child[index];
            if (child_id == NO_NODE) {
                child_id = ensureChild_(current, index);
            }

            PageTableNode& parent = nodes_[current];
            const Address child_pa = nodes_[child_id].table_pa;
            if (!geometry_.permissions(parent.entries[index]).any() ||
                inspection.leaf) {
                parent.entries[index] =
                    (child_pa & geometry_.tablePointerMask()) |
                    geometry_.permissionBits(Permissions::all());
            } else {
                parent.entries[index] =
                    (parent.entries[index] & ~geometry_.tablePointerMask()) |
                    (child_pa & geometry_.tablePointerMask());
            }
            current = child_id;
        }

        const unsigned index = geometry_.indexOf(leaf_level, virtual_address);
        PageTableNode& leaf_node = nodes_[current];
        Address raw = physical_address & geometry_.leafAddressMask(leaf_level);
        raw |= geometry_.permissionBits(permissions);
        if (leaf_level > 1U) {
            raw |= geometry_.bit_page_size;
        }
        leaf_node.entries[index] = raw;
        leaf_node.child[index] = NO_NODE;
        return MapError::Ok;
    }

    WalkResult walk(Address virtual_address) const override {
        WalkResult result;
        std::uint32_t node_id = root_;
        Address external_table_pa = nodes_[root_].table_pa;
        Permissions effective = Permissions::all();

        for (int signed_level = 4; signed_level >= 1; --signed_level) {
            const unsigned level = static_cast<unsigned>(signed_level);
            const unsigned index = geometry_.indexOf(level, virtual_address);
            const PageTableNode* node_ptr = nullptr;
            if (node_id != NO_NODE && node_id < nodes_.size()) {
                node_ptr = &nodes_[node_id];
            }

            Address table_pa = external_table_pa;
            Address raw = 0;
            std::uint32_t step_node = NO_NODE;
            if (node_ptr != nullptr) {
                table_pa = node_ptr->table_pa;
                raw = node_ptr->entries[index];
                step_node = node_id;
            }

            WitnessStep& step =
                result.witness.steps[result.witness.count];
            step.level = level;
            step.index = index;
            step.node_id = step_node;
            step.table_pa = table_pa;
            step.entry_pa = table_pa + static_cast<Address>(index) * 8U;
            step.raw = raw;
            ++result.witness.count;

            const EntryInspection inspection =
                inspectEntry(geometry_, raw, level);
            if (!inspection.active) {
                result.status = WalkStatus::Unmapped;
                return result;
            }

            effective = effective & inspection.permissions;
            if (inspection.reason != MisconfigReason::None) {
                result.status = WalkStatus::Misconfigured;
                result.reason = inspection.reason;
                result.reserved_bits = inspection.reserved_bits;
                return result;
            }

            if (inspection.leaf) {
                const Address block_size = geometry_.blockSize(level);
                result.status = WalkStatus::Mapped;
                result.page_base =
                    raw & geometry_.leafAddressMask(level);
                result.hpa = result.page_base |
                             (virtual_address & (block_size - Address(1)));
                result.permissions = effective;
                result.leaf_level = level;
                return result;
            }

            if (node_ptr != nullptr &&
                index < node_ptr->child.size() &&
                node_ptr->child[index] != NO_NODE) {
                node_id = node_ptr->child[index];
                external_table_pa = nodes_[node_id].table_pa;
            } else {
                node_id = NO_NODE;
                external_table_pa = raw & geometry_.tablePointerMask();
            }
        }

        result.status = WalkStatus::Unmapped;
        return result;
    }

    std::vector<FlatRange> flatten() const override {
        std::vector<FlatRange> ranges;
        flattenNode_(root_, 4U, 0, Permissions::all(), ranges);
        coalesce_(ranges);
        return ranges;
    }

private:
    Geometry geometry_;
    Address next_table_pa_ = 0;
    std::vector<PageTableNode> nodes_;
    std::uint32_t root_ = NO_NODE;

    Address allocTablePa_() {
        const Address table_bytes = Address(1) << geometry_.table_align_bits;
        const Address allocated = next_table_pa_;
        next_table_pa_ += table_bytes;
        return allocated;
    }

    PageTableNode makeNode_(unsigned level, Address table_pa) const {
        PageTableNode node;
        node.level = level;
        node.table_pa = table_pa;
        const std::size_t count = geometry_.entryCount(level);
        node.entries.assign(count, 0);
        node.child.assign(count, NO_NODE);
        return node;
    }

    std::uint32_t ensureChild_(std::uint32_t node_id, unsigned index) {
        if (nodes_[node_id].child[index] != NO_NODE) {
            return nodes_[node_id].child[index];
        }
        const unsigned child_level = nodes_[node_id].level - 1U;
        const std::uint32_t child_id =
            static_cast<std::uint32_t>(nodes_.size());
        nodes_.push_back(makeNode_(child_level, allocTablePa_()));
        nodes_[node_id].child[index] = child_id;
        return child_id;
    }

    void splitLeaf_(std::uint32_t node_id, unsigned index) {
        const Address old_raw = nodes_[node_id].entries[index];
        const unsigned level = nodes_[node_id].level;
        if (level <= 1U) {
            return;
        }

        const std::uint32_t child_id = ensureChild_(node_id, index);
        const Address old_base =
            old_raw & geometry_.leafAddressMask(level);
        const Permissions old_permissions = geometry_.permissions(old_raw);
        const Address child_size = geometry_.blockSize(level - 1U);

        PageTableNode& child = nodes_[child_id];
        for (unsigned i = 0; i < geometry_.entryCount(level - 1U); ++i) {
            const Address child_pa =
                old_base + static_cast<Address>(i) * child_size;
            Address raw = child_pa & geometry_.leafAddressMask(level - 1U);
            raw |= geometry_.permissionBits(old_permissions);
            if (level - 1U > 1U) {
                raw |= geometry_.bit_page_size;
            }
            child.entries[i] = raw;
        }

        PageTableNode& parent = nodes_[node_id];
        parent.entries[index] =
            (child.table_pa & geometry_.tablePointerMask()) |
            geometry_.permissionBits(Permissions::all());
        parent.child[index] = child_id;
    }

    void flattenNode_(std::uint32_t node_id, unsigned level, Address base,
                      Permissions parent_permissions,
                      std::vector<FlatRange>& ranges) const {
        const PageTableNode& node = nodes_[node_id];
        const Address block_size = geometry_.blockSize(level);
        const Address count = geometry_.entryCount(level);

        for (unsigned index = 0; index < count; ++index) {
            const Address lo =
                base + static_cast<Address>(index) * block_size;
            const Address hi = lo + block_size;
            const Address raw = node.entries[index];
            const EntryInspection inspection =
                inspectEntry(geometry_, raw, level);

            if (!inspection.active) {
                ranges.push_back({lo, hi, WalkStatus::Unmapped, 0,
                                  Permissions::none(),
                                  MisconfigReason::None, 0});
                continue;
            }

            const Permissions effective =
                parent_permissions & inspection.permissions;

            if (inspection.reason != MisconfigReason::None) {
                ranges.push_back({lo, hi, WalkStatus::Misconfigured, 0,
                                  Permissions::none(), inspection.reason,
                                  level});
                continue;
            }

            if (inspection.leaf) {
                const Address page_base =
                    raw & geometry_.leafAddressMask(level);
                ranges.push_back({lo, hi, WalkStatus::Mapped, page_base,
                                  effective, MisconfigReason::None, level});
                continue;
            }

            if (index < node.child.size() &&
                node.child[index] != NO_NODE) {
                flattenNode_(node.child[index], level - 1U, lo,
                             effective, ranges);
            } else {
                ranges.push_back({lo, hi, WalkStatus::Unmapped, 0,
                                  Permissions::none(),
                                  MisconfigReason::None, 0});
            }
        }
    }

    static void coalesce_(std::vector<FlatRange>& ranges) {
        std::vector<FlatRange> merged;
        merged.reserve(ranges.size());
        for (const FlatRange& range : ranges) {
            if (merged.empty()) {
                merged.push_back(range);
                continue;
            }

            FlatRange& previous = merged.back();
            bool can_merge = previous.hi == range.lo &&
                             previous.status == range.status &&
                             previous.reason == range.reason &&
                             previous.permissions == range.permissions;
            if (can_merge && previous.status == WalkStatus::Mapped) {
                const Address expected_hpa =
                    previous.hpa_base + (previous.hi - previous.lo);
                can_merge = expected_hpa == range.hpa_base;
            }
            if (can_merge) {
                previous.hi = range.hi;
            } else {
                merged.push_back(range);
            }
        }
        ranges.swap(merged);
    }
};

inline AccessDecision checkAccess(const ICandidateEpt02& table,
                                  Address virtual_address,
                                  AccessRequest request) {
    return checkAccess(table.walk(virtual_address), request);
}

struct ComposeResult {
    bool ok = false;
    MapError error = MapError::Ok;
    std::string message;
    EptPageTable ept02;
};

struct ComposeOptions {
    bool preserve_source_fault_ranges = false;
};

inline std::vector<FlatRange> composeSemantics(
    const EptPageTable& ept01, const EptPageTable& ept12) {
    std::vector<FlatRange> result;
    const std::vector<FlatRange> first = ept12.flatten();
    const std::vector<FlatRange> second = ept01.flatten();

    for (const FlatRange& l2_range : first) {
        if (l2_range.status != WalkStatus::Mapped) {
            result.push_back(l2_range);
            continue;
        }

        const Address l1_lo = l2_range.hpa_base;
        const Address l1_hi = l1_lo + (l2_range.hi - l2_range.lo);
        auto it = std::lower_bound(
            second.begin(), second.end(), l1_lo,
            [](const FlatRange& range, Address value) {
                return range.hi <= value;
            });

        for (; it != second.end() && it->lo < l1_hi; ++it) {
            const FlatRange& l1_range = *it;
            const Address q_lo = (std::max)(l1_lo, l1_range.lo);
            const Address q_hi = (std::min)(l1_hi, l1_range.hi);
            if (q_lo >= q_hi) {
                continue;
            }

            const Address l2_lo =
                l2_range.lo + (q_lo - l1_lo);
            const Address l2_hi = l2_lo + (q_hi - q_lo);

            if (l1_range.status == WalkStatus::Mapped) {
                const Address hpa_base =
                    l1_range.hpa_base + (q_lo - l1_range.lo);
                const Permissions permissions =
                    l2_range.permissions & l1_range.permissions;
                result.push_back({l2_lo, l2_hi, WalkStatus::Mapped,
                                  hpa_base, permissions,
                                  MisconfigReason::None, 0});
            } else {
                result.push_back({l2_lo, l2_hi, l1_range.status, 0,
                                  Permissions::none(), l1_range.reason,
                                  l1_range.leaf_level});
            }
        }
    }

    std::vector<FlatRange> merged;
    merged.reserve(result.size());
    for (const FlatRange& range : result) {
        if (merged.empty()) {
            merged.push_back(range);
            continue;
        }

        FlatRange& previous = merged.back();
        bool can_merge = previous.hi == range.lo &&
                         previous.status == range.status &&
                         previous.reason == range.reason &&
                         previous.permissions == range.permissions;
        if (can_merge && range.status == WalkStatus::Mapped) {
            const Address expected_hpa =
                previous.hpa_base + (previous.hi - previous.lo);
            can_merge = expected_hpa == range.hpa_base;
        }

        if (can_merge) {
            previous.hi = range.hi;
        } else {
            merged.push_back(range);
        }
    }
    return merged;
}

inline MapError mapBestFitRange(EptPageTable& table, Address virtual_address,
                                Address physical_address, Address length,
                                Permissions permissions) {
    const Geometry& geometry = table.geometry();
    Address gpa = virtual_address;
    Address hpa = physical_address;
    Address remaining = length;

    while (remaining != 0) {
        bool mapped = false;
        const PageKind kinds[3] = {
            PageKind::Large, PageKind::Medium, PageKind::Small
        };
        for (PageKind kind : kinds) {
            const Address size = geometry.pageSize(kind);
            if (remaining < size) {
                continue;
            }
            if ((gpa & (size - 1U)) != 0 ||
                (hpa & (size - 1U)) != 0) {
                continue;
            }
            const MapError error = table.map(gpa, hpa, permissions, kind);
            if (error != MapError::Ok) {
                return error;
            }
            gpa += size;
            hpa += size;
            remaining -= size;
            mapped = true;
            break;
        }
        if (!mapped) {
            return MapError::BadAlignment;
        }
    }
    return MapError::Ok;
}

inline ComposeResult composeExact(const EptPageTable& ept01,
                                  const EptPageTable& ept12,
                                  ComposeOptions options = {}) {
    ComposeResult result;
    if (ept01.geometry().address_bits !=
        ept12.geometry().address_bits) {
        result.ok = false;
        result.error = MapError::BadGeometry;
        result.message = "EPT01 and EPT12 geometries differ";
        return result;
    }

    const Geometry geometry = ept01.geometry();
    EptPageTable ept02(geometry);
    const std::vector<FlatRange> semantics =
        composeSemantics(ept01, ept12);

    for (const FlatRange& range : semantics) {
        if (range.status != WalkStatus::Mapped) {
            continue;
        }
        const MapError error = mapBestFitRange(
            ept02, range.lo, range.hpa_base, range.hi - range.lo,
            range.permissions);
        if (error != MapError::Ok) {
            result.ok = false;
            result.error = error;
            result.message = "failed to install composed range";
            result.ept02 = std::move(ept02);
            return result;
        }
    }

    result.ok = true;
    result.error = MapError::Ok;
    result.ept02 = std::move(ept02);
    (void)options;
    return result;
}
enum class ViolationKind : unsigned {
    None,
    SourceMisconfiguration,
    UnauthorizedMapping,
    HpaMismatch,
    PermissionEscalation,
    PermissionDowngrade,
    MissingMapping,
    UnexpectedMapping,
    CandidateMisconfiguration,
    FaultClassMismatch,
    GeometryMismatch
};

inline bool isSecurityBreach(ViolationKind kind) noexcept {
    return kind == ViolationKind::UnauthorizedMapping ||
           kind == ViolationKind::HpaMismatch ||
           kind == ViolationKind::PermissionEscalation;
}

struct VerifyOptions {
    bool exact_permissions = false;
    bool require_complete_mappings = false;
    bool reject_unexpected_mappings = false;
    bool reject_candidate_misconfiguration = true;
    bool reject_source_misconfiguration = true;
    bool exact_fault_class = false;
    std::size_t max_violations = 64;

    static VerifyOptions securityOnly() {
        return VerifyOptions{};
    }

    static VerifyOptions exactComposition() {
        VerifyOptions options;
        options.exact_permissions = true;
        options.require_complete_mappings = true;
        options.reject_unexpected_mappings = true;
        options.reject_candidate_misconfiguration = true;
        options.exact_fault_class = false;
        return options;
    }
};

struct WitnessBundle {
    Address l2_gpa = NO_ADDRESS;
    WalkResult ept12{};
    bool has_ept01 = false;
    Address l1_gpa = NO_ADDRESS;
    WalkResult ept01{};
    WalkResult ept02{};
};

struct Violation {
    ViolationKind kind = ViolationKind::None;
    Address l2_gpa = 0;
    Address l2_end = NO_ADDRESS;
    Address expected_hpa = NO_ADDRESS;
    Address actual_hpa = NO_ADDRESS;
    Permissions expected_permissions{};
    Permissions actual_permissions{};
    Permissions diff_permissions{};
    MisconfigReason misconfig_reason = MisconfigReason::None;
    bool security_breach = false;
    WitnessBundle witness{};
};

struct VerificationReport {
    bool geometry_ok = true;
    std::vector<Violation> violations;
    std::size_t symbolic_ranges = 0;
    Address lowest_gpa = NO_ADDRESS;
    WitnessBundle lowest_witness{};

    bool clean() const noexcept { return geometry_ok && violations.empty(); }
    bool securityOk() const noexcept {
        if (!geometry_ok) {
            return false;
        }
        for (const Violation& violation : violations) {
            if (violation.security_breach) {
                return false;
            }
        }
        return true;
    }
};

inline WitnessBundle makeWitness(Address l2_gpa,
                                const EptPageTable& ept01,
                                const EptPageTable& ept12,
                                const ICandidateEpt02& ept02) {
    WitnessBundle witness;
    witness.l2_gpa = l2_gpa;
    witness.ept12 = ept12.walk(l2_gpa);
    if (witness.ept12.status == WalkStatus::Mapped) {
        witness.l1_gpa = witness.ept12.hpa;
        witness.has_ept01 = true;
        witness.ept01 = ept01.walk(witness.l1_gpa);
    }
    witness.ept02 = ept02.walk(l2_gpa);
    return witness;
}

inline const char* violationKindName(ViolationKind kind) noexcept {
    switch (kind) {
        case ViolationKind::None: return "None";
        case ViolationKind::SourceMisconfiguration:
            return "SourceMisconfiguration";
        case ViolationKind::UnauthorizedMapping: return "UnauthorizedMapping";
        case ViolationKind::HpaMismatch: return "HpaMismatch";
        case ViolationKind::PermissionEscalation: return "PermissionEscalation";
        case ViolationKind::PermissionDowngrade: return "PermissionDowngrade";
        case ViolationKind::MissingMapping: return "MissingMapping";
        case ViolationKind::UnexpectedMapping: return "UnexpectedMapping";
        case ViolationKind::CandidateMisconfiguration:
            return "CandidateMisconfiguration";
        case ViolationKind::FaultClassMismatch: return "FaultClassMismatch";
        case ViolationKind::GeometryMismatch: return "GeometryMismatch";
    }
    return "Unknown";
}

inline const char* walkStatusName(WalkStatus status) noexcept {
    switch (status) {
        case WalkStatus::Unmapped: return "Unmapped";
        case WalkStatus::Mapped: return "Mapped";
        case WalkStatus::Misconfigured: return "Misconfigured";
    }
    return "Unknown";
}

inline const char* misconfigReasonName(MisconfigReason reason) noexcept {
    switch (reason) {
        case MisconfigReason::None: return "None";
        case MisconfigReason::WriteWithoutRead: return "WriteWithoutRead";
        case MisconfigReason::ReservedBits: return "ReservedBits";
        case MisconfigReason::UnalignedPage: return "UnalignedPage";
        case MisconfigReason::BadMemoryType: return "BadMemoryType";
        case MisconfigReason::UnsupportedPageSize:
            return "UnsupportedPageSize";
    }
    return "Unknown";
}

inline std::string hex(Address value) {
    char buffer[19];
    std::snprintf(buffer, sizeof(buffer), "0x%016llx",
                  static_cast<unsigned long long>(value));
    return std::string(buffer);
}

inline std::string formatPermissions(Permissions p) {
    std::string result;
    result += "R=";
    result += p.r ? '1' : '0';
    result += " W=";
    result += p.w ? '1' : '0';
    result += " Xs=";
    result += p.x_sup ? '1' : '0';
    result += " Xu=";
    result += p.x_usr ? '1' : '0';
    return result;
}

inline std::string formatWalk(const WalkResult& walk) {
    std::string result;
    result += walkStatusName(walk.status);
    result += " reason=";
    result += misconfigReasonName(walk.reason);
    result += " perms{";
    result += formatPermissions(walk.permissions);
    result += "} hpa=";
    result += hex(walk.hpa);
    result += " base=";
    result += hex(walk.page_base);
    for (unsigned i = 0; i < walk.witness.count; ++i) {
        const WitnessStep& step = walk.witness.steps[i];
        result += "\n    L";
        result += std::to_string(step.level);
        result += " idx=";
        result += std::to_string(step.index);
        result += " entry_pa=";
        result += hex(step.entry_pa);
        result += " raw=";
        result += hex(step.raw);
    }
    return result;
}

inline std::string formatWitnessBundle(const WitnessBundle& witness) {
    std::string result;
    result += "lowest_l2_gpa=";
    result += hex(witness.l2_gpa);
    result += "\n  EPT12: ";
    result += formatWalk(witness.ept12);
    if (witness.has_ept01) {
        result += "\n  EPT01@";
        result += hex(witness.l1_gpa);
        result += ": ";
        result += formatWalk(witness.ept01);
    }
    result += "\n  EPT02: ";
    result += formatWalk(witness.ept02);
    return result;
}

namespace detail {

inline void addViolation(VerificationReport& report,
                         const VerifyOptions& options,
                         Violation violation,
                         const EptPageTable& ept01,
                         const EptPageTable& ept12,
                         const ICandidateEpt02& ept02) {
    violation.security_breach = isSecurityBreach(violation.kind);
    if (report.violations.size() < options.max_violations) {
        violation.witness = makeWitness(
            violation.l2_gpa, ept01, ept12, ept02);
        if (report.lowest_gpa == NO_ADDRESS ||
            violation.l2_gpa < report.lowest_gpa) {
            report.lowest_gpa = violation.l2_gpa;
            report.lowest_witness = violation.witness;
        }
        report.violations.push_back(std::move(violation));
    } else if (report.lowest_gpa == NO_ADDRESS) {
        report.lowest_gpa = violation.l2_gpa;
        report.lowest_witness = makeWitness(
            violation.l2_gpa, ept01, ept12, ept02);
    }
}

} // namespace detail

inline VerificationReport verifyCandidate(
    const EptPageTable& ept01, const EptPageTable& ept12,
    const ICandidateEpt02& ept02, VerifyOptions options = {}) {
    VerificationReport report;
    if (ept01.geometry().address_bits != ept12.geometry().address_bits ||
        ept01.geometry().address_bits != ept02.geometry().address_bits) {
        report.geometry_ok = false;
        return report;
    }

    const std::vector<FlatRange> expected = composeSemantics(ept01, ept12);
    const std::vector<FlatRange> actual = ept02.flatten();
    report.symbolic_ranges = expected.size() + actual.size();

    std::size_t i = 0;
    std::size_t j = 0;
    while (i < expected.size() && j < actual.size()) {
        const FlatRange& e = expected[i];
        const FlatRange& c = actual[j];
        const Address lo = (std::max)(e.lo, c.lo);
        const Address hi = (std::min)(e.hi, c.hi);

        if (lo < hi) {
            const Address expected_hpa =
                e.status == WalkStatus::Mapped
                    ? e.hpa_base + (lo - e.lo)
                    : NO_ADDRESS;
            const Address actual_hpa =
                c.status == WalkStatus::Mapped
                    ? c.hpa_base + (lo - c.lo)
                    : NO_ADDRESS;

            auto record = [&](Violation violation) {
                violation.l2_end = hi;
                detail::addViolation(report, options, std::move(violation),
                                     ept01, ept12, ept02);
            };

            if (e.status == WalkStatus::Misconfigured &&
                options.reject_source_misconfiguration) {
                Violation violation;
                violation.kind = ViolationKind::SourceMisconfiguration;
                violation.l2_gpa = lo;
                violation.misconfig_reason = e.reason;
                record(std::move(violation));
            }

            if (c.status == WalkStatus::Misconfigured &&
                options.reject_candidate_misconfiguration) {
                Violation violation;
                violation.kind = ViolationKind::CandidateMisconfiguration;
                violation.l2_gpa = lo;
                violation.actual_hpa = actual_hpa;
                violation.actual_permissions = c.permissions;
                violation.misconfig_reason = c.reason;
                record(std::move(violation));
            }

            if (c.status == WalkStatus::Mapped) {
                if (e.status != WalkStatus::Mapped) {
                    const bool accessible = c.permissions.any();
                    if (accessible ||
                        options.reject_unexpected_mappings) {
                        Violation violation;
                        violation.kind = accessible
                            ? ViolationKind::UnauthorizedMapping
                            : ViolationKind::UnexpectedMapping;
                        violation.l2_gpa = lo;
                        violation.actual_hpa = actual_hpa;
                        violation.actual_permissions = c.permissions;
                        record(std::move(violation));
                    }
                } else {
                    if (expected_hpa != actual_hpa) {
                        Violation violation;
                        violation.kind = ViolationKind::HpaMismatch;
                        violation.l2_gpa = lo;
                        violation.expected_hpa = expected_hpa;
                        violation.actual_hpa = actual_hpa;
                        violation.expected_permissions = e.permissions;
                        violation.actual_permissions = c.permissions;
                        record(std::move(violation));
                    }

                    const Permissions escalation =
                        c.permissions & ~e.permissions;
                    if (escalation.any()) {
                        Violation violation;
                        violation.kind = ViolationKind::PermissionEscalation;
                        violation.l2_gpa = lo;
                        violation.expected_hpa = expected_hpa;
                        violation.actual_hpa = actual_hpa;
                        violation.expected_permissions = e.permissions;
                        violation.actual_permissions = c.permissions;
                        violation.diff_permissions = escalation;
                        record(std::move(violation));
                    }

                    if (options.exact_permissions) {
                        const Permissions downgrade =
                            e.permissions & ~c.permissions;
                        if (downgrade.any()) {
                            Violation violation;
                            violation.kind =
                                ViolationKind::PermissionDowngrade;
                            violation.l2_gpa = lo;
                            violation.expected_permissions = e.permissions;
                            violation.actual_permissions = c.permissions;
                            violation.diff_permissions = downgrade;
                            record(std::move(violation));
                        }
                    }
                }
            } else if (e.status == WalkStatus::Mapped &&
                       options.require_complete_mappings) {
                Violation violation;
                violation.kind = ViolationKind::MissingMapping;
                violation.l2_gpa = lo;
                violation.expected_hpa = expected_hpa;
                record(std::move(violation));
            }

            if (options.exact_fault_class &&
                e.status == WalkStatus::Misconfigured &&
                c.status != WalkStatus::Misconfigured) {
                Violation violation;
                violation.kind = ViolationKind::FaultClassMismatch;
                violation.l2_gpa = lo;
                record(std::move(violation));
            }
        }

        if (e.hi == hi) {
            ++i;
        }
        if (c.hi == hi) {
            ++j;
        }
    }

    return report;
}

template <class Composer>
VerificationReport verifyComposer(const EptPageTable& ept01,
                                  const EptPageTable& ept12,
                                  Composer&& composer,
                                  VerifyOptions options = {}) {
    auto candidate = composer(ept01, ept12);
    return verifyCandidate(ept01, ept12, candidate, options);
}

struct ExhaustiveReport {
    bool ok = true;
    unsigned cases = 0;
    std::uint64_t pages_checked = 0;
    std::uint64_t point_checks = 0;
    std::uint64_t byte_checks = 0;
    Address symbolic_lowest = NO_ADDRESS;
    Address brute_lowest = NO_ADDRESS;
    std::string error;
};

namespace detail {

struct MiniRng {
    std::uint32_t state;

    std::uint32_t next() {
        state = state * 1664525U + 1013904223U;
        return state;
    }

    unsigned below(unsigned bound) {
        return static_cast<unsigned>(next() % bound);
    }
};

inline Permissions randomLegalPermissions(MiniRng& rng) {
    Permissions p;
    p.r = (rng.next() & 1U) != 0;
    p.w = p.r && ((rng.next() & 1U) != 0);
    p.x_sup = (rng.next() & 1U) != 0;
    p.x_usr = (rng.next() & 1U) != 0;
    if (!p.any()) {
        p.r = true;
    }
    return p;
}

inline const FlatRange* rangeAt(const std::vector<FlatRange>& ranges,
                                Address address) {
    auto it = std::lower_bound(
        ranges.begin(), ranges.end(), address,
        [](const FlatRange& range, Address value) {
            return range.hi <= value;
        });
    if (it == ranges.end() || address < it->lo || address >= it->hi) {
        return nullptr;
    }
    return &*it;
}

inline Address bruteFirstViolation16(const EptPageTable& ept01,
                                     const EptPageTable& ept12,
                                     const EptPageTable& ept02,
                                     const VerifyOptions& options) {
    const Geometry& geo = ept12.geometry();
    const Address page_size = geo.blockSize(1);
    const unsigned page_count =
        static_cast<unsigned>(geo.domainEnd() / page_size);

    for (unsigned page = 0; page < page_count; ++page) {
        const Address gpa = static_cast<Address>(page) * page_size;
        const WalkResult w12 = ept12.walk(gpa);
        WalkResult w01;
        Address expected_hpa = NO_ADDRESS;
        Permissions expected_permissions{};
        WalkStatus expected_status = w12.status;

        if (w12.status == WalkStatus::Mapped) {
            w01 = ept01.walk(w12.hpa);
            expected_status = w01.status;
            if (w01.status == WalkStatus::Mapped) {
                expected_hpa = w01.hpa;
                expected_permissions = w12.permissions & w01.permissions;
            }
        }

        const WalkResult c02 = ept02.walk(gpa);
        bool bad = false;

        if (c02.status == WalkStatus::Misconfigured &&
            options.reject_candidate_misconfiguration) {
            bad = true;
        }
        if (!bad && c02.status == WalkStatus::Mapped) {
            if (expected_status != WalkStatus::Mapped) {
                if (c02.permissions.any() ||
                    options.reject_unexpected_mappings) {
                    bad = true;
                }
            } else {
                if (c02.hpa != expected_hpa) {
                    bad = true;
                }
                if ((c02.permissions & ~expected_permissions).any()) {
                    bad = true;
                }
                if (options.exact_permissions &&
                    (expected_permissions & ~c02.permissions).any()) {
                    bad = true;
                }
            }
        }
        if (!bad && expected_status == WalkStatus::Mapped &&
            c02.status != WalkStatus::Mapped &&
            options.require_complete_mappings) {
            bad = true;
        }

        if (bad) {
            return gpa;
        }
    }
    return NO_ADDRESS;
}

} // namespace detail

inline ExhaustiveReport run16BitExhaustiveCrossCheck(
    unsigned cases = 64, std::uint32_t seed = 0x69D5EFA1U) {
    ExhaustiveReport report;
    detail::MiniRng rng{seed};
    const VerifyOptions options = VerifyOptions::exactComposition();

    for (unsigned case_index = 0; case_index < cases; ++case_index) {
        const Geometry geo = Geometry::mini16();
        EptPageTable ept01(geo);
        EptPageTable ept12(geo);
        const Address page_size = geo.blockSize(1);
        const unsigned page_count =
            static_cast<unsigned>(geo.domainEnd() / page_size);

        for (unsigned page = 0; page < page_count; ++page) {
            const Address gpa = static_cast<Address>(page) * page_size;
            const Address hpa =
                static_cast<Address>(rng.below(page_count)) * page_size;
            const Address l1_gpa =
                static_cast<Address>(rng.below(page_count)) * page_size;
            const Permissions p01 = detail::randomLegalPermissions(rng);
            const Permissions p12 = detail::randomLegalPermissions(rng);
            if (ept01.map(gpa, hpa, p01, PageKind::Small) != MapError::Ok ||
                ept12.map(gpa, l1_gpa, p12, PageKind::Small) !=
                    MapError::Ok) {
                report.ok = false;
                report.error = "mini mapping failed";
                return report;
            }
        }

        ComposeResult composed = composeExact(ept01, ept12);
        if (!composed.ok) {
            report.ok = false;
            report.error = composed.message;
            return report;
        }

        const Address target_page =
            static_cast<Address>(rng.below(page_count));
        const Address target_gpa = target_page * page_size;
        const unsigned mutation = case_index % 5U;

        if (mutation == 1U) {
            const WalkResult source12 = ept12.walk(target_gpa);
            if (source12.status != WalkStatus::Mapped) {
                report.ok = false;
                report.error = "mini source walk failed";
                return report;
            }
            const WalkResult source01 = ept01.walk(source12.hpa);
            if (source01.witness.count == 0) {
                report.ok = false;
                report.error = "mini EPT01 witness missing";
                return report;
            }
            const WitnessStep& pte_step =
                source01.witness.steps[source01.witness.count - 1U];
            ept01.rawEntry(pte_step.node_id, pte_step.index) &=
                ~geo.bit_x_usr;

            const WalkResult candidate_walk =
                composed.ept02.walk(target_gpa);
            if (candidate_walk.witness.count == 0) {
                report.ok = false;
                report.error = "mini candidate witness missing";
                return report;
            }
            const WitnessStep& candidate_step =
                candidate_walk.witness
                    .steps[candidate_walk.witness.count - 1U];
            composed.ept02.rawEntry(candidate_step.node_id,
                                    candidate_step.index) |= geo.bit_x_usr;
        } else if (mutation == 2U) {
            const WalkResult walk = composed.ept02.walk(target_gpa);
            const WitnessStep& step =
                walk.witness.steps[walk.witness.count - 1U];
            Address& raw =
                composed.ept02.rawEntry(step.node_id, step.index);
            raw ^= page_size;
        } else if (mutation == 3U) {
            const WalkResult walk = composed.ept02.walk(target_gpa);
            const WitnessStep& step =
                walk.witness.steps[walk.witness.count - 1U];
            Address& raw =
                composed.ept02.rawEntry(step.node_id, step.index);
            raw |= Address(1) << (geo.address_bits + 2U);
        } else if (mutation == 4U) {
            const WalkResult walk = composed.ept02.walk(target_gpa);
            const WitnessStep& step =
                walk.witness.steps[walk.witness.count - 1U];
            Address& raw =
                composed.ept02.rawEntry(step.node_id, step.index);
            raw &= ~geo.bit_r;
            raw |= geo.bit_w;
        }

        const VerificationReport symbolic = verifyCandidate(
            ept01, ept12, composed.ept02, options);
        const Address brute_low = detail::bruteFirstViolation16(
            ept01, ept12, composed.ept02, options);

        if (mutation == 0U) {
            if (!symbolic.clean()) {
                report.ok = false;
                report.error = "clean case reported symbolic violation at " +
                               hex(symbolic.lowest_gpa);
                report.symbolic_lowest = symbolic.lowest_gpa;
                return report;
            }
            if (brute_low != NO_ADDRESS) {
                report.ok = false;
                report.error = "clean case brute violation at " +
                               hex(brute_low);
                report.brute_lowest = brute_low;
                return report;
            }
        } else {
            if (symbolic.lowest_gpa == NO_ADDRESS) {
                report.ok = false;
                report.error = "mutated case missed symbolic violation";
                return report;
            }
            if (symbolic.lowest_gpa != brute_low) {
                report.ok = false;
                report.error = "lowest GPA mismatch: symbolic " +
                               hex(symbolic.lowest_gpa) + " brute " +
                               hex(brute_low);
                report.symbolic_lowest = symbolic.lowest_gpa;
                report.brute_lowest = brute_low;
                return report;
            }
        }

        const std::vector<FlatRange> candidate_ranges =
            composed.ept02.flatten();
        for (unsigned page = 0; page < page_count; ++page) {
            const Address gpa = static_cast<Address>(page) * page_size;
            const WalkResult walk = composed.ept02.walk(gpa);
            const FlatRange* range =
                detail::rangeAt(candidate_ranges, gpa);
            ++report.point_checks;
            if (range == nullptr) {
                report.ok = false;
                report.error = "flatten missing GPA " + hex(gpa);
                return report;
            }
            if (range->status != walk.status ||
                range->permissions != walk.permissions) {
                report.ok = false;
                report.error = "flatten/walk status or perm mismatch at " +
                               hex(gpa);
                return report;
            }
            if (walk.status == WalkStatus::Mapped) {
                const Address symbolic_hpa =
                    range->hpa_base + (gpa - range->lo);
                if (symbolic_hpa != walk.hpa) {
                    report.ok = false;
                    report.error = "flatten/walk HPA mismatch at " +
                                   hex(gpa);
                    return report;
                }
            }
        }

        for (Address byte_address = 0;
             byte_address < geo.domainEnd(); ++byte_address) {
            const WalkResult byte_walk =
                composed.ept02.walk(byte_address);
            const FlatRange* byte_range =
                detail::rangeAt(candidate_ranges, byte_address);
            ++report.byte_checks;
            if (byte_range == nullptr) {
                report.ok = false;
                report.error = "byte flatten missing address " +
                               hex(byte_address);
                return report;
            }
            if (byte_range->status != byte_walk.status ||
                byte_range->permissions != byte_walk.permissions) {
                report.ok = false;
                report.error =
                    "byte flatten/walk status or perm mismatch at " +
                    hex(byte_address);
                return report;
            }
            if (byte_walk.status == WalkStatus::Mapped) {
                const Address symbolic_hpa =
                    byte_range->hpa_base +
                    (byte_address - byte_range->lo);
                if (symbolic_hpa != byte_walk.hpa) {
                    report.ok = false;
                    report.error =
                        "byte flatten/walk HPA mismatch at " +
                        hex(byte_address);
                    return report;
                }
            }
        }

        report.cases++;
        report.pages_checked += page_count;
    }

    return report;
}

} // namespace eptn

#endif
