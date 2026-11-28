#ifndef PKVM_AUDIT_AUDIT_HPP
#define PKVM_AUDIT_AUDIT_HPP

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <limits>
#include <map>
#include <optional>
#include <stdexcept>
#include <string>
#include <utility>
#include <vector>

namespace pkvm {

using EntityId = std::uint32_t;
using Pfn = std::uint64_t;

inline constexpr EntityId kHostEntity = 0U;
inline constexpr EntityId kHypEntity = 1U;
inline constexpr EntityId kFirstGuestEntity = 2U;
inline constexpr EntityId kInvalidEntity =
    std::numeric_limits<EntityId>::max();
inline constexpr std::uint64_t kDefaultPageSize = 4096U;

enum class EntityKind : std::uint8_t {
    Host,
    Hyp,
    Guest,
};

enum class PageState : std::uint8_t {
    PKVM_NOPAGE,
    PKVM_PAGE_OWNED,
    PKVM_PAGE_SHARED_OWNED,
    PKVM_PAGE_SHARED_BORROWED,
};

inline constexpr PageState PKVM_NOPAGE = PageState::PKVM_NOPAGE;
inline constexpr PageState PKVM_PAGE_OWNED = PageState::PKVM_PAGE_OWNED;
inline constexpr PageState PKVM_PAGE_SHARED_OWNED =
    PageState::PKVM_PAGE_SHARED_OWNED;
inline constexpr PageState PKVM_PAGE_SHARED_BORROWED =
    PageState::PKVM_PAGE_SHARED_BORROWED;

enum class Access : std::uint8_t {
    None,
    ReadOnly,
    ReadWrite,
};

enum class ShareKind : std::uint8_t {
    None,
    HostToHyp,
    HostToGuest,
};

enum class HvcOperation : std::uint8_t {
    NONE,
    HOST_SHARE_HYP,
    HOST_DONATE_HYP,
    HOST_SHARE_GUEST,
    HOST_DONATE_GUEST,
    GUEST_UNSHARE_HOST,
    RECLAIM,
};

enum class ErrorCode : std::uint8_t {
    None,
    InvalidOperation,
    UnknownEntity,
    InvalidCaller,
    InvalidTarget,
    InvalidConfiguration,
    InvalidMapping,
    UntrackedPage,
    OutOfRange,
    NotOwner,
    AlreadyShared,
    DoubleBorrow,
    BorrowerStillMapped,
    MissingMapping,
    MappingConflict,
    MappingNotFound,
    PermissionViolation,
    IsolationViolation,
    NotScrubbed,
    InvariantViolation,
    Overflow,
};

struct PhysRange {
    std::uint64_t base{0U};
    std::uint64_t size{0U};

    [[nodiscard]] constexpr std::uint64_t end() const noexcept {
        return base + size;
    }

    [[nodiscard]] constexpr bool empty() const noexcept { return size == 0U; }

    [[nodiscard]] constexpr bool contains(std::uint64_t address) const noexcept {
        return address >= base && address - base < size;
    }

    [[nodiscard]] constexpr bool contains_range(
        std::uint64_t address, std::uint64_t length) const noexcept {
        return length != 0U && length <= size && address >= base &&
               address - base <= size - length;
    }
};

struct EntityConfig {
    EntityId id{kInvalidEntity};
    EntityKind kind{EntityKind::Guest};
    PhysRange stage2_range{};
    bool isolated{true};
    std::string name{};
};

struct PageConfig {
    Pfn pfn{0U};
    EntityId owner{kHostEntity};
    bool confidential{false};
    bool scrubbed{false};
    bool map_to_owner{true};
};

struct MachineConfig {
    std::uint64_t page_size{kDefaultPageSize};
    std::vector<EntityConfig> entities{};
    std::vector<PageConfig> pages{};
};

struct Stage2Mapping {
    std::uint64_t phys_base{0U};
    std::uint64_t size{0U};
    Access access{Access::None};

    [[nodiscard]] constexpr std::uint64_t end() const noexcept {
        return phys_base + size;
    }
};

struct PageInfo {
    Pfn pfn{0U};
    std::uint64_t address{0U};
    PageState state{PKVM_NOPAGE};
    EntityId owner{kInvalidEntity};
    EntityId borrower{kInvalidEntity};
    ShareKind share{ShareKind::None};
    Access owner_access{Access::None};
    Access borrower_access{Access::None};
    bool confidential{false};
    bool scrubbed{false};
};

struct Violation {
    std::uint64_t step{0U};
    Pfn pfn{0U};
    ErrorCode code{ErrorCode::None};
    EntityId entity{kInvalidEntity};
    EntityId owner{kInvalidEntity};
    EntityId borrower{kInvalidEntity};
    PageState state{PKVM_NOPAGE};
    Access actual_access{Access::None};
    std::string detail{};
};

struct AuditResult {
    bool accepted{false};
    ErrorCode error{ErrorCode::None};
    std::uint64_t step{0U};
    std::optional<Violation> witness{};
    std::string detail{};

    [[nodiscard]] bool ok() const noexcept {
        return accepted && error == ErrorCode::None;
    }
};

struct HvcRequest {
    HvcOperation operation{HvcOperation::NONE};
    EntityId caller{kInvalidEntity};
    EntityId target{kInvalidEntity};
    Pfn pfn{0U};
    bool scrub{false};
};

struct HvcResult : AuditResult {
    HvcRequest request{};
};

[[nodiscard]] inline HvcRequest host_share_hyp(Pfn pfn) {
    return HvcRequest{HvcOperation::HOST_SHARE_HYP, kHostEntity, kHypEntity,
                      pfn, false};
}

[[nodiscard]] inline HvcRequest host_donate_hyp(Pfn pfn) {
    return HvcRequest{HvcOperation::HOST_DONATE_HYP, kHostEntity, kHypEntity,
                      pfn, false};
}

[[nodiscard]] inline HvcRequest host_share_guest(Pfn pfn, EntityId guest) {
    return HvcRequest{HvcOperation::HOST_SHARE_GUEST, kHostEntity, guest, pfn,
                      false};
}

[[nodiscard]] inline HvcRequest host_donate_guest(Pfn pfn, EntityId guest) {
    return HvcRequest{HvcOperation::HOST_DONATE_GUEST, kHostEntity, guest, pfn,
                      false};
}

[[nodiscard]] inline HvcRequest guest_unshare_host(Pfn pfn, EntityId guest,
                                                   bool scrub = false) {
    return HvcRequest{HvcOperation::GUEST_UNSHARE_HOST, guest, kHostEntity, pfn,
                      scrub};
}

[[nodiscard]] inline HvcRequest reclaim(Pfn pfn, bool scrub = false) {
    return HvcRequest{HvcOperation::RECLAIM, kHostEntity, kInvalidEntity, pfn,
                      scrub};
}

[[nodiscard]] inline const char* to_string(PageState state) noexcept {
    switch (state) {
    case PageState::PKVM_NOPAGE:
        return "PKVM_NOPAGE";
    case PageState::PKVM_PAGE_OWNED:
        return "PKVM_PAGE_OWNED";
    case PageState::PKVM_PAGE_SHARED_OWNED:
        return "PKVM_PAGE_SHARED_OWNED";
    case PageState::PKVM_PAGE_SHARED_BORROWED:
        return "PKVM_PAGE_SHARED_BORROWED";
    }
    return "PKVM_UNKNOWN";
}

[[nodiscard]] inline const char* to_string(Access access) noexcept {
    switch (access) {
    case Access::None:
        return "NONE";
    case Access::ReadOnly:
        return "RO";
    case Access::ReadWrite:
        return "RW";
    }
    return "UNKNOWN";
}

[[nodiscard]] inline const char* to_string(ErrorCode code) noexcept {
    switch (code) {
    case ErrorCode::None:
        return "NONE";
    case ErrorCode::InvalidOperation:
        return "INVALID_OPERATION";
    case ErrorCode::UnknownEntity:
        return "UNKNOWN_ENTITY";
    case ErrorCode::InvalidCaller:
        return "INVALID_CALLER";
    case ErrorCode::InvalidTarget:
        return "INVALID_TARGET";
    case ErrorCode::InvalidConfiguration:
        return "INVALID_CONFIGURATION";
    case ErrorCode::InvalidMapping:
        return "INVALID_MAPPING";
    case ErrorCode::UntrackedPage:
        return "UNTRACKED_PAGE";
    case ErrorCode::OutOfRange:
        return "OUT_OF_RANGE";
    case ErrorCode::NotOwner:
        return "NOT_OWNER";
    case ErrorCode::AlreadyShared:
        return "ALREADY_SHARED";
    case ErrorCode::DoubleBorrow:
        return "DOUBLE_BORROW";
    case ErrorCode::BorrowerStillMapped:
        return "BORROWER_STILL_MAPPED";
    case ErrorCode::MissingMapping:
        return "MISSING_MAPPING";
    case ErrorCode::MappingConflict:
        return "MAPPING_CONFLICT";
    case ErrorCode::MappingNotFound:
        return "MAPPING_NOT_FOUND";
    case ErrorCode::PermissionViolation:
        return "PERMISSION_VIOLATION";
    case ErrorCode::IsolationViolation:
        return "ISOLATION_VIOLATION";
    case ErrorCode::NotScrubbed:
        return "NOT_SCRUBBED";
    case ErrorCode::InvariantViolation:
        return "INVARIANT_VIOLATION";
    case ErrorCode::Overflow:
        return "OVERFLOW";
    }
    return "UNKNOWN";
}

class Machine {
  public:
    explicit Machine(MachineConfig config);

    Machine(const Machine&) = default;
    Machine(Machine&&) noexcept = default;
    Machine& operator=(const Machine&) = default;
    Machine& operator=(Machine&&) noexcept = default;
    ~Machine() = default;

    [[nodiscard]] std::uint64_t page_size() const noexcept {
        return page_size_;
    }

    [[nodiscard]] std::uint64_t current_step() const noexcept {
        return step_;
    }

    [[nodiscard]] std::optional<EntityConfig> entity(EntityId id) const;
    [[nodiscard]] std::vector<EntityConfig> entities() const;
    [[nodiscard]] std::optional<PageInfo> page(Pfn pfn) const;
    [[nodiscard]] std::vector<PageInfo> pages() const;
    [[nodiscard]] std::optional<PageState> state_for(EntityId observer,
                                                     Pfn pfn) const;
    [[nodiscard]] std::optional<Access> access_for(EntityId id, Pfn pfn) const;
    [[nodiscard]] std::vector<Stage2Mapping> mappings(EntityId id) const;

    HvcResult execute(const HvcRequest& request);

    // Deliberately injects a mapping without the HVC state transition. This is
    // for adversarial audit fixtures, e.g. simulating a stale Host mapping.
    AuditResult simulate_stage2_map(EntityId id, PhysRange range,
                                    Access access);
    AuditResult simulate_stage2_map_page(EntityId id, Pfn pfn, Access access);
    AuditResult simulate_stage2_unmap(EntityId id, PhysRange range);
    AuditResult simulate_stage2_unmap_page(EntityId id, Pfn pfn);

    // Marks a confidential page as wiped. It is deliberately separate from
    // RECLAIM so a missing scrub can be detected at the violating step.
    AuditResult set_scrubbed(Pfn pfn, bool scrubbed);
    AuditResult scrub(Pfn pfn) { return set_scrubbed(pfn, true); }

    [[nodiscard]] AuditResult verify() const;
    [[nodiscard]] std::optional<Violation> first_violation() const;
    [[nodiscard]] const std::vector<HvcResult>& hvc_history() const noexcept {
        return history_;
    }

  private:
    struct PageRecord {
        Pfn pfn{0U};
        std::uint64_t address{0U};
        PageState state{PKVM_PAGE_OWNED};
        EntityId owner{kHostEntity};
        EntityId borrower{kInvalidEntity};
        ShareKind share{ShareKind::None};
        bool confidential{false};
        bool scrubbed{false};
    };

    struct EntityRecord {
        EntityConfig config{};
        std::vector<Stage2Mapping> table{};
    };

    struct Snapshot {
        std::vector<std::vector<Stage2Mapping>> tables{};
        std::vector<PageRecord> pages{};
    };

    [[nodiscard]] EntityRecord* find_entity(EntityId id);
    [[nodiscard]] const EntityRecord* find_entity(EntityId id) const;
    [[nodiscard]] PageRecord* find_page(Pfn pfn);
    [[nodiscard]] const PageRecord* find_page(Pfn pfn) const;

    [[nodiscard]] bool pfn_address(Pfn pfn, std::uint64_t* address) const;
    [[nodiscard]] bool address_in_range(const EntityRecord& entity,
                                        std::uint64_t address) const;
    [[nodiscard]] bool page_in_range(const EntityRecord& entity,
                                     const PageRecord& page) const;
    [[nodiscard]] std::optional<Access> table_access(const EntityRecord& entity,
                                                     Pfn pfn) const;
    [[nodiscard]] ErrorCode insert_mapping(EntityRecord& entity,
                                           std::uint64_t base,
                                           std::uint64_t size, Access access);
    void erase_mapping(EntityRecord& entity, std::uint64_t base,
                       std::uint64_t size);
    [[nodiscard]] Snapshot snapshot() const;
    void restore(const Snapshot& snapshot_value);

    [[nodiscard]] HvcResult reject(const HvcRequest& request, ErrorCode code,
                                   EntityId entity, const PageRecord* page,
                                   std::string detail);
    [[nodiscard]] HvcResult reject_violation(const HvcRequest& request,
                                             Violation violation);
    void remember(const Violation& violation);

    [[nodiscard]] AuditResult verify_at(std::uint64_t step) const;
    [[nodiscard]] AuditResult reject_audit(ErrorCode code, std::uint64_t step,
                                           Pfn pfn, EntityId entity,
                                           std::string detail) const;

    [[nodiscard]] static bool add_overflow(std::uint64_t lhs,
                                           std::uint64_t rhs) noexcept;
    [[nodiscard]] static bool ranges_overlap(std::uint64_t first_base,
                                             std::uint64_t first_size,
                                             std::uint64_t second_base,
                                             std::uint64_t second_size) noexcept;
    [[nodiscard]] static bool violation_less(const Violation& lhs,
                                             const Violation& rhs) noexcept;

    std::uint64_t page_size_{kDefaultPageSize};
    std::vector<EntityRecord> entities_{};
    std::map<EntityId, std::size_t> entity_index_{};
    std::vector<PageRecord> pages_{};
    std::map<Pfn, std::size_t> page_index_{};
    std::uint64_t step_{0U};
    std::vector<HvcResult> history_{};
    std::vector<Violation> violations_{};
};

namespace detail {

[[nodiscard]] inline bool valid_access(Access access) noexcept {
    return access == Access::ReadOnly || access == Access::ReadWrite;
}

[[nodiscard]] inline bool access_exceeds(Access actual, Access limit) noexcept {
    return static_cast<std::uint8_t>(actual) >
           static_cast<std::uint8_t>(limit);
}

[[nodiscard]] inline std::uint64_t checked_end(std::uint64_t base,
                                               std::uint64_t size,
                                               bool* overflow) noexcept {
    if (base > std::numeric_limits<std::uint64_t>::max() - size) {
        *overflow = true;
        return 0U;
    }
    *overflow = false;
    return base + size;
}

} // namespace detail

inline bool Machine::add_overflow(std::uint64_t lhs,
                                  std::uint64_t rhs) noexcept {
    return lhs > std::numeric_limits<std::uint64_t>::max() - rhs;
}

inline bool Machine::ranges_overlap(std::uint64_t first_base,
                                    std::uint64_t first_size,
                                    std::uint64_t second_base,
                                    std::uint64_t second_size) noexcept {
    if (first_size == 0U || second_size == 0U) {
        return false;
    }
    return first_base < second_base + second_size &&
           second_base < first_base + first_size;
}

inline bool Machine::violation_less(const Violation& lhs,
                                    const Violation& rhs) noexcept {
    if (lhs.step != rhs.step) {
        return lhs.step < rhs.step;
    }
    if (lhs.pfn != rhs.pfn) {
        return lhs.pfn < rhs.pfn;
    }
    if (lhs.entity != rhs.entity) {
        return lhs.entity < rhs.entity;
    }
    return static_cast<std::uint8_t>(lhs.code) <
           static_cast<std::uint8_t>(rhs.code);
}

inline Machine::Machine(MachineConfig config) : page_size_(config.page_size) {
    if (page_size_ == 0U) {
        throw std::invalid_argument("pKVM page size must be non-zero");
    }
    if (config.entities.size() > static_cast<std::size_t>(kInvalidEntity)) {
        throw std::invalid_argument("too many pKVM entities");
    }

    bool found_host = false;
    bool found_hyp = false;
    for (EntityConfig entity_config : config.entities) {
        if (entity_config.id == kInvalidEntity) {
            throw std::invalid_argument("invalid pKVM entity id");
        }
        if (entity_config.stage2_range.size == 0U) {
            throw std::invalid_argument("entity stage-2 range is empty");
        }
        if (add_overflow(entity_config.stage2_range.base,
                         entity_config.stage2_range.size)) {
            throw std::invalid_argument("entity stage-2 range overflows");
        }
        if (entity_config.id == kHostEntity) {
            if (entity_config.kind != EntityKind::Host || found_host) {
                throw std::invalid_argument("Host must be entity 0");
            }
            found_host = true;
        } else if (entity_config.id == kHypEntity) {
            if (entity_config.kind != EntityKind::Hyp || found_hyp) {
                throw std::invalid_argument("Hyp must be entity 1");
            }
            found_hyp = true;
        } else {
            if (entity_config.kind != EntityKind::Guest ||
                !entity_config.isolated) {
                throw std::invalid_argument(
                    "guest entities must be isolated and have ids >= 2");
            }
        }

        EntityRecord record{};
        record.config = std::move(entity_config);
        entities_.push_back(std::move(record));
    }
    if (!found_host || !found_hyp) {
        throw std::invalid_argument("machine requires Host and Hyp entities");
    }

    std::sort(entities_.begin(), entities_.end(),
              [](const EntityRecord& lhs, const EntityRecord& rhs) {
                  return lhs.config.id < rhs.config.id;
              });
    for (std::size_t index = 0U; index < entities_.size(); ++index) {
        const EntityId id = entities_[index].config.id;
        if (entity_index_.find(id) != entity_index_.end()) {
            throw std::invalid_argument("duplicate pKVM entity id");
        }
        entity_index_.emplace(id, index);
    }

    pages_.reserve(config.pages.size());
    for (const PageConfig& page_config : config.pages) {
        PageRecord page{};
        page.pfn = page_config.pfn;
        if (!pfn_address(page.pfn, &page.address)) {
            throw std::invalid_argument("PFN address overflows");
        }
        if (add_overflow(page.address, page_size_)) {
            throw std::invalid_argument("page address overflows");
        }
        page.state = PKVM_PAGE_OWNED;
        page.owner = page_config.owner;
        page.confidential = page_config.confidential;
        page.scrubbed = page_config.scrubbed || !page_config.confidential;
        if (page_index_.find(page.pfn) != page_index_.end()) {
            throw std::invalid_argument("duplicate PFN in machine config");
        }
        const EntityRecord* owner = find_entity(page.owner);
        if (owner == nullptr) {
            throw std::invalid_argument("page owner is not a known entity");
        }
        if (!page_in_range(*owner, page)) {
            throw std::invalid_argument("page is outside owner stage-2 range");
        }

        pages_.push_back(page);
        page_index_.emplace(page.pfn, pages_.size() - 1U);
        if (page_config.map_to_owner) {
            const ErrorCode mapping_error =
                insert_mapping(*find_entity(page.owner), page.address, page_size_,
                               Access::ReadWrite);
            if (mapping_error != ErrorCode::None) {
                throw std::invalid_argument("cannot map initial page owner");
            }
        }
    }

    std::sort(pages_.begin(), pages_.end(),
              [](const PageRecord& lhs, const PageRecord& rhs) {
                  return lhs.pfn < rhs.pfn;
              });
    page_index_.clear();
    for (std::size_t index = 0U; index < pages_.size(); ++index) {
        page_index_.emplace(pages_[index].pfn, index);
    }
}

inline Machine::EntityRecord* Machine::find_entity(EntityId id) {
    const auto found = entity_index_.find(id);
    if (found == entity_index_.end()) {
        return nullptr;
    }
    return &entities_[found->second];
}

inline const Machine::EntityRecord* Machine::find_entity(EntityId id) const {
    const auto found = entity_index_.find(id);
    if (found == entity_index_.end()) {
        return nullptr;
    }
    return &entities_[found->second];
}

inline Machine::PageRecord* Machine::find_page(Pfn pfn) {
    const auto found = page_index_.find(pfn);
    if (found == page_index_.end()) {
        return nullptr;
    }
    return &pages_[found->second];
}

inline const Machine::PageRecord* Machine::find_page(Pfn pfn) const {
    const auto found = page_index_.find(pfn);
    if (found == page_index_.end()) {
        return nullptr;
    }
    return &pages_[found->second];
}

inline bool Machine::pfn_address(Pfn pfn, std::uint64_t* address) const {
    if (address == nullptr ||
        pfn > std::numeric_limits<std::uint64_t>::max() / page_size_) {
        return false;
    }
    *address = pfn * page_size_;
    return true;
}

inline bool Machine::page_in_range(const EntityRecord& entity_record,
                                   const PageRecord& page) const {
    return address_in_range(entity_record, page.address);
}

inline bool Machine::address_in_range(const EntityRecord& entity_record,
                                      std::uint64_t address) const {
    const PhysRange& range = entity_record.config.stage2_range;
    return address >= range.base && address <= range.end() &&
           page_size_ <= range.end() - address;
}

inline std::optional<Access>
Machine::table_access(const EntityRecord& entity_record, Pfn pfn) const {
    std::uint64_t address = 0U;
    if (!pfn_address(pfn, &address)) {
        return std::nullopt;
    }
    for (const Stage2Mapping& mapping : entity_record.table) {
        if (mapping.phys_base <= address &&
            address <= mapping.end() &&
            page_size_ <= mapping.end() - address) {
            return mapping.access;
        }
    }
    return std::nullopt;
}

inline ErrorCode Machine::insert_mapping(EntityRecord& entity_record,
                                         std::uint64_t base,
                                         std::uint64_t size, Access access) {
    if (!detail::valid_access(access) || size == 0U ||
        base % page_size_ != 0U || size % page_size_ != 0U) {
        return ErrorCode::InvalidMapping;
    }
    bool overflow = false;
    const std::uint64_t mapping_end =
        detail::checked_end(base, size, &overflow);
    if (overflow) {
        return ErrorCode::Overflow;
    }
    const PhysRange& range = entity_record.config.stage2_range;
    if (mapping_end > range.end() || base < range.base) {
        return ErrorCode::OutOfRange;
    }
    for (const Stage2Mapping& mapping : entity_record.table) {
        if (ranges_overlap(base, size, mapping.phys_base, mapping.size)) {
            return ErrorCode::MappingConflict;
        }
    }

    Stage2Mapping mapping{};
    mapping.phys_base = base;
    mapping.size = size;
    mapping.access = access;
    entity_record.table.push_back(mapping);
    std::sort(entity_record.table.begin(), entity_record.table.end(),
              [](const Stage2Mapping& lhs, const Stage2Mapping& rhs) {
                  return lhs.phys_base < rhs.phys_base;
              });
    return ErrorCode::None;
}

inline void Machine::erase_mapping(EntityRecord& entity_record,
                                   std::uint64_t base, std::uint64_t size) {
    if (size == 0U) {
        return;
    }
    bool overflow = false;
    const std::uint64_t erase_end = detail::checked_end(base, size, &overflow);
    if (overflow) {
        return;
    }

    std::vector<Stage2Mapping> kept{};
    kept.reserve(entity_record.table.size() + 1U);
    for (const Stage2Mapping& mapping : entity_record.table) {
        if (!ranges_overlap(mapping.phys_base, mapping.size, base, size)) {
            kept.push_back(mapping);
            continue;
        }
        if (mapping.phys_base < base) {
            Stage2Mapping left = mapping;
            left.size = base - mapping.phys_base;
            kept.push_back(left);
        }
        if (mapping.end() > erase_end) {
            Stage2Mapping right = mapping;
            right.phys_base = erase_end;
            right.size = mapping.end() - erase_end;
            kept.push_back(right);
        }
    }
    entity_record.table = std::move(kept);
}

inline Machine::Snapshot Machine::snapshot() const {
    Snapshot state{};
    state.tables.reserve(entities_.size());
    for (const EntityRecord& entity_record : entities_) {
        state.tables.push_back(entity_record.table);
    }
    state.pages = pages_;
    return state;
}

inline void Machine::restore(const Snapshot& snapshot_value) {
    if (snapshot_value.tables.size() != entities_.size() ||
        snapshot_value.pages.size() != pages_.size()) {
        return;
    }
    for (std::size_t index = 0U; index < entities_.size(); ++index) {
        entities_[index].table = snapshot_value.tables[index];
    }
    pages_ = snapshot_value.pages;
}

inline std::optional<EntityConfig> Machine::entity(EntityId id) const {
    const EntityRecord* entity_record = find_entity(id);
    if (entity_record == nullptr) {
        return std::nullopt;
    }
    return entity_record->config;
}

inline std::vector<EntityConfig> Machine::entities() const {
    std::vector<EntityConfig> output{};
    output.reserve(entities_.size());
    for (const EntityRecord& entity_record : entities_) {
        output.push_back(entity_record.config);
    }
    return output;
}

inline std::optional<PageInfo> Machine::page(Pfn pfn) const {
    const PageRecord* page_record = find_page(pfn);
    if (page_record == nullptr) {
        return std::nullopt;
    }
    PageInfo info{};
    info.pfn = page_record->pfn;
    info.address = page_record->address;
    info.state = page_record->state;
    info.owner = page_record->owner;
    info.borrower = page_record->borrower;
    info.share = page_record->share;
    info.confidential = page_record->confidential;
    info.scrubbed = page_record->scrubbed;
    const EntityRecord* owner = find_entity(page_record->owner);
    if (owner != nullptr) {
        const std::optional<Access> access = table_access(*owner, pfn);
        info.owner_access = access.value_or(Access::None);
    }
    const EntityRecord* borrower = find_entity(page_record->borrower);
    if (borrower != nullptr) {
        const std::optional<Access> access = table_access(*borrower, pfn);
        info.borrower_access = access.value_or(Access::None);
    }
    return info;
}

inline std::vector<PageInfo> Machine::pages() const {
    std::vector<PageInfo> output{};
    output.reserve(pages_.size());
    for (const PageRecord& page_record : pages_) {
        const std::optional<PageInfo> info = page(page_record.pfn);
        if (info.has_value()) {
            output.push_back(*info);
        }
    }
    return output;
}

inline std::optional<PageState> Machine::state_for(EntityId observer,
                                                   Pfn pfn) const {
    const PageRecord* page_record = find_page(pfn);
    if (page_record == nullptr || find_entity(observer) == nullptr) {
        return std::nullopt;
    }
    if (page_record->state == PKVM_PAGE_OWNED) {
        return page_record->owner == observer ? PKVM_PAGE_OWNED : PKVM_NOPAGE;
    }
    if (page_record->state == PKVM_PAGE_SHARED_OWNED) {
        if (page_record->owner == observer) {
            return PKVM_PAGE_SHARED_OWNED;
        }
        if (page_record->borrower == observer) {
            return PKVM_PAGE_SHARED_BORROWED;
        }
        return PKVM_NOPAGE;
    }
    return PKVM_NOPAGE;
}

inline std::optional<Access> Machine::access_for(EntityId id, Pfn pfn) const {
    const EntityRecord* entity_record = find_entity(id);
    if (entity_record == nullptr || find_page(pfn) == nullptr) {
        return std::nullopt;
    }
    return table_access(*entity_record, pfn);
}

inline std::vector<Stage2Mapping> Machine::mappings(EntityId id) const {
    const EntityRecord* entity_record = find_entity(id);
    if (entity_record == nullptr) {
        return {};
    }
    return entity_record->table;
}

inline AuditResult Machine::reject_audit(ErrorCode code, std::uint64_t step,
                                         Pfn pfn, EntityId entity,
                                         std::string detail) const {
    AuditResult result{};
    result.accepted = false;
    result.error = code;
    result.step = step;
    result.detail = std::move(detail);
    Violation violation{};
    violation.step = step;
    violation.pfn = pfn;
    violation.code = code;
    violation.entity = entity;
    violation.detail = result.detail;
    result.witness = violation;
    return result;
}

inline void Machine::remember(const Violation& violation) {
    violations_.push_back(violation);
}

inline HvcResult Machine::reject(const HvcRequest& request, ErrorCode code,
                                 EntityId entity, const PageRecord* page_record,
                                 std::string detail) {
    HvcResult result{};
    result.request = request;
    result.accepted = false;
    result.error = code;
    result.step = step_;
    result.detail = std::move(detail);

    Violation violation{};
    violation.step = step_;
    violation.pfn = request.pfn;
    violation.code = code;
    violation.entity = entity;
    violation.detail = result.detail;
    if (page_record != nullptr) {
        violation.owner = page_record->owner;
        violation.borrower = page_record->borrower;
        violation.state = page_record->state;
        const EntityRecord* access_entity = find_entity(entity);
        if (access_entity != nullptr) {
            violation.actual_access =
                table_access(*access_entity, page_record->pfn)
                    .value_or(Access::None);
        }
    }
    result.witness = violation;
    remember(violation);
    history_.push_back(result);
    return result;
}

inline HvcResult Machine::reject_violation(const HvcRequest& request,
                                           Violation violation) {
    HvcResult result{};
    result.request = request;
    result.accepted = false;
    result.error = violation.code;
    result.step = step_;
    result.detail = violation.detail;
    remember(violation);
    result.witness = std::move(violation);
    history_.push_back(result);
    return result;
}

inline HvcResult Machine::execute(const HvcRequest& request) {
    ++step_;
    const Snapshot before = snapshot();

    switch (request.operation) {
    case HvcOperation::NONE:
        return reject(request, ErrorCode::InvalidOperation, request.caller,
                      nullptr, "no HVC operation was requested");
    case HvcOperation::HOST_SHARE_HYP:
    case HvcOperation::HOST_DONATE_HYP:
    case HvcOperation::HOST_SHARE_GUEST:
    case HvcOperation::HOST_DONATE_GUEST:
    case HvcOperation::GUEST_UNSHARE_HOST:
    case HvcOperation::RECLAIM:
        break;
    default:
        return reject(request, ErrorCode::InvalidOperation, request.caller,
                      nullptr, "unknown HVC operation value");
    }

    const EntityRecord* caller = find_entity(request.caller);
    if (caller == nullptr) {
        return reject(request, ErrorCode::UnknownEntity, request.caller,
                      nullptr, "caller entity does not exist");
    }

    std::uint64_t request_address = 0U;
    if (!pfn_address(request.pfn, &request_address)) {
        return reject(request, ErrorCode::OutOfRange, request.caller, nullptr,
                      "PFN multiplication overflows the physical address");
    }
    const bool host_call =
        request.operation == HvcOperation::HOST_SHARE_HYP ||
        request.operation == HvcOperation::HOST_DONATE_HYP ||
        request.operation == HvcOperation::HOST_SHARE_GUEST ||
        request.operation == HvcOperation::HOST_DONATE_GUEST ||
        request.operation == HvcOperation::RECLAIM;
    if (host_call && request.caller != kHostEntity) {
        return reject(request, ErrorCode::InvalidCaller, request.caller,
                      find_page(request.pfn),
                      "only Host may issue share, donate or reclaim HVCs");
    }
    if (request.operation == HvcOperation::GUEST_UNSHARE_HOST &&
        (request.caller < kFirstGuestEntity ||
         caller->config.kind != EntityKind::Guest)) {
        return reject(request, ErrorCode::InvalidCaller, request.caller,
                      find_page(request.pfn),
                      "GUEST_UNSHARE_HOST must be issued by a guest");
    }

    const EntityRecord* host = find_entity(kHostEntity);
    const EntityRecord* hyp = find_entity(kHypEntity);
    const EntityRecord* guest_target = nullptr;
    const EntityRecord* page_owner = nullptr;

    switch (request.operation) {
    case HvcOperation::HOST_SHARE_HYP:
        if (request.target != kInvalidEntity && request.target != kHypEntity) {
            return reject(request, ErrorCode::InvalidTarget, request.target,
                          find_page(request.pfn),
                          "HOST_SHARE_HYP target must be Hyp");
        }
        break;
    case HvcOperation::HOST_DONATE_HYP:
        if (request.target != kInvalidEntity && request.target != kHypEntity) {
            return reject(request, ErrorCode::InvalidTarget, request.target,
                          find_page(request.pfn),
                          "HOST_DONATE_HYP target must be Hyp");
        }
        break;
    case HvcOperation::HOST_SHARE_GUEST:
    case HvcOperation::HOST_DONATE_GUEST:
        guest_target = find_entity(request.target);
        if (guest_target == nullptr ||
            guest_target->config.kind != EntityKind::Guest ||
            !guest_target->config.isolated) {
            return reject(request, ErrorCode::InvalidTarget, request.target,
                          find_page(request.pfn),
                          "guest target must be an isolated pVM");
        }
        break;
    case HvcOperation::GUEST_UNSHARE_HOST:
        if (request.target != kInvalidEntity && request.target != kHostEntity) {
            return reject(request, ErrorCode::InvalidTarget, request.target,
                          find_page(request.pfn),
                          "GUEST_UNSHARE_HOST target must be Host");
        }
        break;
    case HvcOperation::RECLAIM:
        break;
    case HvcOperation::NONE:
        break;
    }

    if (!address_in_range(*caller, request_address)) {
        return reject(request, ErrorCode::OutOfRange, request.caller, nullptr,
                      "PFN is outside caller stage-2 range");
    }

    PageRecord* page_record = find_page(request.pfn);
    if (page_record == nullptr) {
        return reject(request, ErrorCode::UntrackedPage, request.caller, nullptr,
                      "PFN is not tracked by this machine");
    }
    page_owner = find_entity(page_record->owner);

    if ((request.operation == HvcOperation::HOST_SHARE_GUEST ||
         request.operation == HvcOperation::HOST_DONATE_GUEST) &&
        guest_target != nullptr && !page_in_range(*guest_target, *page_record)) {
        return reject(request, ErrorCode::OutOfRange, request.target,
                      page_record, "PFN is outside guest stage-2 range");
    }
    if ((request.operation == HvcOperation::HOST_SHARE_HYP ||
         request.operation == HvcOperation::HOST_DONATE_HYP) &&
        hyp != nullptr && !page_in_range(*hyp, *page_record)) {
        return reject(request, ErrorCode::OutOfRange, kHypEntity, page_record,
                      "PFN is outside Hyp stage-2 range");
    }
    if (request.operation == HvcOperation::GUEST_UNSHARE_HOST &&
        host != nullptr && !page_in_range(*host, *page_record)) {
        return reject(request, ErrorCode::OutOfRange, kHostEntity, page_record,
                      "PFN is outside Host stage-2 range");
    }
    if (request.operation == HvcOperation::RECLAIM && page_owner != nullptr &&
        !page_in_range(*page_owner, *page_record)) {
        return reject(request, ErrorCode::OutOfRange, page_record->owner,
                      page_record, "PFN is outside current owner stage-2 range");
    }

    switch (request.operation) {
    case HvcOperation::HOST_SHARE_HYP: {
        if (page_record->state != PKVM_PAGE_OWNED) {
            return reject(request, ErrorCode::AlreadyShared, request.caller,
                          page_record,
                          "HOST_SHARE_HYP requires a private owned page");
        }
        if (page_record->owner != kHostEntity) {
            return reject(request, ErrorCode::NotOwner, request.caller,
                          page_record, "Host is not the page owner");
        }
        if (!table_access(*caller, request.pfn).has_value()) {
            return reject(request, ErrorCode::MissingMapping, request.caller,
                          page_record,
                          "owner page is not mapped in Host stage-2");
        }
        if (table_access(*hyp, request.pfn).has_value()) {
            return reject(request, ErrorCode::MappingConflict, kHypEntity,
                          page_record,
                          "Hyp already has a mapping for this private page");
        }
        for (const EntityRecord& entity_record : entities_) {
            if (entity_record.config.id != kHostEntity &&
                table_access(entity_record, request.pfn).has_value()) {
                return reject(request, ErrorCode::IsolationViolation,
                              entity_record.config.id, page_record,
                              "third entity maps a private Host page");
            }
        }
        const ErrorCode mapping_error =
            insert_mapping(*find_entity(kHypEntity), page_record->address,
                           page_size_, Access::ReadOnly);
        if (mapping_error != ErrorCode::None) {
            restore(before);
            return reject(request, mapping_error, kHypEntity,
                          find_page(request.pfn),
                          "failed to install Hyp read-only mapping");
        }
        page_record->state = PKVM_PAGE_SHARED_OWNED;
        page_record->borrower = kHypEntity;
        page_record->share = ShareKind::HostToHyp;
        break;
    }
    case HvcOperation::HOST_DONATE_HYP: {
        if (page_record->state != PKVM_PAGE_OWNED) {
            return reject(request, ErrorCode::AlreadyShared, request.caller,
                          page_record,
                          "HOST_DONATE_HYP requires a private owned page");
        }
        if (page_record->owner != kHostEntity) {
            return reject(request, ErrorCode::NotOwner, request.caller,
                          page_record, "Host is not the page owner");
        }
        if (table_access(*hyp, request.pfn).has_value()) {
            return reject(request, ErrorCode::MappingConflict, kHypEntity,
                          page_record,
                          "Hyp already maps a page still owned by Host");
        }
        for (const EntityRecord& entity_record : entities_) {
            if (entity_record.config.id != kHostEntity &&
                table_access(entity_record, request.pfn).has_value()) {
                return reject(request, ErrorCode::IsolationViolation,
                              entity_record.config.id, page_record,
                              "third entity maps a page before donation");
            }
        }
        erase_mapping(*find_entity(kHostEntity), page_record->address,
                      page_size_);
        const ErrorCode mapping_error =
            insert_mapping(*find_entity(kHypEntity), page_record->address,
                           page_size_, Access::ReadWrite);
        if (mapping_error != ErrorCode::None) {
            restore(before);
            return reject(request, mapping_error, kHypEntity,
                          find_page(request.pfn),
                          "failed to install Hyp owner mapping");
        }
        page_record->state = PKVM_PAGE_OWNED;
        page_record->owner = kHypEntity;
        page_record->borrower = kInvalidEntity;
        page_record->share = ShareKind::None;
        break;
    }
    case HvcOperation::HOST_SHARE_GUEST: {
        if (page_record->state != PKVM_PAGE_OWNED) {
            return reject(request, ErrorCode::AlreadyShared, request.caller,
                          page_record,
                          "HOST_SHARE_GUEST requires a private owned page");
        }
        if (page_record->owner != kHostEntity) {
            return reject(request, ErrorCode::NotOwner, request.caller,
                          page_record, "Host is not the page owner");
        }
        if (!table_access(*caller, request.pfn).has_value()) {
            return reject(request, ErrorCode::MissingMapping, request.caller,
                          page_record,
                          "owner page is not mapped in Host stage-2");
        }
        if (guest_target == nullptr ||
            table_access(*guest_target, request.pfn).has_value()) {
            return reject(request, ErrorCode::DoubleBorrow, request.target,
                          page_record,
                          "guest already maps the private page");
        }
        for (const EntityRecord& entity_record : entities_) {
            if (entity_record.config.id != kHostEntity &&
                table_access(entity_record, request.pfn).has_value()) {
                return reject(request, ErrorCode::IsolationViolation,
                              entity_record.config.id, page_record,
                              "third entity maps a private Host page");
            }
        }
        const ErrorCode mapping_error =
            insert_mapping(*find_entity(request.target), page_record->address,
                           page_size_, Access::ReadOnly);
        if (mapping_error != ErrorCode::None) {
            restore(before);
            return reject(request, mapping_error, request.target,
                          find_page(request.pfn),
                          "failed to install guest read-only mapping");
        }
        page_record->state = PKVM_PAGE_SHARED_OWNED;
        page_record->owner = kHostEntity;
        page_record->borrower = request.target;
        page_record->share = ShareKind::HostToGuest;
        if (page_record->confidential) {
            page_record->scrubbed = false;
        }
        break;
    }
    case HvcOperation::HOST_DONATE_GUEST: {
        if (page_record->state != PKVM_PAGE_OWNED) {
            return reject(request, ErrorCode::AlreadyShared, request.caller,
                          page_record,
                          "HOST_DONATE_GUEST requires a private owned page");
        }
        if (page_record->owner != kHostEntity) {
            return reject(request, ErrorCode::NotOwner, request.caller,
                          page_record, "Host is not the page owner");
        }
        if (guest_target == nullptr ||
            table_access(*guest_target, request.pfn).has_value()) {
            return reject(request, ErrorCode::DoubleBorrow, request.target,
                          page_record,
                          "guest already maps the page before donation");
        }
        for (const EntityRecord& entity_record : entities_) {
            if (entity_record.config.id != kHostEntity &&
                table_access(entity_record, request.pfn).has_value()) {
                return reject(request, ErrorCode::IsolationViolation,
                              entity_record.config.id, page_record,
                              "third entity maps a page before donation");
            }
        }
        erase_mapping(*find_entity(kHostEntity), page_record->address,
                      page_size_);
        const ErrorCode mapping_error =
            insert_mapping(*find_entity(request.target), page_record->address,
                           page_size_, Access::ReadWrite);
        if (mapping_error != ErrorCode::None) {
            restore(before);
            return reject(request, mapping_error, request.target,
                          find_page(request.pfn),
                          "failed to install guest owner mapping");
        }
        page_record->state = PKVM_PAGE_OWNED;
        page_record->owner = request.target;
        page_record->borrower = kInvalidEntity;
        page_record->share = ShareKind::None;
        if (page_record->confidential) {
            page_record->scrubbed = false;
        }
        break;
    }
    case HvcOperation::GUEST_UNSHARE_HOST: {
        if (page_record->state != PKVM_PAGE_SHARED_OWNED ||
            page_record->share != ShareKind::HostToGuest ||
            page_record->owner != kHostEntity ||
            page_record->borrower != request.caller) {
            return reject(request, ErrorCode::NotOwner, request.caller,
                          page_record,
                          "caller is not the guest borrower of a Host share");
        }
        if (!table_access(*find_entity(kHostEntity), request.pfn).has_value()) {
            return reject(request, ErrorCode::MissingMapping, kHostEntity,
                          page_record,
                          "Host owner mapping disappeared before unshare");
        }
        if (!table_access(*caller, request.pfn).has_value()) {
            return reject(request, ErrorCode::MissingMapping, request.caller,
                          page_record,
                          "guest borrower mapping is already absent");
        }
        for (const EntityRecord& entity_record : entities_) {
            if (entity_record.config.id != kHostEntity &&
                entity_record.config.id != request.caller &&
                table_access(entity_record, request.pfn).has_value()) {
                return reject(request, ErrorCode::IsolationViolation,
                              entity_record.config.id, page_record,
                              "another entity still maps the shared page");
            }
        }
        if (page_record->confidential && !page_record->scrubbed &&
            !request.scrub) {
            return reject(request, ErrorCode::NotScrubbed, request.caller,
                          page_record,
                          "confidential page must be scrubbed before unshare");
        }
        erase_mapping(*find_entity(request.caller), page_record->address,
                      page_size_);
        page_record->state = PKVM_PAGE_OWNED;
        page_record->owner = kHostEntity;
        page_record->borrower = kInvalidEntity;
        page_record->share = ShareKind::None;
        if (page_record->confidential && request.scrub) {
            page_record->scrubbed = true;
        }
        break;
    }
    case HvcOperation::RECLAIM: {
        if (page_record->state == PKVM_PAGE_SHARED_OWNED ||
            page_record->borrower != kInvalidEntity) {
            return reject(request, ErrorCode::BorrowerStillMapped,
                          page_record->borrower, page_record,
                          "all borrowers must unshare before reclaim");
        }
        if (page_record->state != PKVM_PAGE_OWNED) {
            return reject(request, ErrorCode::InvariantViolation,
                          request.caller, page_record,
                          "only an exclusively owned page can be reclaimed");
        }
        if (page_record->owner == kHostEntity) {
            return reject(request, ErrorCode::NotOwner, request.caller,
                          page_record, "page is already owned by Host");
        }
        if (request.target != kInvalidEntity &&
            request.target != kHostEntity &&
            request.target != page_record->owner) {
            return reject(request, ErrorCode::InvalidTarget, request.target,
                          page_record,
                          "RECLAIM target must be Host or the current owner");
        }
        if (page_owner == nullptr) {
            return reject(request, ErrorCode::UnknownEntity,
                          page_record->owner, page_record,
                          "current page owner does not exist");
        }
        for (const EntityRecord& entity_record : entities_) {
            if (entity_record.config.id != page_record->owner &&
                table_access(entity_record, request.pfn).has_value()) {
                return reject(request, ErrorCode::IsolationViolation,
                              entity_record.config.id, page_record,
                              "non-owner mapping remains before reclaim");
            }
        }
        if (page_record->confidential && !page_record->scrubbed &&
            !request.scrub) {
            return reject(request, ErrorCode::NotScrubbed, request.caller,
                          page_record,
                          "confidential page must be scrubbed before reclaim");
        }
        erase_mapping(*find_entity(page_record->owner), page_record->address,
                      page_size_);
        const ErrorCode mapping_error =
            insert_mapping(*find_entity(kHostEntity), page_record->address,
                           page_size_, Access::ReadWrite);
        if (mapping_error != ErrorCode::None) {
            restore(before);
            return reject(request, mapping_error, kHostEntity,
                          find_page(request.pfn),
                          "failed to restore Host owner mapping");
        }
        page_record->state = PKVM_PAGE_OWNED;
        page_record->owner = kHostEntity;
        page_record->borrower = kInvalidEntity;
        page_record->share = ShareKind::None;
        if (page_record->confidential && request.scrub) {
            page_record->scrubbed = true;
        }
        break;
    }
    case HvcOperation::NONE:
        break;
    }

    const AuditResult verified = verify_at(step_);
    if (!verified.ok()) {
        restore(before);
        if (verified.witness.has_value()) {
            return reject_violation(request, *verified.witness);
        }
        return reject(request, ErrorCode::InvariantViolation, request.caller,
                      page_record, "post-HVC invariant verification failed");
    }

    HvcResult result{};
    result.request = request;
    result.accepted = true;
    result.error = ErrorCode::None;
    result.step = step_;
    history_.push_back(result);
    return result;
}

inline AuditResult Machine::simulate_stage2_map(EntityId id, PhysRange range,
                                                Access access) {
    ++step_;
    EntityRecord* entity_record = find_entity(id);
    if (entity_record == nullptr) {
        return reject_audit(ErrorCode::UnknownEntity, step_, 0U, id,
                            "mapping entity does not exist");
    }
    const ErrorCode mapping_error =
        insert_mapping(*entity_record, range.base, range.size, access);
    if (mapping_error != ErrorCode::None) {
        return reject_audit(mapping_error, step_, range.base / page_size_, id,
                            "stage-2 mapping injection was rejected");
    }

    AuditResult result = verify_at(step_);
    if (!result.ok() && result.witness.has_value()) {
        remember(*result.witness);
    }
    return result;
}

inline AuditResult Machine::simulate_stage2_map_page(EntityId id, Pfn pfn,
                                                     Access access) {
    std::uint64_t address = 0U;
    if (!pfn_address(pfn, &address)) {
        ++step_;
        return reject_audit(ErrorCode::OutOfRange, step_, pfn, id,
                            "PFN multiplication overflows");
    }
    return simulate_stage2_map(id, PhysRange{address, page_size_}, access);
}

inline AuditResult Machine::simulate_stage2_unmap(EntityId id,
                                                  PhysRange range) {
    ++step_;
    EntityRecord* entity_record = find_entity(id);
    if (entity_record == nullptr) {
        return reject_audit(ErrorCode::UnknownEntity, step_,
                            range.base / page_size_, id,
                            "mapping entity does not exist");
    }
    erase_mapping(*entity_record, range.base, range.size);
    AuditResult result = verify_at(step_);
    if (!result.ok() && result.witness.has_value()) {
        remember(*result.witness);
    }
    return result;
}

inline AuditResult Machine::simulate_stage2_unmap_page(EntityId id, Pfn pfn) {
    std::uint64_t address = 0U;
    if (!pfn_address(pfn, &address)) {
        ++step_;
        return reject_audit(ErrorCode::OutOfRange, step_, pfn, id,
                            "PFN multiplication overflows");
    }
    return simulate_stage2_unmap(id, PhysRange{address, page_size_});
}

inline AuditResult Machine::set_scrubbed(Pfn pfn, bool scrubbed) {
    ++step_;
    PageRecord* page_record = find_page(pfn);
    if (page_record == nullptr) {
        return reject_audit(ErrorCode::UntrackedPage, step_, pfn,
                            kInvalidEntity, "PFN is not tracked");
    }
    page_record->scrubbed = scrubbed || !page_record->confidential;
    AuditResult result{};
    result.accepted = true;
    result.error = ErrorCode::None;
    result.step = step_;
    return result;
}

inline AuditResult Machine::verify() const { return verify_at(step_); }

inline AuditResult Machine::verify_at(std::uint64_t step) const {
    std::optional<Violation> best{};
    auto consider = [&best](Violation candidate) {
        if (!best.has_value() || violation_less(candidate, *best)) {
            best = std::move(candidate);
        }
    };

    for (const EntityRecord& entity_record : entities_) {
        const PhysRange& range = entity_record.config.stage2_range;
        for (const Stage2Mapping& mapping : entity_record.table) {
            bool overflow = false;
            const std::uint64_t mapping_end =
                detail::checked_end(mapping.phys_base, mapping.size, &overflow);
            if (overflow || mapping.size == 0U ||
                mapping.phys_base % page_size_ != 0U ||
                mapping.size % page_size_ != 0U ||
                !detail::valid_access(mapping.access)) {
                Violation violation{};
                violation.step = step;
                violation.pfn = mapping.phys_base / page_size_;
                violation.code = ErrorCode::InvalidMapping;
                violation.entity = entity_record.config.id;
                violation.actual_access = mapping.access;
                violation.detail = "malformed Stage-2 interval";
                consider(std::move(violation));
                continue;
            }
            if (mapping.phys_base < range.base || mapping_end > range.end()) {
                Violation violation{};
                violation.step = step;
                violation.pfn = mapping.phys_base / page_size_;
                violation.code = ErrorCode::OutOfRange;
                violation.entity = entity_record.config.id;
                violation.actual_access = mapping.access;
                violation.detail =
                    "Stage-2 mapping escapes the entity legal range";
                consider(std::move(violation));
            }
        }
    }

    for (const PageRecord& page_record : pages_) {
        const auto access_of = [this, &page_record](
                                   EntityId id) -> std::optional<Access> {
            const EntityRecord* entity_record = find_entity(id);
            if (entity_record == nullptr) {
                return std::nullopt;
            }
            return table_access(*entity_record, page_record.pfn);
        };

        if (page_record.state == PKVM_NOPAGE) {
            for (const EntityRecord& entity_record : entities_) {
                const std::optional<Access> access = access_of(
                    entity_record.config.id);
                if (access.has_value()) {
                    Violation violation{};
                    violation.step = step;
                    violation.pfn = page_record.pfn;
                    violation.code = ErrorCode::IsolationViolation;
                    violation.entity = entity_record.config.id;
                    violation.owner = page_record.owner;
                    violation.borrower = page_record.borrower;
                    violation.state = page_record.state;
                    violation.actual_access = *access;
                    violation.detail =
                        "PKVM_NOPAGE is mapped in a Stage-2 table";
                    consider(std::move(violation));
                }
            }
            continue;
        }

        if (page_record.state == PKVM_PAGE_OWNED) {
            if (find_entity(page_record.owner) == nullptr) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::UnknownEntity;
                violation.entity = page_record.owner;
                violation.owner = page_record.owner;
                violation.state = page_record.state;
                violation.detail = "owned page has an unknown owner";
                consider(std::move(violation));
            }
            for (const EntityRecord& entity_record : entities_) {
                if (entity_record.config.id == page_record.owner) {
                    continue;
                }
                const std::optional<Access> access =
                    access_of(entity_record.config.id);
                if (access.has_value()) {
                    Violation violation{};
                    violation.step = step;
                    violation.pfn = page_record.pfn;
                    violation.code = ErrorCode::IsolationViolation;
                    violation.entity = entity_record.config.id;
                    violation.owner = page_record.owner;
                    violation.state = page_record.state;
                    violation.actual_access = *access;
                    violation.detail =
                        "exclusive page is visible outside its owner";
                    consider(std::move(violation));
                }
            }
            continue;
        }

        if (page_record.state == PKVM_PAGE_SHARED_OWNED) {
            const EntityRecord* owner = find_entity(page_record.owner);
            const EntityRecord* borrower = find_entity(page_record.borrower);
            if (owner == nullptr || borrower == nullptr ||
                page_record.owner == page_record.borrower ||
                page_record.share == ShareKind::None) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::InvariantViolation;
                violation.entity = page_record.owner;
                violation.owner = page_record.owner;
                violation.borrower = page_record.borrower;
                violation.state = page_record.state;
                violation.detail =
                    "shared page lacks a unique valid owner and borrower";
                consider(std::move(violation));
                continue;
            }
            if (page_record.share == ShareKind::HostToHyp &&
                (page_record.owner != kHostEntity ||
                 page_record.borrower != kHypEntity)) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::InvariantViolation;
                violation.entity = page_record.borrower;
                violation.owner = page_record.owner;
                violation.borrower = page_record.borrower;
                violation.state = page_record.state;
                violation.detail = "HostToHyp share has invalid endpoints";
                consider(std::move(violation));
            }
            if (page_record.share == ShareKind::HostToGuest &&
                (page_record.owner != kHostEntity ||
                 borrower->config.kind != EntityKind::Guest)) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::InvariantViolation;
                violation.entity = page_record.borrower;
                violation.owner = page_record.owner;
                violation.borrower = page_record.borrower;
                violation.state = page_record.state;
                violation.detail = "HostToGuest share has invalid endpoints";
                consider(std::move(violation));
            }

            const std::optional<Access> owner_access =
                access_of(page_record.owner);
            if (!owner_access.has_value()) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::MissingMapping;
                violation.entity = page_record.owner;
                violation.owner = page_record.owner;
                violation.borrower = page_record.borrower;
                violation.state = page_record.state;
                violation.detail = "shared page owner mapping is absent";
                consider(std::move(violation));
            }
            const std::optional<Access> borrower_access =
                access_of(page_record.borrower);
            if (!borrower_access.has_value()) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::MissingMapping;
                violation.entity = page_record.borrower;
                violation.owner = page_record.owner;
                violation.borrower = page_record.borrower;
                violation.state = page_record.state;
                violation.detail = "shared page borrower mapping is absent";
                consider(std::move(violation));
            } else if (detail::access_exceeds(*borrower_access,
                                              Access::ReadOnly)) {
                Violation violation{};
                violation.step = step;
                violation.pfn = page_record.pfn;
                violation.code = ErrorCode::PermissionViolation;
                violation.entity = page_record.borrower;
                violation.owner = page_record.owner;
                violation.borrower = page_record.borrower;
                violation.state = page_record.state;
                violation.actual_access = *borrower_access;
                violation.detail =
                    "shared borrower exceeds read-only protocol access";
                consider(std::move(violation));
            }

            for (const EntityRecord& entity_record : entities_) {
                if (entity_record.config.id == page_record.owner ||
                    entity_record.config.id == page_record.borrower) {
                    continue;
                }
                const std::optional<Access> access =
                    access_of(entity_record.config.id);
                if (access.has_value()) {
                    Violation violation{};
                    violation.step = step;
                    violation.pfn = page_record.pfn;
                    violation.code = ErrorCode::IsolationViolation;
                    violation.entity = entity_record.config.id;
                    violation.owner = page_record.owner;
                    violation.borrower = page_record.borrower;
                    violation.state = page_record.state;
                    violation.actual_access = *access;
                    violation.detail =
                        "shared page has a second or foreign borrower";
                    consider(std::move(violation));
                }
            }
            continue;
        }

        Violation violation{};
        violation.step = step;
        violation.pfn = page_record.pfn;
        violation.code = ErrorCode::InvariantViolation;
        violation.entity = page_record.owner;
        violation.owner = page_record.owner;
        violation.borrower = page_record.borrower;
        violation.state = page_record.state;
        violation.detail = "invalid canonical page state";
        consider(std::move(violation));
    }

    AuditResult result{};
    if (!best.has_value()) {
        result.accepted = true;
        result.error = ErrorCode::None;
        result.step = step;
        return result;
    }
    result.accepted = false;
    result.error = best->code;
    result.step = step;
    result.detail = best->detail;
    result.witness = std::move(best);
    return result;
}

inline std::optional<Violation> Machine::first_violation() const {
    if (violations_.empty()) {
        return std::nullopt;
    }
    auto first = violations_.begin();
    for (auto current = violations_.begin(); current != violations_.end();
         ++current) {
        if (violation_less(*current, *first)) {
            first = current;
        }
    }
    return *first;
}

} // namespace pkvm

#endif // PKVM_AUDIT_AUDIT_HPP
