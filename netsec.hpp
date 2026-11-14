#pragma once

#include <compare>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <string>
#include <string_view>
#include <vector>

namespace netsec {

struct DomainId {
    std::uint64_t value{0};

    static constexpr DomainId from(std::uint64_t value) noexcept { return {value}; }
    [[nodiscard]] constexpr bool valid() const noexcept { return value != 0; }

    friend constexpr bool operator==(DomainId, DomainId) noexcept = default;
    friend constexpr auto operator<=>(DomainId, DomainId) noexcept = default;
};

struct RootComplexId {
    std::uint32_t value{0};

    static constexpr RootComplexId from(std::uint32_t value) noexcept { return {value}; }

    friend constexpr bool operator==(RootComplexId, RootComplexId) noexcept = default;
    friend constexpr auto operator<=>(RootComplexId, RootComplexId) noexcept = default;
};

struct DeviceId {
    std::uint16_t segment{0};
    std::uint8_t bus{0};
    std::uint8_t device{0};
    std::uint8_t function{0};

    friend constexpr bool operator==(DeviceId, DeviceId) noexcept = default;
    friend constexpr auto operator<=>(DeviceId, DeviceId) noexcept = default;
};

struct RequesterId {
    RootComplexId root_complex{};
    DeviceId device{};

    static constexpr RequesterId make(std::uint32_t root_complex, std::uint16_t segment,
                                      std::uint8_t bus, std::uint8_t device,
                                      std::uint8_t function) noexcept {
        return {RootComplexId{root_complex}, DeviceId{segment, bus, device, function}};
    }

    friend constexpr bool operator==(RequesterId, RequesterId) noexcept = default;
    friend constexpr auto operator<=>(RequesterId, RequesterId) noexcept = default;
};

struct Pasid {
    std::uint32_t value{0};

    static constexpr std::uint32_t max_value = 0x000FFFFFu;

    static constexpr Pasid from(std::uint32_t value) noexcept { return {value}; }
    [[nodiscard]] constexpr bool valid() const noexcept { return value <= max_value; }

    friend constexpr bool operator==(Pasid, Pasid) noexcept = default;
    friend constexpr auto operator<=>(Pasid, Pasid) noexcept = default;
};

struct AddressSpaceId {
    std::uint64_t value{0};

    static constexpr AddressSpaceId from(std::uint64_t value) noexcept { return {value}; }

    friend constexpr bool operator==(AddressSpaceId, AddressSpaceId) noexcept = default;
    friend constexpr auto operator<=>(AddressSpaceId, AddressSpaceId) noexcept = default;
};

enum class AccessType : std::uint8_t {
    read = 0,
    write = 1,
    execute = 2,
    atomic = 3,
};

struct MemoryPermissions {
    bool read{true};
    bool write{false};
    bool execute{false};
    bool atomic{false};

    friend constexpr bool operator==(const MemoryPermissions&,
                                     const MemoryPermissions&) noexcept = default;

    [[nodiscard]] constexpr bool allows(AccessType access) const noexcept {
        switch (access) {
        case AccessType::read:
            return read;
        case AccessType::write:
            return write;
        case AccessType::execute:
            return execute;
        case AccessType::atomic:
            return atomic;
        }
        return false;
    }
};

enum class Status : std::uint8_t {
    ok = 0,
    invalid_argument,
    unknown_domain,
    domain_exists,
    domain_mismatch,
    device_already_attached,
    device_not_attached,
    pasid_not_authorized,
    pasid_already_authorized,
    address_space_mismatch,
    mapping_not_found,
    mapping_conflict,
    permission_denied,
    translation_fault,
    stale_translation,
    migration_in_progress,
    unsupported_operation,
    invalid_snapshot,
};

[[nodiscard]] std::string_view to_string(Status status) noexcept;
[[nodiscard]] std::string to_string(const RequesterId& requester);

struct PageMapping {
    std::uint64_t iova{0};
    std::uint64_t phys{0};
    std::uint64_t size{0};
    MemoryPermissions permissions{};

    friend constexpr bool operator==(const PageMapping&, const PageMapping&) noexcept = default;
};

enum class LookupStatus : std::uint8_t {
    present = 0,
    not_present,
    permission_denied,
    fault,
};

struct PageTranslation {
    std::uint64_t translated_address{0};
    std::uint64_t page_size{4096};
    MemoryPermissions permissions{};
};

struct PageTableLookup {
    LookupStatus status{LookupStatus::not_present};
    PageTranslation translation{};
};

class TranslationProvider {
public:
    virtual ~TranslationProvider() = default;

    virtual PageTableLookup lookup(std::uint64_t iova, AccessType access) const = 0;
    virtual bool exportable() const noexcept { return false; }
    virtual std::vector<PageMapping> export_mappings() const { return {}; }
};

class AddressSpacePageTable final : public TranslationProvider {
public:
    explicit AddressSpacePageTable(std::uint64_t page_size = 4096);
    ~AddressSpacePageTable() override;

    AddressSpacePageTable(AddressSpacePageTable&&) noexcept;
    AddressSpacePageTable& operator=(AddressSpacePageTable&&) noexcept;
    AddressSpacePageTable(const AddressSpacePageTable&) = delete;
    AddressSpacePageTable& operator=(const AddressSpacePageTable&) = delete;

    [[nodiscard]] std::uint64_t page_size() const noexcept;
    Status map_range(std::uint64_t iova, std::uint64_t phys, std::uint64_t size,
                     MemoryPermissions permissions = {});
    Status unmap_range(std::uint64_t iova, std::uint64_t size);
    void clear();
    [[nodiscard]] std::vector<PageMapping> mappings() const;

    PageTableLookup lookup(std::uint64_t iova, AccessType access) const override;
    bool exportable() const noexcept override { return true; }
    std::vector<PageMapping> export_mappings() const override;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

struct AtsConfig {
    std::uint64_t default_page_size{4096};
    std::size_t max_cache_entries{0};
};

struct TranslationRequest {
    DomainId domain{};
    RequesterId requester{};
    Pasid pasid{};
    AddressSpaceId address_space{};
    std::uint64_t iova{0};
    AccessType access{AccessType::read};
};

struct TranslationResult {
    Status status{Status::mapping_not_found};
    std::uint64_t translated_address{0};
    std::uint64_t page_size{0};
    MemoryPermissions permissions{};
    std::uint64_t domain_epoch{0};
    bool cache_hit{false};

    [[nodiscard]] explicit operator bool() const noexcept { return status == Status::ok; }
};

struct PageRequest {
    DomainId domain{};
    RequesterId requester{};
    Pasid pasid{};
    AddressSpaceId address_space{};
    std::uint64_t address{0};
    AccessType access{AccessType::read};
    std::uint16_t page_request_group{0};
    std::uint8_t response_index{0};
};

enum class PriDisposition : std::uint8_t {
    service = 0,
    already_mapped,
    invalid,
};

struct PageRequestResult {
    Status status{Status::mapping_not_found};
    PriDisposition disposition{PriDisposition::invalid};
    std::uint16_t page_request_group{0};
    std::uint8_t response_index{0};

    [[nodiscard]] explicit operator bool() const noexcept { return status == Status::ok; }
};

struct PasidBindingSnapshot {
    Pasid pasid{};
    AddressSpaceId address_space{};
    std::uint64_t page_size{0};
    bool external_provider{false};
    std::vector<PageMapping> mappings;
};

struct DeviceSnapshot {
    RequesterId requester{};
    std::vector<PasidBindingSnapshot> pasids;
};

struct MigrationSnapshot {
    DomainId domain{};
    std::uint64_t epoch{0};
    std::vector<DeviceSnapshot> devices;
};

struct ExportResult {
    Status status{Status::unknown_domain};
    MigrationSnapshot snapshot{};

    [[nodiscard]] explicit operator bool() const noexcept { return status == Status::ok; }
};

using ImportResolver = std::function<std::shared_ptr<TranslationProvider>(
    DomainId, const RequesterId&, Pasid, AddressSpaceId)>;

struct ImportOptions {
    bool replace_existing_domain{false};
};

struct IsolationStats {
    std::uint64_t translations{0};
    std::uint64_t cache_hits{0};
    std::uint64_t cache_misses{0};
    std::uint64_t rejected{0};
    std::uint64_t invalidations{0};
    std::uint64_t pri_requests{0};
    std::uint64_t pri_rejected{0};
    std::uint64_t cache_entries{0};
};

class AtsIsolation {
public:
    AtsIsolation();
    explicit AtsIsolation(AtsConfig config);
    ~AtsIsolation();

    AtsIsolation(AtsIsolation&&) noexcept;
    AtsIsolation& operator=(AtsIsolation&&) noexcept;
    AtsIsolation(const AtsIsolation&) = delete;
    AtsIsolation& operator=(const AtsIsolation&) = delete;

    Status register_domain(DomainId domain);
    Status retire_domain(DomainId domain);

    Status attach_device(DomainId domain, const RequesterId& requester);
    Status detach_device(DomainId domain, const RequesterId& requester);
    [[nodiscard]] bool is_attached(DomainId domain, const RequesterId& requester) const;

    Status authorize_pasid(DomainId domain, const RequesterId& requester, Pasid pasid,
                           AddressSpaceId address_space);
    Status authorize_pasid(DomainId domain, const RequesterId& requester, Pasid pasid,
                           AddressSpaceId address_space,
                           std::shared_ptr<TranslationProvider> provider);
    Status revoke_pasid(DomainId domain, const RequesterId& requester, Pasid pasid);
    [[nodiscard]] bool is_pasid_authorized(DomainId domain, const RequesterId& requester,
                                           Pasid pasid, AddressSpaceId address_space) const;

    Status map_range(DomainId domain, const RequesterId& requester, Pasid pasid,
                     AddressSpaceId address_space, std::uint64_t iova, std::uint64_t phys,
                     std::uint64_t size, MemoryPermissions permissions = {});
    Status unmap_range(DomainId domain, const RequesterId& requester, Pasid pasid,
                       AddressSpaceId address_space, std::uint64_t iova, std::uint64_t size);

    TranslationResult translate(const TranslationRequest& request);
    PageRequestResult handle_page_request(const PageRequest& request);

    Status invalidate_domain(DomainId domain);
    Status invalidate_device(DomainId domain, const RequesterId& requester);
    Status invalidate_pasid(DomainId domain, const RequesterId& requester, Pasid pasid);
    Status invalidate_range(DomainId domain, const RequesterId& requester, Pasid pasid,
                            AddressSpaceId address_space, std::uint64_t iova,
                            std::uint64_t size);

    Status begin_migration(DomainId domain);
    Status cancel_migration(DomainId domain);
    [[nodiscard]] ExportResult export_snapshot(DomainId domain) const;
    Status import_snapshot(const MigrationSnapshot& snapshot,
                           ImportResolver resolver = {},
                           ImportOptions options = {});

    [[nodiscard]] std::size_t cached_entries(DomainId domain) const;
    [[nodiscard]] std::size_t cache_size() const;
    [[nodiscard]] IsolationStats stats() const;

private:
    struct Impl;
    std::unique_ptr<Impl> impl_;
};

using AtsIsolator = AtsIsolation;
using AtsDomainManager = AtsIsolation;

} // namespace netsec
