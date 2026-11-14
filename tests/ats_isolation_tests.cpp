#include "netsec.hpp"

#include <atomic>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <iostream>
#include <mutex>
#include <thread>

namespace {

int failures = 0;

#define CHECK(condition)                                                                         \
    do {                                                                                         \
        if (!(condition)) {                                                                      \
            std::cerr << "FAIL " << __FILE__ << ':' << __LINE__ << ": " << #condition << '\n';  \
            ++failures;                                                                          \
        }                                                                                        \
    } while (false)

void check_status(netsec::Status actual, netsec::Status expected, int line) {
    if (actual != expected) {
        std::cerr << "FAIL " << __FILE__ << ':' << line << ": expected "
                  << netsec::to_string(expected) << ", got " << netsec::to_string(actual) << '\n';
        ++failures;
    }
}

#define CHECK_STATUS(actual, expected) check_status((actual), (expected), __LINE__)

using netsec::AccessType;
using netsec::AddressSpaceId;
using netsec::AtsIsolation;
using netsec::DomainId;
using netsec::LookupStatus;
using netsec::MemoryPermissions;
using netsec::PageTableLookup;
using netsec::PageTranslation;
using netsec::Pasid;
using netsec::RequesterId;
using netsec::Status;
using netsec::TranslationProvider;
using netsec::TranslationRequest;

constexpr std::uint64_t kPage = 4096;

RequesterId device(std::uint32_t rc, std::uint16_t segment, std::uint8_t bus,
                   std::uint8_t dev, std::uint8_t fn) {
    return RequesterId::make(rc, segment, bus, dev, fn);
}

TranslationRequest request(DomainId domain, const RequesterId& requester, Pasid pasid,
                           AddressSpaceId address_space, std::uint64_t iova,
                           AccessType access = AccessType::read) {
    TranslationRequest result;
    result.domain = domain;
    result.requester = requester;
    result.pasid = pasid;
    result.address_space = address_space;
    result.iova = iova;
    result.access = access;
    return result;
}

void test_legitimate_passthrough() {
    AtsIsolation isolation;
    const DomainId vm1 = DomainId::from(1);
    const RequesterId gpu = device(1, 0, 0x20, 0, 0);
    const Pasid pasid = Pasid::from(5);
    const AddressSpaceId as = AddressSpaceId::from(0x100);

    CHECK_STATUS(isolation.register_domain(vm1), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm1, gpu), Status::ok);
    CHECK(isolation.is_attached(vm1, gpu));
    CHECK_STATUS(isolation.attach_device(vm1, gpu), Status::device_already_attached);
    CHECK_STATUS(isolation.authorize_pasid(vm1, gpu, pasid, as), Status::ok);
    CHECK(isolation.is_pasid_authorized(vm1, gpu, pasid, as));

    const std::uint64_t iova = 0x0000'1000'0000ULL;
    const std::uint64_t phys = 0x0000'8000'0000ULL;
    const MemoryPermissions rw{true, true, false, false};
    CHECK_STATUS(isolation.map_range(vm1, gpu, pasid, as, iova, phys, 2 * kPage, rw),
                 Status::ok);

    const auto first = isolation.translate(request(vm1, gpu, pasid, as, iova + 0x123));
    CHECK_STATUS(first.status, Status::ok);
    CHECK(first.translated_address == phys + 0x123);
    CHECK(!first.cache_hit);

    const auto second = isolation.translate(request(vm1, gpu, pasid, as, iova + 0x123));
    CHECK_STATUS(second.status, Status::ok);
    CHECK(second.translated_address == phys + 0x123);
    CHECK(second.cache_hit);

    const auto write =
        isolation.translate(request(vm1, gpu, pasid, as, iova + 0x200, AccessType::write));
    CHECK_STATUS(write.status, Status::ok);
    CHECK(write.translated_address == phys + 0x200);

    const std::uint64_t ro_iova = iova + 2 * kPage;
    CHECK_STATUS(isolation.map_range(vm1, gpu, pasid, as, ro_iova, phys + 2 * kPage, kPage,
                                     MemoryPermissions{true, false, false, false}),
                 Status::ok);
    const auto denied =
        isolation.translate(request(vm1, gpu, pasid, as, ro_iova, AccessType::write));
    CHECK_STATUS(denied.status, Status::permission_denied);
    CHECK(denied.translated_address == 0);

    CHECK_STATUS(isolation.translate(request(vm1, gpu, pasid, as, iova + 0x123)).status,
                 Status::ok);

    netsec::PageRequest pri;
    pri.domain = vm1;
    pri.requester = gpu;
    pri.pasid = pasid;
    pri.address_space = as;
    pri.address = iova + 0x123;
    pri.page_request_group = 3;
    pri.response_index = 2;
    const auto mapped_pri = isolation.handle_page_request(pri);
    CHECK_STATUS(mapped_pri.status, Status::ok);
    CHECK(mapped_pri.disposition == netsec::PriDisposition::already_mapped);
    CHECK(mapped_pri.page_request_group == 3);
    CHECK(mapped_pri.response_index == 2);

    pri.address = iova + 0x9000;
    const auto fault_pri = isolation.handle_page_request(pri);
    CHECK_STATUS(fault_pri.status, Status::ok);
    CHECK(fault_pri.disposition == netsec::PriDisposition::service);

    CHECK_STATUS(isolation.unmap_range(vm1, gpu, pasid, as, iova, 2 * kPage), Status::ok);
    const auto gone = isolation.translate(request(vm1, gpu, pasid, as, iova + 0x123));
    CHECK_STATUS(gone.status, Status::mapping_not_found);

    CHECK_STATUS(isolation.detach_device(vm1, gpu), Status::ok);
    const auto detached = isolation.translate(request(vm1, gpu, pasid, as, ro_iova));
    CHECK_STATUS(detached.status, Status::device_not_attached);
}

void test_cross_vm_hit_rejected() {
    AtsIsolation isolation;
    const DomainId vm1 = DomainId::from(11);
    const DomainId vm2 = DomainId::from(22);
    const RequesterId vm1_gpu = device(1, 0, 0x30, 0, 0);
    const RequesterId vm2_gpu = device(2, 0, 0x30, 0, 0);
    const Pasid pasid = Pasid::from(7);
    const AddressSpaceId as = AddressSpaceId::from(0x700);
    const std::uint64_t iova = 0x0000'2000'0000ULL;

    CHECK_STATUS(isolation.register_domain(vm1), Status::ok);
    CHECK_STATUS(isolation.register_domain(vm2), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm1, vm1_gpu), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm2, vm2_gpu), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm1, vm1_gpu, pasid, as), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm2, vm2_gpu, pasid, as), Status::ok);
    CHECK_STATUS(isolation.map_range(vm1, vm1_gpu, pasid, as, iova, 0xA000, kPage), Status::ok);
    CHECK_STATUS(isolation.map_range(vm2, vm2_gpu, pasid, as, iova, 0xB000, kPage), Status::ok);

    const auto vm1_result = isolation.translate(request(vm1, vm1_gpu, pasid, as, iova));
    const auto vm2_result = isolation.translate(request(vm2, vm2_gpu, pasid, as, iova));
    CHECK_STATUS(vm1_result.status, Status::ok);
    CHECK_STATUS(vm2_result.status, Status::ok);
    CHECK(vm1_result.translated_address == 0xA000);
    CHECK(vm2_result.translated_address == 0xB000);

    const auto forged = isolation.translate(request(vm2, vm1_gpu, pasid, as, iova));
    CHECK_STATUS(forged.status, Status::domain_mismatch);
    CHECK(forged.translated_address == 0);
    CHECK(!forged.cache_hit);

    const auto reverse_forged = isolation.translate(request(vm1, vm2_gpu, pasid, as, iova));
    CHECK_STATUS(reverse_forged.status, Status::domain_mismatch);
    CHECK(reverse_forged.translated_address == 0);

    CHECK(isolation.cached_entries(vm1) == 1);
    CHECK(isolation.cached_entries(vm2) == 1);

    CHECK_STATUS(isolation.invalidate_domain(vm1), Status::ok);
    CHECK(isolation.cached_entries(vm1) == 0);
    CHECK(isolation.cached_entries(vm2) == 1);

    const auto vm2_after = isolation.translate(request(vm2, vm2_gpu, pasid, as, iova));
    CHECK_STATUS(vm2_after.status, Status::ok);
    CHECK(vm2_after.cache_hit);
    CHECK(vm2_after.translated_address == 0xB000);

    const auto vm1_after = isolation.translate(request(vm1, vm1_gpu, pasid, as, iova));
    CHECK_STATUS(vm1_after.status, Status::ok);
    CHECK(!vm1_after.cache_hit);
    CHECK(vm1_after.translated_address == 0xA000);

    const auto same_bdf =
        isolation.translate(request(vm1, vm2_gpu, pasid, as, iova));
    CHECK_STATUS(same_bdf.status, Status::domain_mismatch);
}

void test_pasid_forgery_rejected() {
    AtsIsolation isolation;
    const DomainId vm1 = DomainId::from(31);
    const DomainId vm2 = DomainId::from(32);
    const RequesterId vm1_gpu = device(3, 0, 0x40, 0, 0);
    const RequesterId vm2_gpu = device(4, 0, 0x40, 0, 0);
    const Pasid shared_pasid = Pasid::from(9);
    const AddressSpaceId vm1_as = AddressSpaceId::from(0x101);
    const AddressSpaceId vm2_as = AddressSpaceId::from(0x202);
    const std::uint64_t iova = 0x0000'3000'0000ULL;

    CHECK_STATUS(isolation.register_domain(vm1), Status::ok);
    CHECK_STATUS(isolation.register_domain(vm2), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm1, vm1_gpu), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm2, vm2_gpu), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm1, vm1_gpu, shared_pasid, vm1_as), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm2, vm2_gpu, shared_pasid, vm2_as), Status::ok);
    CHECK_STATUS(isolation.map_range(vm1, vm1_gpu, shared_pasid, vm1_as, iova, 0xC000, kPage),
                 Status::ok);
    CHECK_STATUS(isolation.map_range(vm2, vm2_gpu, shared_pasid, vm2_as, iova, 0xD000, kPage),
                 Status::ok);

    const auto forged_domain =
        isolation.translate(request(vm2, vm1_gpu, shared_pasid, vm2_as, iova));
    CHECK_STATUS(forged_domain.status, Status::domain_mismatch);

    const auto forged_pasid =
        isolation.translate(request(vm1, vm1_gpu, Pasid::from(10), vm1_as, iova));
    CHECK_STATUS(forged_pasid.status, Status::pasid_not_authorized);
    CHECK(forged_pasid.translated_address == 0);

    const auto forged_as =
        isolation.translate(request(vm1, vm1_gpu, shared_pasid, vm2_as, iova));
    CHECK_STATUS(forged_as.status, Status::address_space_mismatch);
    CHECK(forged_as.translated_address == 0);

    const auto invalid_pasid =
        isolation.translate(request(vm1, vm1_gpu, Pasid::from(Pasid::max_value + 1), vm1_as,
                                    iova));
    CHECK_STATUS(invalid_pasid.status, Status::invalid_argument);

    netsec::PageRequest forged_pri;
    forged_pri.domain = vm1;
    forged_pri.requester = vm1_gpu;
    forged_pri.pasid = Pasid::from(10);
    forged_pri.address_space = vm1_as;
    forged_pri.address = iova;
    const auto pri = isolation.handle_page_request(forged_pri);
    CHECK_STATUS(pri.status, Status::pasid_not_authorized);
    CHECK(pri.disposition == netsec::PriDisposition::invalid);

    CHECK(isolation.cached_entries(vm1) == 0);
    CHECK(isolation.cached_entries(vm2) == 0);
    const auto legitimate =
        isolation.translate(request(vm1, vm1_gpu, shared_pasid, vm1_as, iova));
    CHECK_STATUS(legitimate.status, Status::ok);
    CHECK(legitimate.translated_address == 0xC000);
    CHECK(isolation.cached_entries(vm1) == 1);
}

class BlockingProvider final : public TranslationProvider {
public:
    PageTableLookup lookup(std::uint64_t iova, AccessType) const override {
        ++calls;
        std::unique_lock<std::mutex> lock(mutex);
        blocked = true;
        condition.notify_all();
        condition.wait(lock, [this] { return released; });
        PageTranslation translation;
        translation.translated_address = 0xE000 + (iova & (kPage - 1));
        translation.page_size = kPage;
        translation.permissions = MemoryPermissions{true, true, false, false};
        return PageTableLookup{LookupStatus::present, translation};
    }

    void wait_until_blocked() const {
        std::unique_lock<std::mutex> lock(mutex);
        condition.wait_for(lock, std::chrono::seconds(2), [this] { return blocked; });
    }

    void release() {
        std::lock_guard<std::mutex> lock(mutex);
        released = true;
        condition.notify_all();
    }

    mutable std::atomic<int> calls{0};

private:
    mutable std::mutex mutex;
    mutable std::condition_variable condition;
    mutable bool blocked{false};
    bool released{false};
};

void test_invalidation_race() {
    AtsIsolation isolation;
    const DomainId vm1 = DomainId::from(41);
    const RequesterId gpu = device(5, 0, 0x50, 0, 0);
    const Pasid pasid = Pasid::from(11);
    const AddressSpaceId as = AddressSpaceId::from(0x111);
    const std::uint64_t iova = 0x0000'4000'0000ULL;

    auto provider = std::make_shared<BlockingProvider>();
    CHECK_STATUS(isolation.register_domain(vm1), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm1, gpu), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm1, gpu, pasid, as, provider), Status::ok);

    netsec::TranslationResult result;
    std::thread translator([&] {
        result = isolation.translate(request(vm1, gpu, pasid, as, iova));
    });

    provider->wait_until_blocked();
    CHECK_STATUS(isolation.invalidate_domain(vm1), Status::ok);
    provider->release();
    translator.join();

    CHECK(provider->calls.load() == 1);
    CHECK_STATUS(result.status, Status::stale_translation);
    CHECK(result.translated_address == 0);
    CHECK(isolation.cached_entries(vm1) == 0);

    const auto after = isolation.translate(request(vm1, gpu, pasid, as, iova));
    CHECK_STATUS(after.status, Status::ok);
    CHECK(after.translated_address == 0xE000);
    CHECK(isolation.cached_entries(vm1) == 1);
}

void test_scoped_invalidation_and_race_scope() {
    AtsIsolation isolation;
    const DomainId vm1 = DomainId::from(51);
    const RequesterId nic = device(6, 0, 0x60, 0, 0);
    const RequesterId nvme = device(6, 0, 0x61, 0, 0);
    const Pasid pasid_a = Pasid::from(1);
    const Pasid pasid_b = Pasid::from(2);
    const AddressSpaceId as_a = AddressSpaceId::from(0xA1);
    const AddressSpaceId as_b = AddressSpaceId::from(0xB2);
    const std::uint64_t iova = 0x0000'5000'0000ULL;

    CHECK_STATUS(isolation.register_domain(vm1), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm1, nic), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm1, nvme), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm1, nic, pasid_a, as_a), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm1, nvme, pasid_b, as_b), Status::ok);
    CHECK_STATUS(isolation.map_range(vm1, nic, pasid_a, as_a, iova, 0x10000, kPage), Status::ok);
    CHECK_STATUS(isolation.map_range(vm1, nvme, pasid_b, as_b, iova, 0x20000, kPage), Status::ok);

    CHECK_STATUS(isolation.translate(request(vm1, nic, pasid_a, as_a, iova)).status, Status::ok);
    CHECK_STATUS(isolation.translate(request(vm1, nvme, pasid_b, as_b, iova)).status, Status::ok);
    CHECK(isolation.cached_entries(vm1) == 2);

    CHECK_STATUS(isolation.invalidate_pasid(vm1, nic, pasid_a), Status::ok);
    CHECK(isolation.cached_entries(vm1) == 1);

    const auto nvme_hit = isolation.translate(request(vm1, nvme, pasid_b, as_b, iova));
    CHECK_STATUS(nvme_hit.status, Status::ok);
    CHECK(nvme_hit.cache_hit);

    const auto nic_miss = isolation.translate(request(vm1, nic, pasid_a, as_a, iova));
    CHECK_STATUS(nic_miss.status, Status::ok);
    CHECK(!nic_miss.cache_hit);

    CHECK_STATUS(isolation.invalidate_range(vm1, nic, pasid_a, as_a, iova, kPage), Status::ok);
    CHECK_STATUS(isolation.translate(request(vm1, nic, pasid_a, as_a, iova)).status, Status::ok);
    CHECK_STATUS(isolation.invalidate_device(vm1, nvme), Status::ok);
    CHECK(isolation.cached_entries(vm1) == 1);
}

void test_live_migration_rebuilds_domain() {
    AtsIsolation source;
    const DomainId migrating = DomainId::from(61);
    const DomainId local = DomainId::from(62);
    const RequesterId migrating_gpu = device(7, 0, 0x70, 0, 0);
    const RequesterId local_gpu = device(8, 0, 0x71, 0, 0);
    const Pasid pasid = Pasid::from(21);
    const AddressSpaceId as = AddressSpaceId::from(0x210);
    const std::uint64_t iova = 0x0000'6000'0000ULL;

    CHECK_STATUS(source.register_domain(migrating), Status::ok);
    CHECK_STATUS(source.register_domain(local), Status::ok);
    CHECK_STATUS(source.attach_device(migrating, migrating_gpu), Status::ok);
    CHECK_STATUS(source.attach_device(local, local_gpu), Status::ok);
    CHECK_STATUS(source.authorize_pasid(migrating, migrating_gpu, pasid, as), Status::ok);
    CHECK_STATUS(source.authorize_pasid(local, local_gpu, pasid, as), Status::ok);
    CHECK_STATUS(source.map_range(migrating, migrating_gpu, pasid, as, iova, 0x30000, 2 * kPage),
                 Status::ok);
    CHECK_STATUS(source.map_range(local, local_gpu, pasid, as, iova, 0x40000, kPage), Status::ok);

    CHECK_STATUS(source.translate(request(migrating, migrating_gpu, pasid, as, iova)).status,
                 Status::ok);
    CHECK_STATUS(source.translate(request(local, local_gpu, pasid, as, iova)).status, Status::ok);
    CHECK(source.cached_entries(migrating) == 1);

    CHECK_STATUS(source.begin_migration(migrating), Status::ok);
    const auto frozen =
        source.translate(request(migrating, migrating_gpu, pasid, as, iova));
    CHECK_STATUS(frozen.status, Status::migration_in_progress);
    CHECK_STATUS(source.translate(request(local, local_gpu, pasid, as, iova)).status, Status::ok);

    const auto exported = source.export_snapshot(migrating);
    CHECK_STATUS(exported.status, Status::ok);
    CHECK(exported.snapshot.domain == migrating);
    CHECK(exported.snapshot.devices.size() == 1);
    CHECK(exported.snapshot.devices[0].pasids.size() == 1);
    CHECK(exported.snapshot.devices[0].pasids[0].mappings.size() == 2);

    AtsIsolation destination;
    const RequesterId destination_gpu = device(9, 0, 0x72, 0, 0);
    CHECK_STATUS(destination.register_domain(local), Status::ok);
    CHECK_STATUS(destination.attach_device(local, destination_gpu), Status::ok);
    CHECK_STATUS(destination.authorize_pasid(local, destination_gpu, pasid, as), Status::ok);
    CHECK_STATUS(destination.map_range(local, destination_gpu, pasid, as, iova, 0x50000, kPage),
                 Status::ok);

    CHECK_STATUS(destination.import_snapshot(exported.snapshot), Status::ok);
    CHECK(destination.is_attached(migrating, migrating_gpu));
    CHECK(destination.is_pasid_authorized(migrating, migrating_gpu, pasid, as));
    CHECK(destination.cached_entries(migrating) == 0);

    const auto rebuilt =
        destination.translate(request(migrating, migrating_gpu, pasid, as, iova + 0x10));
    CHECK_STATUS(rebuilt.status, Status::ok);
    CHECK(rebuilt.translated_address == 0x30010);
    CHECK(!rebuilt.cache_hit);
    CHECK(destination.cached_entries(migrating) == 1);

    const auto rebuilt_hit =
        destination.translate(request(migrating, migrating_gpu, pasid, as, iova + 0x10));
    CHECK(rebuilt_hit.cache_hit);
    CHECK(rebuilt_hit.translated_address == 0x30010);

    const auto local_result =
        destination.translate(request(local, destination_gpu, pasid, as, iova));
    CHECK_STATUS(local_result.status, Status::ok);
    CHECK(local_result.translated_address == 0x50000);
    CHECK(local_result.translated_address != rebuilt.translated_address);

    const auto cross =
        destination.translate(request(migrating, destination_gpu, pasid, as, iova));
    CHECK_STATUS(cross.status, Status::domain_mismatch);
    CHECK(cross.translated_address == 0);

    const auto reverse_cross =
        destination.translate(request(local, migrating_gpu, pasid, as, iova));
    CHECK_STATUS(reverse_cross.status, Status::domain_mismatch);

    CHECK_STATUS(destination.import_snapshot(exported.snapshot), Status::domain_exists);
    CHECK_STATUS(source.retire_domain(migrating), Status::ok);
    CHECK_STATUS(source.translate(request(migrating, migrating_gpu, pasid, as, iova)).status,
                 Status::unknown_domain);
    CHECK_STATUS(destination.translate(request(migrating, migrating_gpu, pasid, as, iova + 0x10))
                     .status,
                 Status::ok);
}

void test_migration_rejects_unrebuildable_provider() {
    AtsIsolation source;
    const DomainId vm = DomainId::from(71);
    const RequesterId gpu = device(10, 0, 0x80, 0, 0);
    const Pasid pasid = Pasid::from(31);
    const AddressSpaceId as = AddressSpaceId::from(0x310);

    auto provider = std::make_shared<BlockingProvider>();
    provider->release();
    CHECK_STATUS(source.register_domain(vm), Status::ok);
    CHECK_STATUS(source.attach_device(vm, gpu), Status::ok);
    CHECK_STATUS(source.authorize_pasid(vm, gpu, pasid, as, provider), Status::ok);
    CHECK_STATUS(source.begin_migration(vm), Status::ok);

    const auto exported = source.export_snapshot(vm);
    CHECK_STATUS(exported.status, Status::ok);
    CHECK(exported.snapshot.devices.size() == 1);
    CHECK(exported.snapshot.devices[0].pasids[0].external_provider);

    AtsIsolation destination;
    CHECK_STATUS(destination.import_snapshot(exported.snapshot), Status::invalid_snapshot);

    bool resolver_called = false;
    const Status imported = destination.import_snapshot(
        exported.snapshot,
        [&](DomainId domain, const RequesterId& requester, Pasid requested_pasid,
            AddressSpaceId requested_as) -> std::shared_ptr<TranslationProvider> {
            resolver_called = true;
            CHECK(domain == vm);
            CHECK(requester == gpu);
            CHECK(requested_pasid == pasid);
            CHECK(requested_as == as);
            return provider;
        });
    CHECK_STATUS(imported, Status::ok);
    CHECK(resolver_called);

    const auto result = destination.translate(request(vm, gpu, pasid, as, 0x1234));
    CHECK_STATUS(result.status, Status::ok);
    CHECK(result.translated_address == 0xE234);
}

void test_address_space_page_table_guards() {
    netsec::AddressSpacePageTable table(kPage);
    const MemoryPermissions rw{true, true, false, false};
    CHECK_STATUS(table.map_range(0x1000, 0x9000, kPage, rw), Status::ok);
    CHECK_STATUS(table.map_range(0x1000, 0x9000, kPage, rw), Status::mapping_conflict);
    CHECK_STATUS(table.map_range(0x1001, 0x9000, kPage, rw), Status::invalid_argument);
    CHECK_STATUS(table.map_range(0x2000, 0x9001, kPage, rw), Status::invalid_argument);

    const auto mapped = table.lookup(0x1010, AccessType::read);
    CHECK(mapped.status == LookupStatus::present);
    CHECK(mapped.translation.translated_address == 0x9010);

    const auto denied = table.lookup(0x1010, AccessType::execute);
    CHECK(denied.status == LookupStatus::permission_denied);
    const auto missing = table.lookup(0x2010, AccessType::read);
    CHECK(missing.status == LookupStatus::not_present);

    CHECK_STATUS(table.unmap_range(0x1000, kPage), Status::ok);
    CHECK(table.lookup(0x1010, AccessType::read).status == LookupStatus::not_present);
}

void test_platform_supplied_page_table() {
    AtsIsolation isolation;
    const DomainId vm = DomainId::from(81);
    const RequesterId nic = device(11, 0, 0x90, 0, 0);
    const Pasid pasid = Pasid::from(41);
    const AddressSpaceId as = AddressSpaceId::from(0x410);
    auto table = std::make_shared<netsec::AddressSpacePageTable>(kPage);

    CHECK_STATUS(isolation.register_domain(vm), Status::ok);
    CHECK_STATUS(isolation.attach_device(vm, nic), Status::ok);
    CHECK_STATUS(isolation.authorize_pasid(vm, nic, pasid, as, table), Status::ok);
    CHECK_STATUS(isolation.map_range(vm, nic, pasid, as, 0x2000, 0x7000, kPage), Status::ok);

    const auto result = isolation.translate(request(vm, nic, pasid, as, 0x2010));
    CHECK_STATUS(result.status, Status::ok);
    CHECK(result.translated_address == 0x7010);

    table->unmap_range(0x2000, kPage);
    CHECK_STATUS(isolation.invalidate_pasid(vm, nic, pasid), Status::ok);
    const auto gone = isolation.translate(request(vm, nic, pasid, as, 0x2010));
    CHECK_STATUS(gone.status, Status::mapping_not_found);
}

void test_migration_preserves_page_size_and_cancel() {
    AtsIsolation source(netsec::AtsConfig{65536, 0});
    const DomainId vm = DomainId::from(91);
    const RequesterId gpu = device(12, 0, 0xA0, 0, 0);
    const Pasid pasid = Pasid::from(51);
    const AddressSpaceId as = AddressSpaceId::from(0x510);
    const std::uint64_t iova = 0x0001'0000ULL;
    const std::uint64_t phys = 0x0009'0000ULL;

    CHECK_STATUS(source.register_domain(vm), Status::ok);
    CHECK_STATUS(source.attach_device(vm, gpu), Status::ok);
    CHECK_STATUS(source.authorize_pasid(vm, gpu, pasid, as), Status::ok);
    CHECK_STATUS(source.map_range(vm, gpu, pasid, as, iova, phys, 65536), Status::ok);
    CHECK_STATUS(source.translate(request(vm, gpu, pasid, as, iova + 0x20)).status, Status::ok);

    CHECK_STATUS(source.begin_migration(vm), Status::ok);
    CHECK_STATUS(source.cancel_migration(vm), Status::ok);
    const auto resumed = source.translate(request(vm, gpu, pasid, as, iova + 0x20));
    CHECK_STATUS(resumed.status, Status::ok);
    CHECK(resumed.translated_address == phys + 0x20);

    CHECK_STATUS(source.begin_migration(vm), Status::ok);
    const auto exported = source.export_snapshot(vm);
    CHECK_STATUS(exported.status, Status::ok);
    CHECK(exported.snapshot.devices[0].pasids[0].page_size == 65536);

    AtsIsolation destination;
    CHECK_STATUS(destination.import_snapshot(exported.snapshot), Status::ok);
    const auto rebuilt = destination.translate(request(vm, gpu, pasid, as, iova + 0x20));
    CHECK_STATUS(rebuilt.status, Status::ok);
    CHECK(rebuilt.translated_address == phys + 0x20);
    CHECK(rebuilt.page_size == 65536);
}

} // namespace

int main() {
    test_legitimate_passthrough();
    test_cross_vm_hit_rejected();
    test_pasid_forgery_rejected();
    test_invalidation_race();
    test_scoped_invalidation_and_race_scope();
    test_live_migration_rebuilds_domain();
    test_migration_rejects_unrebuildable_provider();
    test_address_space_page_table_guards();
    test_platform_supplied_page_table();
    test_migration_preserves_page_size_and_cancel();

    if (failures != 0) {
        std::cerr << failures << " test check(s) failed\n";
        return 1;
    }
    std::cout << "all ATS isolation tests passed\n";
    return 0;
}
