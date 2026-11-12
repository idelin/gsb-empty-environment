#include "netsec.hpp"

#include <atomic>
#include <cassert>
#include <chrono>
#include <condition_variable>
#include <cstring>
#include <iostream>
#include <memory>
#include <mutex>
#include <random>
#include <set>
#include <thread>
#include <unordered_map>
#include <vector>

using namespace netsec;

namespace {

int g_failures = 0;

#define CHECK(cond)                                                          \
    do {                                                                     \
        if (!(cond)) {                                                       \
            ++g_failures;                                                    \
            std::cerr << "CHECK failed: " #cond " at " __FILE__ << ":"       \
                      << __LINE__ << "\n";                                   \
        }                                                                    \
    } while (0)

// In-memory model of an untrusted, replaceable file. Each published generation
// gets its own byte buffer; truncation and replacement never mutate old bytes,
// mirroring how a versioned page source must preserve leased data.
struct FakeFile {
    std::vector<std::byte> bytes;

    explicit FakeFile(std::size_t size, std::byte fill = std::byte{0})
        : bytes(size) {
        if (fill == std::byte{0}) {
            for (std::size_t i = 0; i < size; ++i) {
                bytes[i] = static_cast<std::byte>(i & 0xFF);
            }
        } else {
            for (auto& b : bytes) b = fill;
        }
    }
};

class FakePageReader : public IPageReader {
public:
    void addGeneration(VersionId id, std::shared_ptr<const FakeFile> file) {
        std::lock_guard<std::mutex> lock(mutex_);
        generations_[id] = std::move(file);
    }

    // When non-empty, reads of those page indices fail with IoError once.
    std::set<std::uint64_t> io_error_pages;
    std::atomic<int> read_calls{0};

    std::size_t page_size_hint = 64;
    int block_page_index = -1;
    bool block_open = false;
    std::mutex block_mutex;
    std::condition_variable block_cv;
    std::condition_variable reached_cv;
    bool reached_block = false;
    std::atomic<bool> cancel_requested{false};

    void openGate() {
        std::lock_guard<std::mutex> lock(block_mutex);
        block_open = true;
        block_cv.notify_all();
    }

    void waitUntilBlocked() {
        std::unique_lock<std::mutex> lock(block_mutex);
        reached_cv.wait(lock, [this] { return reached_block; });
    }

    PageRead readPage(VersionId version, std::uint64_t offset,
                      std::byte* dst, std::size_t dst_size) override {
        read_calls.fetch_add(1, std::memory_order_relaxed);

        if (block_page_index >= 0 &&
            (offset / page_size_hint) ==
                static_cast<std::uint64_t>(block_page_index)) {
            {
                std::lock_guard<std::mutex> lock(block_mutex);
                reached_block = true;
            }
            reached_cv.notify_all();
            std::unique_lock<std::mutex> lock(block_mutex);
            block_cv.wait(lock, [this] {
                return block_open ||
                       cancel_requested.load(std::memory_order_acquire);
            });
        }

        std::shared_ptr<const FakeFile> file;
        {
            std::lock_guard<std::mutex> lock(mutex_);
            auto it = generations_.find(version);
            if (it == generations_.end()) {
                return {PageStatus::Unavailable, 0};
            }
            file = it->second;
        }

        if (offset >= file->bytes.size()) {
            return {PageStatus::Unavailable, 0};
        }

        const std::uint64_t page_index = offset / page_size_hint;
        if (io_error_pages.erase(page_index) > 0) {
            return {PageStatus::IoError, 0};
        }

        const std::size_t avail =
            static_cast<std::size_t>(file->bytes.size() - offset);
        const std::size_t n = std::min(avail, dst_size);
        std::memcpy(dst, file->bytes.data() + offset, n);
        return {PageStatus::Ok, n};
    }

private:
    std::mutex mutex_;
    std::unordered_map<VersionId, std::shared_ptr<const FakeFile>>
        generations_;
};

ReadResult readInto(Lease& lease, std::uint64_t offset, std::size_t len,
                    std::vector<std::byte>& buf) {
    buf.assign(len, std::byte{0});
    ReadResult r = lease.read(offset, buf.data(), len);
    CHECK(r.bytes <= len);
    buf.resize(r.bytes);
    return r;
}

bool sameBytes(const std::vector<std::byte>& a,
               const std::vector<std::byte>& b) {
    return a.size() == b.size() &&
           std::memcmp(a.data(), b.data(), a.size()) == 0;
}

} // namespace

// 1. Cross-page reads and exact-prefix semantics under truncation.
static void testCrossPageReadAndTruncation() {
    constexpr std::size_t kPage = 64;
    auto volume = makeVolume(VolumeConfig{kPage, 16});
    auto reader = std::make_shared<FakePageReader>();
    reader->page_size_hint = kPage;

    constexpr std::size_t kSize = kPage * 4; // 256 bytes, 4 pages.
    auto file = std::make_shared<FakeFile>(kSize);
    VersionId v1 = volume->publishVersion(reader, kSize);
    reader->addGeneration(v1, file);

    Lease lease = volume->acquireLease(0, kSize);
    CHECK(lease.valid());
    CHECK(lease.version() == v1);

    // Unaligned start, spans three pages.
    const std::uint64_t off = kPage - 10;
    const std::size_t len = kPage * 2 + 25;
    std::vector<std::byte> buf;
    ReadResult r = readInto(lease, off, len, buf);
    CHECK(r.outcome == ReadOutcome::Complete);
    CHECK(buf.size() == len);
    CHECK(std::memcmp(buf.data(), file->bytes.data() + off, len) == 0);

    // File shortened after the lease was acquired: pinned version is stable.
    VersionId v2 = volume->truncateTo(kPage);
    reader->addGeneration(v2, file);
    CHECK(v2 == v1 + 1);
    CHECK(volume->currentVersion() == v2);

    std::vector<std::byte> buf2;
    readInto(lease, off, len, buf2);
    CHECK(sameBytes(buf, buf2));

    // New lease on the shortened file: reads past EOF return exact prefix.
    Lease short_lease = volume->acquireLease(0, kSize);
    std::vector<std::byte> big(kSize);
    ReadResult rs = short_lease.read(0, big.data(), big.size());
    CHECK(rs.outcome == ReadOutcome::Truncated);
    CHECK(rs.bytes == kPage);
    CHECK(std::memcmp(big.data(), file->bytes.data(), kPage) == 0);

    // Prefix precision when the read starts inside the valid tail page.
    ReadResult rs2 = short_lease.read(kPage - 5, big.data(), 20);
    CHECK(rs2.outcome == ReadOutcome::Truncated);
    CHECK(rs2.bytes == 5);

    lease.release();
    short_lease.release();
    CHECK(volume->liveLeaseCount() == 0);
}

// 2. Version replacement keeps old leases fully readable.
static void testVersionReplacement() {
    constexpr std::size_t kPage = 32;
    auto volume = makeVolume(VolumeConfig{kPage, 8});

    auto reader_a = std::make_shared<FakePageReader>();
    reader_a->page_size_hint = kPage;
    auto file_a = std::make_shared<FakeFile>(kPage * 3);
    VersionId va = volume->publishVersion(reader_a, file_a->bytes.size());
    reader_a->addGeneration(va, file_a);

    Lease lease_a = volume->acquireLease(0, file_a->bytes.size());
    std::vector<std::byte> snapshot_a;
    readInto(lease_a, 0, file_a->bytes.size(), snapshot_a);
    CHECK(snapshot_a.size() == file_a->bytes.size());

    auto reader_b = std::make_shared<FakePageReader>();
    reader_b->page_size_hint = kPage;
    auto file_b = std::make_shared<FakeFile>(kPage * 5, std::byte{0xAB});
    VersionId vb = volume->replaceWith(reader_b, file_b->bytes.size());
    reader_b->addGeneration(vb, file_b);
    CHECK(vb == va + 1);

    // Old lease still resolves through the old reader and old bytes.
    std::vector<std::byte> still_a;
    readInto(lease_a, 0, file_a->bytes.size(), still_a);
    CHECK(sameBytes(snapshot_a, still_a));

    Lease lease_b = volume->acquireLease(0, file_b->bytes.size());
    CHECK(lease_b.version() == vb);
    std::vector<std::byte> got_b;
    readInto(lease_b, 0, file_b->bytes.size(), got_b);
    CHECK(got_b.size() == file_b->bytes.size());
    CHECK(got_b[0] == std::byte{0xAB});

    lease_a.release();
    CHECK(!lease_a.valid());
    CHECK(volume->liveLeaseCount() == 1);
}

// 3. Cache eviction cannot invalidate pages pinned by a lease.
static void testCacheEvictionPinning() {
    constexpr std::size_t kPage = 16;
    auto volume = makeVolume(VolumeConfig{kPage, 2}); // Two-page LRU.
    auto reader = std::make_shared<FakePageReader>();
    reader->page_size_hint = kPage;

    auto file = std::make_shared<FakeFile>(kPage * 6);
    VersionId v = volume->publishVersion(reader, file->bytes.size());
    reader->addGeneration(v, file);

    Lease lease = volume->acquireLease(0, file->bytes.size());

    std::vector<std::byte> full;
    readInto(lease, 0, file->bytes.size(), full);
    CHECK(full.size() == file->bytes.size());
    CHECK(volume->cachedPageCount() <= 2);

    // Re-read while pages are still hot in the two-entry cache as well as
    // pinned: no additional reader calls.
    const int calls_after_first = reader->read_calls.load();
    std::vector<std::byte> again;
    readInto(lease, 0, file->bytes.size(), again);
    CHECK(sameBytes(full, again));
    CHECK(reader->read_calls.load() == calls_after_first);

    // A new version and a second lease churn the shared cache.
    auto file2 = std::make_shared<FakeFile>(kPage * 4, std::byte{0x77});
    VersionId v2 = volume->replaceWith(reader, file2->bytes.size());
    reader->addGeneration(v2, file2);
    Lease lease2 = volume->acquireLease(0, file2->bytes.size());
    std::vector<std::byte> other;
    readInto(lease2, 0, file2->bytes.size(), other);
    CHECK(volume->cachedPageCount() <= 2);

    // Eviction removed v1 pages from the shared cache, but the lease still
    // holds them pinned: data must be byte-identical and served from pins.
    const int calls_after_churn = reader->read_calls.load();
    std::vector<std::byte> after_churn;
    readInto(lease, 0, file->bytes.size(), after_churn);
    CHECK(sameBytes(full, after_churn));
    CHECK(reader->read_calls.load() == calls_after_churn);
}

// 4. Cancellation stops at a page boundary with the exact prefix.
static void testCancellation() {
    constexpr std::size_t kPage = 32;
    auto volume = makeVolume(VolumeConfig{kPage, 8});
    auto reader = std::make_shared<FakePageReader>();
    reader->page_size_hint = kPage;

    auto file = std::make_shared<FakeFile>(kPage * 4);
    VersionId v = volume->publishVersion(reader, file->bytes.size());
    reader->addGeneration(v, file);

    // Pre-cancelled lease: immediate Cancelled with zero prefix.
    Lease lease = volume->acquireLease(0, file->bytes.size());
    lease.cancel();
    std::vector<std::byte> buf(file->bytes.size());
    ReadResult r = lease.read(0, buf.data(), buf.size());
    CHECK(r.outcome == ReadOutcome::Cancelled);
    CHECK(r.bytes == 0);
    CHECK(lease.cancelled());

    // In-flight cancellation at page 2: pages 0 and 1 complete first.
    Lease lease2 = volume->acquireLease(0, file->bytes.size());
    reader->block_page_index = 2;
    std::thread worker([&] {
        ReadResult rr = lease2.read(0, buf.data(), buf.size());
        CHECK(rr.outcome == ReadOutcome::Cancelled);
        // The blocked page itself completes before cancellation is observed,
        // so the exact prefix covers pages 0..2.
        CHECK(rr.bytes == kPage * 3);
        CHECK(std::memcmp(buf.data(), file->bytes.data(), kPage * 3) == 0);
    });

    reader->waitUntilBlocked();
    lease2.cancel();
    reader->cancel_requested.store(true);
    reader->openGate();
    worker.join();

    // Later reads fail without any additional reader traffic.
    const int calls = reader->read_calls.load();
    ReadResult rr2 = lease2.read(0, buf.data(), 8);
    CHECK(rr2.outcome == ReadOutcome::Cancelled);
    CHECK(reader->read_calls.load() == calls);
}

// 5. Reader I/O failure yields ReadError with the exact prior prefix.
static void testIoErrorPrefix() {
    constexpr std::size_t kPage = 24;
    auto volume = makeVolume(VolumeConfig{kPage, 8});
    auto reader = std::make_shared<FakePageReader>();
    reader->page_size_hint = kPage;

    auto file = std::make_shared<FakeFile>(kPage * 4);
    VersionId v = volume->publishVersion(reader, file->bytes.size());
    reader->addGeneration(v, file);
    reader->io_error_pages = {2};

    Lease lease = volume->acquireLease(0, file->bytes.size());
    std::vector<std::byte> buf(file->bytes.size());
    ReadResult r = lease.read(0, buf.data(), buf.size());
    CHECK(r.outcome == ReadOutcome::ReadError);
    CHECK(r.bytes == kPage * 2);
    CHECK(std::memcmp(buf.data(), file->bytes.data(), kPage * 2) == 0);
}

// 6. Range validation and invalid lease behavior.
static void testBadRanges() {
    auto volume = makeVolume(VolumeConfig{64, 4});
    auto reader = std::make_shared<FakePageReader>();
    reader->page_size_hint = 64;
    auto file = std::make_shared<FakeFile>(128);
    VersionId v = volume->publishVersion(reader, 128);
    reader->addGeneration(v, file);

    Lease lease = volume->acquireLease(16, 32);
    std::byte buf[32]{};
    CHECK(lease.read(15, buf, 1).outcome == ReadOutcome::BadRange);
    CHECK(lease.read(49, buf, 1).outcome == ReadOutcome::BadRange);
    CHECK(lease.read(48, buf, 1).outcome == ReadOutcome::BadRange);
    CHECK(lease.read(16, nullptr, 1).outcome == ReadOutcome::BadRange);
    CHECK(lease.read(16, buf, 0).outcome == ReadOutcome::BadRange);
    CHECK(lease.read(16, buf, 32).outcome == ReadOutcome::Complete);

    Lease empty = volume->acquireLease(0, 0);
    CHECK(!empty.valid());
    CHECK(empty.read(0, buf, 1).outcome == ReadOutcome::Expired);
}

// 7. Concurrent readers, version replacement and lease release: no dangling
//    references; every read returns bytes matching its fixed generation.
static void testConcurrentLifecycle() {
    constexpr std::size_t kPage = 32;
    auto volume = makeVolume(VolumeConfig{kPage, 4});
    auto reader = std::make_shared<FakePageReader>();
    reader->page_size_hint = kPage;

    constexpr int kGenerations = 8;
    std::vector<std::shared_ptr<FakeFile>> files;
    std::mutex map_mutex;
    std::unordered_map<VersionId, std::shared_ptr<FakeFile>> version_file;
    auto publish = [&](std::shared_ptr<FakeFile> f) {
        VersionId id = volume->publishVersion(reader, f->bytes.size());
        reader->addGeneration(id, f);
        std::lock_guard<std::mutex> lock(map_mutex);
        version_file[id] = std::move(f);
        return id;
    };
    for (int gen = 0; gen < kGenerations; ++gen) {
        auto f = std::make_shared<FakeFile>(kPage * (gen + 2),
                                            static_cast<std::byte>(gen));
        publish(f);
        files.push_back(f);
    }

    std::atomic<bool> stop{false};
    std::atomic<int> bad_reads{0};

    // Re-publish generations concurrently to stress replacement paths.
    std::thread publisher([&] {
        for (int i = 0; i < 200; ++i) {
            publish(files[i % files.size()]);
        }
        stop.store(true);
    });

    auto worker = [&] {
        static std::atomic<unsigned> seed{0x9e3779b9u};
        std::mt19937 rng(seed.fetch_add(0x85ebca6bu,
                                        std::memory_order_relaxed));
        while (!stop.load()) {
            if (volume->currentVersion() == 0) continue;
            // Lease over a fixed large window; compare against the file of
            // the version the lease actually binds to, avoiding a race on
            // currentVersion between lookup and acquisition.
            Lease lease = volume->acquireLease(0, kPage * 16);
            if (!lease.valid()) continue;

            std::shared_ptr<FakeFile> f;
            {
                std::lock_guard<std::mutex> lock(map_mutex);
                auto it = version_file.find(lease.version());
                if (it == version_file.end()) continue;
                f = it->second;
            }

            std::vector<std::byte> buf(f->bytes.size());
            ReadResult r = lease.read(0, buf.data(), buf.size());
            if (r.outcome == ReadOutcome::Complete &&
                r.bytes == f->bytes.size() &&
                std::memcmp(buf.data(), f->bytes.data(), f->bytes.size()) !=
                    0) {
                bad_reads.fetch_add(1);
            }
            if ((rng() & 1) == 0) lease.cancel();
        }
    };

    std::vector<std::thread> pool;
    for (int i = 0; i < 4; ++i) pool.emplace_back(worker);
    publisher.join();
    for (auto& t : pool) t.join();

    CHECK(bad_reads.load() == 0);
    CHECK(volume->liveLeaseCount() == 0);
}

int main() {
    testCrossPageReadAndTruncation();
    testVersionReplacement();
    testCacheEvictionPinning();
    testCancellation();
    testIoErrorPrefix();
    testBadRanges();
    testConcurrentLifecycle();

    if (g_failures == 0) {
        std::cout << "all lease tests passed\n";
        return 0;
    }
    std::cerr << g_failures << " check(s) failed\n";
    return 1;
}
