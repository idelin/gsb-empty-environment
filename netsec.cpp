#include "netsec.hpp"

#include <algorithm>
#include <iomanip>
#include <limits>
#include <mutex>
#include <sstream>
#include <stdexcept>
#include <unordered_map>
#include <utility>

namespace {

bool power_of_two(std::uint64_t value) noexcept {
    return value != 0 && (value & (value - 1)) == 0;
}

bool range_add_overflows(std::uint64_t start, std::uint64_t size) noexcept {
    return size > std::numeric_limits<std::uint64_t>::max() - start;
}

void hash_combine(std::size_t& seed, std::size_t value) noexcept {
    seed ^= value + static_cast<std::size_t>(0x9e3779b97f4a7c15ULL) +
            (seed << 6) + (seed >> 2);
}

struct RequesterIdHash {
    std::size_t operator()(const netsec::RequesterId& requester) const noexcept {
        std::size_t seed = 0;
        hash_combine(seed, requester.root_complex.value);
        hash_combine(seed, requester.device.segment);
        hash_combine(seed, requester.device.bus);
        hash_combine(seed, requester.device.device);
        hash_combine(seed, requester.device.function);
        return seed;
    }
};

struct CacheKey {
    netsec::DomainId domain{};
    netsec::RequesterId requester{};
    netsec::Pasid pasid{};
    netsec::AddressSpaceId address_space{};
    std::uint64_t iova{0};

    friend bool operator==(const CacheKey&, const CacheKey&) noexcept = default;
};

struct CacheKeyHash {
    std::size_t operator()(const CacheKey& key) const noexcept {
        std::size_t seed = 0;
        hash_combine(seed, key.domain.value);
        hash_combine(seed, RequesterIdHash{}(key.requester));
        hash_combine(seed, key.pasid.value);
        hash_combine(seed, key.address_space.value);
        hash_combine(seed, key.iova);
        return seed;
    }
};

} // namespace

namespace netsec {

std::string_view to_string(Status status) noexcept {
    switch (status) {
    case Status::ok:
        return "ok";
    case Status::invalid_argument:
        return "invalid_argument";
    case Status::unknown_domain:
        return "unknown_domain";
    case Status::domain_exists:
        return "domain_exists";
    case Status::domain_mismatch:
        return "domain_mismatch";
    case Status::device_already_attached:
        return "device_already_attached";
    case Status::device_not_attached:
        return "device_not_attached";
    case Status::pasid_not_authorized:
        return "pasid_not_authorized";
    case Status::pasid_already_authorized:
        return "pasid_already_authorized";
    case Status::address_space_mismatch:
        return "address_space_mismatch";
    case Status::mapping_not_found:
        return "mapping_not_found";
    case Status::mapping_conflict:
        return "mapping_conflict";
    case Status::permission_denied:
        return "permission_denied";
    case Status::translation_fault:
        return "translation_fault";
    case Status::stale_translation:
        return "stale_translation";
    case Status::migration_in_progress:
        return "migration_in_progress";
    case Status::unsupported_operation:
        return "unsupported_operation";
    case Status::invalid_snapshot:
        return "invalid_snapshot";
    }
    return "unknown";
}

std::string to_string(const RequesterId& requester) {
    std::ostringstream stream;
    stream << "rc" << requester.root_complex.value << ':' << std::hex
           << requester.device.segment << ':' << static_cast<unsigned>(requester.device.bus)
           << ':' << static_cast<unsigned>(requester.device.device) << '.'
           << static_cast<unsigned>(requester.device.function);
    return stream.str();
}

struct AddressSpacePageTable::Impl {
    struct Entry {
        std::uint64_t phys{0};
        std::uint64_t size{0};
        MemoryPermissions permissions{};
    };

    explicit Impl(std::uint64_t page_size) : page_size(page_size) {}

    std::uint64_t page_size;
    mutable std::mutex mutex;
    std::unordered_map<std::uint64_t, Entry> pages;
};

AddressSpacePageTable::AddressSpacePageTable(std::uint64_t page_size)
    : impl_(std::make_unique<Impl>(page_size)) {
    if (!power_of_two(page_size)) {
        throw std::invalid_argument("AddressSpacePageTable page size must be a power of two");
    }
}

AddressSpacePageTable::~AddressSpacePageTable() = default;

AddressSpacePageTable::AddressSpacePageTable(AddressSpacePageTable&&) noexcept = default;

AddressSpacePageTable& AddressSpacePageTable::operator=(AddressSpacePageTable&&) noexcept =
    default;

std::uint64_t AddressSpacePageTable::page_size() const noexcept {
    return impl_->page_size;
}

Status AddressSpacePageTable::map_range(std::uint64_t iova, std::uint64_t phys,
                                        std::uint64_t size,
                                        MemoryPermissions permissions) {
    const std::uint64_t page_size = impl_->page_size;
    if (size == 0 || !power_of_two(page_size)) {
        return Status::invalid_argument;
    }
    if (iova % page_size != 0 || phys % page_size != 0 || size % page_size != 0) {
        return Status::invalid_argument;
    }
    if (range_add_overflows(iova, size) || range_add_overflows(phys, size)) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    for (std::uint64_t offset = 0; offset < size; offset += page_size) {
        if (impl_->pages.contains(iova + offset)) {
            return Status::mapping_conflict;
        }
    }
    for (std::uint64_t offset = 0; offset < size; offset += page_size) {
        impl_->pages.emplace(iova + offset,
                             Impl::Entry{phys + offset, page_size, permissions});
    }
    return Status::ok;
}

Status AddressSpacePageTable::unmap_range(std::uint64_t iova, std::uint64_t size) {
    const std::uint64_t page_size = impl_->page_size;
    if (size == 0 || iova % page_size != 0 || size % page_size != 0 ||
        range_add_overflows(iova, size)) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    for (std::uint64_t offset = 0; offset < size; offset += page_size) {
        impl_->pages.erase(iova + offset);
    }
    return Status::ok;
}

void AddressSpacePageTable::clear() {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    impl_->pages.clear();
}

std::vector<PageMapping> AddressSpacePageTable::mappings() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    std::vector<PageMapping> result;
    result.reserve(impl_->pages.size());
    for (const auto& [iova, entry] : impl_->pages) {
        result.push_back(PageMapping{iova, entry.phys, entry.size, entry.permissions});
    }
    std::sort(result.begin(), result.end(),
              [](const PageMapping& lhs, const PageMapping& rhs) { return lhs.iova < rhs.iova; });
    return result;
}

PageTableLookup AddressSpacePageTable::lookup(std::uint64_t iova, AccessType access) const {
    const std::uint64_t page_size = impl_->page_size;
    const std::uint64_t page = iova & ~(page_size - 1);

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto it = impl_->pages.find(page);
    if (it == impl_->pages.end()) {
        return {};
    }

    const Impl::Entry& entry = it->second;
    const std::uint64_t offset = iova - page;
    if (offset >= entry.size || !entry.permissions.allows(access)) {
        return PageTableLookup{LookupStatus::permission_denied, {}};
    }
    return PageTableLookup{
        LookupStatus::present,
        PageTranslation{entry.phys + offset, entry.size, entry.permissions}};
}

std::vector<PageMapping> AddressSpacePageTable::export_mappings() const {
    return mappings();
}

struct AtsIsolation::Impl {
    struct Binding {
        Pasid pasid{};
        AddressSpaceId address_space{};
        std::shared_ptr<TranslationProvider> provider;
        std::shared_ptr<AddressSpacePageTable> internal_table;
        std::uint64_t epoch{0};
    };

    struct DeviceState {
        DomainId domain{};
        std::unordered_map<std::uint32_t, Binding> bindings;
    };

    struct DomainState {
        DomainId id{};
        std::uint64_t epoch{0};
        bool migrating{false};
    };

    struct CacheEntry {
        DomainId domain_tag{};
        RequesterId requester_tag{};
        Pasid pasid_tag{};
        AddressSpaceId address_space_tag{};
        std::uint64_t domain_epoch{0};
        std::uint64_t binding_epoch{0};
        std::uint64_t translated_address{0};
        std::uint64_t page_size{0};
        MemoryPermissions permissions{};
    };

    struct ResolvedAuth {
        const DomainState* domain{nullptr};
        const Binding* binding{nullptr};
    };

    explicit Impl(AtsConfig config) : config(config) {
        if (!power_of_two(config.default_page_size)) {
            this->config.default_page_size = 4096;
        }
    }

    AtsConfig config;
    mutable std::mutex mutex;
    std::unordered_map<std::uint64_t, DomainState> domains;
    std::unordered_map<RequesterId, DeviceState, RequesterIdHash> devices;
    std::unordered_map<CacheKey, CacheEntry, CacheKeyHash> cache;
    IsolationStats counters;
    std::uint64_t next_domain_epoch{1};
    std::uint64_t next_binding_epoch{1};

    Status resolve_auth(const TranslationRequest& request, ResolvedAuth& resolved) const {
        if (!request.domain.valid() || !request.pasid.valid()) {
            return Status::invalid_argument;
        }

        const auto domain_it = domains.find(request.domain.value);
        if (domain_it == domains.end()) {
            return Status::unknown_domain;
        }
        if (domain_it->second.migrating) {
            return Status::migration_in_progress;
        }

        const auto device_it = devices.find(request.requester);
        if (device_it == devices.end()) {
            return Status::device_not_attached;
        }
        if (device_it->second.domain != request.domain) {
            return Status::domain_mismatch;
        }

        const auto binding_it = device_it->second.bindings.find(request.pasid.value);
        if (binding_it == device_it->second.bindings.end()) {
            return Status::pasid_not_authorized;
        }
        if (binding_it->second.address_space != request.address_space) {
            return Status::address_space_mismatch;
        }
        if (!binding_it->second.provider) {
            return Status::unsupported_operation;
        }

        resolved.domain = &domain_it->second;
        resolved.binding = &binding_it->second;
        return Status::ok;
    }

    void bump_binding(Binding& binding) {
        binding.epoch = next_binding_epoch++;
        if (next_binding_epoch == 0) {
            next_binding_epoch = 1;
        }
    }

    void bump_domain(DomainState& domain) {
        domain.epoch = next_domain_epoch++;
        if (next_domain_epoch == 0) {
            next_domain_epoch = 1;
        }
    }

    template <typename Predicate>
    void purge_cache(Predicate predicate) {
        for (auto it = cache.begin(); it != cache.end();) {
            if (predicate(it->first, it->second)) {
                it = cache.erase(it);
            } else {
                ++it;
            }
        }
    }

    Status find_binding_any(DomainId domain, const RequesterId& requester, Pasid pasid,
                            bool reject_migrating, Binding*& binding) {
        if (!domain.valid() || !pasid.valid()) {
            return Status::invalid_argument;
        }

        const auto domain_it = domains.find(domain.value);
        if (domain_it == domains.end()) {
            return Status::unknown_domain;
        }
        if (reject_migrating && domain_it->second.migrating) {
            return Status::migration_in_progress;
        }

        const auto device_it = devices.find(requester);
        if (device_it == devices.end()) {
            return Status::device_not_attached;
        }
        if (device_it->second.domain != domain) {
            return Status::domain_mismatch;
        }

        const auto binding_it = device_it->second.bindings.find(pasid.value);
        if (binding_it == device_it->second.bindings.end()) {
            return Status::pasid_not_authorized;
        }

        binding = &binding_it->second;
        return Status::ok;
    }

    Status find_binding(DomainId domain, const RequesterId& requester, Pasid pasid,
                        AddressSpaceId address_space, Binding*& binding,
                        bool reject_migrating = true) {
        Binding* found = nullptr;
        const Status status =
            find_binding_any(domain, requester, pasid, reject_migrating, found);
        if (status != Status::ok) {
            return status;
        }
        if (found->address_space != address_space) {
            return Status::address_space_mismatch;
        }

        binding = found;
        return Status::ok;
    }

    Status find_binding_for_removal(DomainId domain, const RequesterId& requester, Pasid pasid,
                                    Binding*& binding) {
        return find_binding_any(domain, requester, pasid, false, binding);
    }

    void install_cache_entry(const TranslationRequest& request, std::uint64_t domain_epoch,
                             std::uint64_t binding_epoch, const PageTranslation& translation) {
        if (config.max_cache_entries != 0 && cache.size() >= config.max_cache_entries) {
            cache.erase(cache.begin());
        }

        cache.emplace(
            CacheKey{request.domain, request.requester, request.pasid, request.address_space,
                     request.iova},
            CacheEntry{request.domain, request.requester, request.pasid, request.address_space,
                       domain_epoch, binding_epoch, translation.translated_address,
                       translation.page_size, translation.permissions});
    }
};

AtsIsolation::AtsIsolation() : impl_(std::make_unique<Impl>(AtsConfig{})) {}

AtsIsolation::AtsIsolation(AtsConfig config) : impl_(std::make_unique<Impl>(config)) {}

AtsIsolation::~AtsIsolation() = default;

AtsIsolation::AtsIsolation(AtsIsolation&&) noexcept = default;

AtsIsolation& AtsIsolation::operator=(AtsIsolation&&) noexcept = default;

Status AtsIsolation::register_domain(DomainId domain) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    if (impl_->domains.contains(domain.value)) {
        return Status::domain_exists;
    }
    Impl::DomainState state;
    state.id = domain;
    impl_->bump_domain(state);
    impl_->domains.emplace(domain.value, state);
    return Status::ok;
}

Status AtsIsolation::retire_domain(DomainId domain) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        return Status::unknown_domain;
    }

    for (auto it = impl_->devices.begin(); it != impl_->devices.end();) {
        if (it->second.domain == domain) {
            it = impl_->devices.erase(it);
        } else {
            ++it;
        }
    }
    impl_->purge_cache([domain](const CacheKey& key, const Impl::CacheEntry& entry) {
        return key.domain == domain || entry.domain_tag == domain;
    });
    impl_->domains.erase(domain_it);
    ++impl_->counters.invalidations;
    return Status::ok;
}

Status AtsIsolation::attach_device(DomainId domain, const RequesterId& requester) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        return Status::unknown_domain;
    }
    if (domain_it->second.migrating) {
        return Status::migration_in_progress;
    }
    if (impl_->devices.contains(requester)) {
        return Status::device_already_attached;
    }

    Impl::DeviceState state;
    state.domain = domain;
    impl_->devices.emplace(requester, std::move(state));
    return Status::ok;
}

Status AtsIsolation::detach_device(DomainId domain, const RequesterId& requester) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto device_it = impl_->devices.find(requester);
    if (device_it == impl_->devices.end()) {
        return Status::device_not_attached;
    }
    if (device_it->second.domain != domain) {
        return Status::domain_mismatch;
    }

    impl_->purge_cache([domain, &requester](const CacheKey& key, const Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester;
    });
    impl_->devices.erase(device_it);
    return Status::ok;
}

bool AtsIsolation::is_attached(DomainId domain, const RequesterId& requester) const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto it = impl_->devices.find(requester);
    return it != impl_->devices.end() && it->second.domain == domain;
}

Status AtsIsolation::authorize_pasid(DomainId domain, const RequesterId& requester, Pasid pasid,
                                     AddressSpaceId address_space) {
    return authorize_pasid(domain, requester, pasid, address_space, nullptr);
}

Status AtsIsolation::authorize_pasid(DomainId domain, const RequesterId& requester, Pasid pasid,
                                     AddressSpaceId address_space,
                                     std::shared_ptr<TranslationProvider> provider) {
    if (!domain.valid() || !pasid.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        return Status::unknown_domain;
    }
    if (domain_it->second.migrating) {
        return Status::migration_in_progress;
    }

    const auto device_it = impl_->devices.find(requester);
    if (device_it == impl_->devices.end()) {
        return Status::device_not_attached;
    }
    if (device_it->second.domain != domain) {
        return Status::domain_mismatch;
    }
    if (device_it->second.bindings.contains(pasid.value)) {
        return Status::pasid_already_authorized;
    }

    Impl::Binding binding;
    binding.pasid = pasid;
    binding.address_space = address_space;
    if (provider) {
        binding.provider = std::move(provider);
        binding.internal_table =
            std::dynamic_pointer_cast<AddressSpacePageTable>(binding.provider);
    } else {
        binding.internal_table = std::make_shared<AddressSpacePageTable>(
            impl_->config.default_page_size);
        binding.provider = binding.internal_table;
    }
    impl_->bump_binding(binding);
    device_it->second.bindings.emplace(pasid.value, std::move(binding));
    return Status::ok;
}

Status AtsIsolation::revoke_pasid(DomainId domain, const RequesterId& requester, Pasid pasid) {
    if (!domain.valid() || !pasid.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::Binding* binding = nullptr;
    const Status status = impl_->find_binding_for_removal(domain, requester, pasid, binding);
    if (status != Status::ok) {
        return status;
    }
    (void)binding;

    const auto device_it = impl_->devices.find(requester);
    impl_->purge_cache([domain, &requester, pasid](const CacheKey& key, const Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester && key.pasid == pasid;
    });
    device_it->second.bindings.erase(pasid.value);
    return Status::ok;
}

bool AtsIsolation::is_pasid_authorized(DomainId domain, const RequesterId& requester, Pasid pasid,
                                       AddressSpaceId address_space) const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto device_it = impl_->devices.find(requester);
    if (device_it == impl_->devices.end() || device_it->second.domain != domain) {
        return false;
    }
    const auto binding_it = device_it->second.bindings.find(pasid.value);
    return binding_it != device_it->second.bindings.end() &&
           binding_it->second.address_space == address_space;
}

Status AtsIsolation::map_range(DomainId domain, const RequesterId& requester, Pasid pasid,
                               AddressSpaceId address_space, std::uint64_t iova,
                               std::uint64_t phys, std::uint64_t size,
                               MemoryPermissions permissions) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::Binding* binding = nullptr;
    const Status status =
        impl_->find_binding(domain, requester, pasid, address_space, binding);
    if (status != Status::ok) {
        return status;
    }
    if (!binding->internal_table) {
        return Status::unsupported_operation;
    }

    const Status map_status = binding->internal_table->map_range(iova, phys, size, permissions);
    if (map_status != Status::ok) {
        return map_status;
    }

    impl_->bump_binding(*binding);
    impl_->purge_cache([domain, &requester, pasid, address_space](const CacheKey& key,
                                                                 Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester && key.pasid == pasid &&
               key.address_space == address_space;
    });
    return Status::ok;
}

Status AtsIsolation::unmap_range(DomainId domain, const RequesterId& requester, Pasid pasid,
                                 AddressSpaceId address_space, std::uint64_t iova,
                                 std::uint64_t size) {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::Binding* binding = nullptr;
    const Status status =
        impl_->find_binding(domain, requester, pasid, address_space, binding);
    if (status != Status::ok) {
        return status;
    }
    if (!binding->internal_table) {
        return Status::unsupported_operation;
    }

    const Status unmap_status = binding->internal_table->unmap_range(iova, size);
    if (unmap_status != Status::ok) {
        return unmap_status;
    }

    impl_->bump_binding(*binding);
    impl_->purge_cache([domain, &requester, pasid, address_space](const CacheKey& key,
                                                                 Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester && key.pasid == pasid &&
               key.address_space == address_space;
    });
    return Status::ok;
}

TranslationResult AtsIsolation::translate(const TranslationRequest& request) {
    std::uint64_t domain_epoch = 0;
    std::uint64_t binding_epoch = 0;
    std::shared_ptr<TranslationProvider> provider;

    {
        std::lock_guard<std::mutex> lock(impl_->mutex);
        ++impl_->counters.translations;

        Impl::ResolvedAuth auth;
        const Status auth_status = impl_->resolve_auth(request, auth);
        if (auth_status != Status::ok) {
            ++impl_->counters.rejected;
            return TranslationResult{auth_status};
        }

        const CacheKey key{request.domain, request.requester, request.pasid,
                           request.address_space, request.iova};
        const auto cache_it = impl_->cache.find(key);
        if (cache_it != impl_->cache.end()) {
            Impl::CacheEntry& entry = cache_it->second;
            const bool tags_match =
                entry.domain_tag == request.domain && entry.requester_tag == request.requester &&
                entry.pasid_tag == request.pasid &&
                entry.address_space_tag == request.address_space;
            const bool still_valid =
                entry.domain_epoch == auth.domain->epoch &&
                entry.binding_epoch == auth.binding->epoch;
            if (tags_match && still_valid) {
                if (!entry.permissions.allows(request.access)) {
                    ++impl_->counters.rejected;
                    return TranslationResult{Status::permission_denied};
                }
                ++impl_->counters.cache_hits;
                return TranslationResult{Status::ok,
                                         entry.translated_address,
                                         entry.page_size,
                                         entry.permissions,
                                         entry.domain_epoch,
                                         true};
            }
            impl_->cache.erase(cache_it);
        }

        ++impl_->counters.cache_misses;
        domain_epoch = auth.domain->epoch;
        binding_epoch = auth.binding->epoch;
        provider = auth.binding->provider;
    }

    PageTableLookup lookup;
    bool provider_failed = false;
    try {
        lookup = provider->lookup(request.iova, request.access);
    } catch (...) {
        provider_failed = true;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::ResolvedAuth auth;
    const Status auth_status = impl_->resolve_auth(request, auth);
    if (auth_status != Status::ok) {
        ++impl_->counters.rejected;
        return TranslationResult{auth_status};
    }
    if (auth.domain->epoch != domain_epoch || auth.binding->epoch != binding_epoch) {
        ++impl_->counters.rejected;
        return TranslationResult{Status::stale_translation};
    }
    if (auth.binding->provider != provider) {
        ++impl_->counters.rejected;
        return TranslationResult{Status::stale_translation};
    }
    if (provider_failed) {
        ++impl_->counters.rejected;
        return TranslationResult{Status::translation_fault};
    }

    switch (lookup.status) {
    case LookupStatus::not_present:
        return TranslationResult{Status::mapping_not_found};
    case LookupStatus::permission_denied:
        ++impl_->counters.rejected;
        return TranslationResult{Status::permission_denied};
    case LookupStatus::fault:
        return TranslationResult{Status::translation_fault};
    case LookupStatus::present:
        break;
    }

    if (!lookup.translation.permissions.allows(request.access)) {
        ++impl_->counters.rejected;
        return TranslationResult{Status::permission_denied};
    }
    if (lookup.translation.page_size == 0) {
        ++impl_->counters.rejected;
        return TranslationResult{Status::translation_fault};
    }
    impl_->install_cache_entry(request, domain_epoch, binding_epoch, lookup.translation);
    return TranslationResult{Status::ok,
                             lookup.translation.translated_address,
                             lookup.translation.page_size,
                             lookup.translation.permissions,
                             domain_epoch,
                             false};
}

PageRequestResult AtsIsolation::handle_page_request(const PageRequest& request) {
    PageRequestResult result;
    result.page_request_group = request.page_request_group;
    result.response_index = request.response_index;

    std::lock_guard<std::mutex> lock(impl_->mutex);
    ++impl_->counters.pri_requests;

    TranslationRequest translation_request;
    translation_request.domain = request.domain;
    translation_request.requester = request.requester;
    translation_request.pasid = request.pasid;
    translation_request.address_space = request.address_space;
    translation_request.iova = request.address;
    translation_request.access = request.access;

    Impl::ResolvedAuth auth;
    const Status auth_status = impl_->resolve_auth(translation_request, auth);
    if (auth_status != Status::ok) {
        ++impl_->counters.pri_rejected;
        result.status = auth_status;
        result.disposition = PriDisposition::invalid;
        return result;
    }

    const CacheKey key{request.domain, request.requester, request.pasid,
                       request.address_space, request.address};
    const auto cache_it = impl_->cache.find(key);
    if (cache_it != impl_->cache.end()) {
        const Impl::CacheEntry& entry = cache_it->second;
        const bool tags_match =
            entry.domain_tag == request.domain && entry.requester_tag == request.requester &&
            entry.pasid_tag == request.pasid &&
            entry.address_space_tag == request.address_space;
        const bool still_valid =
            entry.domain_epoch == auth.domain->epoch &&
            entry.binding_epoch == auth.binding->epoch;
        if (tags_match && still_valid) {
            if (!entry.permissions.allows(request.access)) {
                ++impl_->counters.pri_rejected;
                result.status = Status::permission_denied;
                result.disposition = PriDisposition::invalid;
                return result;
            }
            result.status = Status::ok;
            result.disposition = PriDisposition::already_mapped;
            return result;
        }
        impl_->cache.erase(cache_it);
    }

    result.status = Status::ok;
    result.disposition = PriDisposition::service;
    return result;
}

Status AtsIsolation::invalidate_domain(DomainId domain) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        return Status::unknown_domain;
    }

    impl_->bump_domain(domain_it->second);
    impl_->purge_cache([domain](const CacheKey& key, const Impl::CacheEntry& entry) {
        return key.domain == domain || entry.domain_tag == domain;
    });
    ++impl_->counters.invalidations;
    return Status::ok;
}

Status AtsIsolation::invalidate_device(DomainId domain, const RequesterId& requester) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto device_it = impl_->devices.find(requester);
    if (device_it == impl_->devices.end()) {
        return Status::device_not_attached;
    }
    if (device_it->second.domain != domain) {
        return Status::domain_mismatch;
    }

    for (auto& [pasid, binding] : device_it->second.bindings) {
        (void)pasid;
        impl_->bump_binding(binding);
    }
    impl_->purge_cache([domain, &requester](const CacheKey& key, const Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester;
    });
    ++impl_->counters.invalidations;
    return Status::ok;
}

Status AtsIsolation::invalidate_pasid(DomainId domain, const RequesterId& requester, Pasid pasid) {
    if (!domain.valid() || !pasid.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::Binding* binding = nullptr;
    const Status status = impl_->find_binding_for_removal(domain, requester, pasid, binding);
    if (status != Status::ok) {
        return status;
    }

    impl_->bump_binding(*binding);
    impl_->purge_cache([domain, &requester, pasid](const CacheKey& key, const Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester && key.pasid == pasid;
    });
    ++impl_->counters.invalidations;
    return Status::ok;
}

Status AtsIsolation::invalidate_range(DomainId domain, const RequesterId& requester,
                                      Pasid pasid, AddressSpaceId address_space,
                                      std::uint64_t iova, std::uint64_t size) {
    if (!domain.valid() || !pasid.valid() || size == 0) {
        return Status::invalid_argument;
    }

    const std::uint64_t end =
        range_add_overflows(iova, size) ? std::numeric_limits<std::uint64_t>::max()
                                        : iova + size;

    std::lock_guard<std::mutex> lock(impl_->mutex);
    Impl::Binding* binding = nullptr;
    const Status status =
        impl_->find_binding(domain, requester, pasid, address_space, binding, false);
    if (status != Status::ok) {
        return status;
    }

    impl_->bump_binding(*binding);
    impl_->purge_cache([domain, &requester, pasid, address_space, iova,
                        end](const CacheKey& key, const Impl::CacheEntry&) {
        return key.domain == domain && key.requester == requester && key.pasid == pasid &&
               key.address_space == address_space && key.iova >= iova && key.iova < end;
    });
    ++impl_->counters.invalidations;
    return Status::ok;
}

Status AtsIsolation::begin_migration(DomainId domain) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        return Status::unknown_domain;
    }
    if (domain_it->second.migrating) {
        return Status::migration_in_progress;
    }

    domain_it->second.migrating = true;
    impl_->bump_domain(domain_it->second);
    impl_->purge_cache([domain](const CacheKey& key, const Impl::CacheEntry& entry) {
        return key.domain == domain || entry.domain_tag == domain;
    });
    ++impl_->counters.invalidations;
    return Status::ok;
}

Status AtsIsolation::cancel_migration(DomainId domain) {
    if (!domain.valid()) {
        return Status::invalid_argument;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        return Status::unknown_domain;
    }
    if (!domain_it->second.migrating) {
        return Status::invalid_argument;
    }

    domain_it->second.migrating = false;
    impl_->bump_domain(domain_it->second);
    impl_->purge_cache([domain](const CacheKey& key, const Impl::CacheEntry& entry) {
        return key.domain == domain || entry.domain_tag == domain;
    });
    ++impl_->counters.invalidations;
    return Status::ok;
}

ExportResult AtsIsolation::export_snapshot(DomainId domain) const {
    ExportResult result;
    if (!domain.valid()) {
        result.status = Status::invalid_argument;
        return result;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(domain.value);
    if (domain_it == impl_->domains.end()) {
        result.status = Status::unknown_domain;
        return result;
    }

    result.status = Status::ok;
    result.snapshot.domain = domain;
    result.snapshot.epoch = domain_it->second.epoch;

    try {
        for (const auto& [requester, device] : impl_->devices) {
            if (device.domain != domain) {
                continue;
            }
            DeviceSnapshot device_snapshot;
            device_snapshot.requester = requester;
            for (const auto& [pasid, binding] : device.bindings) {
                (void)pasid;
                PasidBindingSnapshot binding_snapshot;
                binding_snapshot.pasid = binding.pasid;
                binding_snapshot.address_space = binding.address_space;
                if (binding.internal_table) {
                    binding_snapshot.page_size = binding.internal_table->page_size();
                }
                binding_snapshot.external_provider = !binding.provider->exportable();
                if (!binding_snapshot.external_provider) {
                    binding_snapshot.mappings = binding.provider->export_mappings();
                }
                device_snapshot.pasids.push_back(std::move(binding_snapshot));
            }
            std::sort(device_snapshot.pasids.begin(), device_snapshot.pasids.end(),
                      [](const PasidBindingSnapshot& lhs, const PasidBindingSnapshot& rhs) {
                          return lhs.pasid.value < rhs.pasid.value;
                      });
            result.snapshot.devices.push_back(std::move(device_snapshot));
        }
        std::sort(result.snapshot.devices.begin(), result.snapshot.devices.end(),
                  [](const DeviceSnapshot& lhs, const DeviceSnapshot& rhs) {
                      return lhs.requester < rhs.requester;
                  });
        return result;
    } catch (...) {
        return ExportResult{Status::unsupported_operation, {}};
    }
}

Status AtsIsolation::import_snapshot(const MigrationSnapshot& snapshot, ImportResolver resolver,
                                     ImportOptions options) {
    if (!snapshot.domain.valid()) {
        return Status::invalid_argument;
    }

    std::unordered_map<RequesterId, std::size_t, RequesterIdHash> requester_index;
    for (std::size_t i = 0; i < snapshot.devices.size(); ++i) {
        const DeviceSnapshot& device = snapshot.devices[i];
        if (!requester_index.emplace(device.requester, i).second) {
            return Status::invalid_snapshot;
        }
        std::unordered_map<std::uint32_t, bool> pasids;
        for (const PasidBindingSnapshot& binding : device.pasids) {
            if (!binding.pasid.valid() || !pasids.emplace(binding.pasid.value, true).second) {
                return Status::invalid_snapshot;
            }
            if (binding.external_provider && !resolver) {
                return Status::invalid_snapshot;
            }
        }
    }

    {
        std::lock_guard<std::mutex> lock(impl_->mutex);
        if (impl_->domains.contains(snapshot.domain.value) && !options.replace_existing_domain) {
            return Status::domain_exists;
        }
        for (const DeviceSnapshot& device : snapshot.devices) {
            const auto existing = impl_->devices.find(device.requester);
            if (existing == impl_->devices.end()) {
                continue;
            }
            if (!options.replace_existing_domain || existing->second.domain != snapshot.domain) {
                return Status::device_already_attached;
            }
        }
    }

    struct PreparedBinding {
        Pasid pasid{};
        AddressSpaceId address_space{};
        std::shared_ptr<TranslationProvider> provider;
        std::shared_ptr<AddressSpacePageTable> internal_table;
    };

    struct PreparedDevice {
        RequesterId requester{};
        std::vector<PreparedBinding> bindings;
    };

    std::vector<PreparedDevice> prepared;
    prepared.reserve(snapshot.devices.size());
    try {
        for (const DeviceSnapshot& device : snapshot.devices) {
            PreparedDevice prepared_device;
            prepared_device.requester = device.requester;
            for (const PasidBindingSnapshot& binding : device.pasids) {
                PreparedBinding prepared_binding;
                prepared_binding.pasid = binding.pasid;
                prepared_binding.address_space = binding.address_space;
                if (binding.external_provider) {
                    prepared_binding.provider =
                        resolver(snapshot.domain, device.requester, binding.pasid,
                                 binding.address_space);
                    if (!prepared_binding.provider) {
                        return Status::invalid_snapshot;
                    }
                    prepared_binding.internal_table =
                        std::dynamic_pointer_cast<AddressSpacePageTable>(
                            prepared_binding.provider);
                } else {
                    const std::uint64_t page_size =
                        binding.page_size == 0 ? impl_->config.default_page_size
                                               : binding.page_size;
                    prepared_binding.internal_table = std::make_shared<AddressSpacePageTable>(
                        page_size);
                    for (const PageMapping& mapping : binding.mappings) {
                        const Status status = prepared_binding.internal_table->map_range(
                            mapping.iova, mapping.phys, mapping.size, mapping.permissions);
                        if (status != Status::ok) {
                            return Status::invalid_snapshot;
                        }
                    }
                    prepared_binding.provider = prepared_binding.internal_table;
                }
                prepared_device.bindings.push_back(std::move(prepared_binding));
            }
            prepared.push_back(std::move(prepared_device));
        }
    } catch (...) {
        return Status::invalid_snapshot;
    }

    std::lock_guard<std::mutex> lock(impl_->mutex);
    const auto domain_it = impl_->domains.find(snapshot.domain.value);
    if (domain_it != impl_->domains.end() && !options.replace_existing_domain) {
        return Status::domain_exists;
    }

    for (const DeviceSnapshot& device : snapshot.devices) {
        const auto existing = impl_->devices.find(device.requester);
        if (existing == impl_->devices.end()) {
            continue;
        }
        if (!options.replace_existing_domain || existing->second.domain != snapshot.domain) {
            return Status::device_already_attached;
        }
    }

    if (domain_it != impl_->domains.end()) {
        for (auto it = impl_->devices.begin(); it != impl_->devices.end();) {
            if (it->second.domain == snapshot.domain) {
                it = impl_->devices.erase(it);
            } else {
                ++it;
            }
        }
        impl_->purge_cache([&snapshot](const CacheKey& key, const Impl::CacheEntry& entry) {
            return key.domain == snapshot.domain || entry.domain_tag == snapshot.domain;
        });
        impl_->domains.erase(domain_it);
    }

    Impl::DomainState domain_state;
    domain_state.id = snapshot.domain;
    impl_->bump_domain(domain_state);
    impl_->domains.emplace(snapshot.domain.value, domain_state);

    for (PreparedDevice& device : prepared) {
        Impl::DeviceState device_state;
        device_state.domain = snapshot.domain;
        for (PreparedBinding& binding : device.bindings) {
            Impl::Binding stored;
            stored.pasid = binding.pasid;
            stored.address_space = binding.address_space;
            stored.provider = std::move(binding.provider);
            stored.internal_table = std::move(binding.internal_table);
            impl_->bump_binding(stored);
            device_state.bindings.emplace(stored.pasid.value, std::move(stored));
        }
        impl_->devices.emplace(device.requester, std::move(device_state));
    }
    return Status::ok;
}

std::size_t AtsIsolation::cached_entries(DomainId domain) const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    std::size_t count = 0;
    for (const auto& [key, entry] : impl_->cache) {
        if (key.domain == domain || entry.domain_tag == domain) {
            ++count;
        }
    }
    return count;
}

std::size_t AtsIsolation::cache_size() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    return impl_->cache.size();
}

IsolationStats AtsIsolation::stats() const {
    std::lock_guard<std::mutex> lock(impl_->mutex);
    IsolationStats result = impl_->counters;
    result.cache_entries = impl_->cache.size();
    return result;
}

} // namespace netsec
