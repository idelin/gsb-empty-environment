#include <atomic>
#include <cassert>
#include <cstdio>
#include <thread>
#include <type_traits>
#include <utility>

#include "drc/deferred_cell.hpp"

using namespace drc;

namespace {

// Object with destructor counting, so tests can observe exactly when
// reclamation happens.
struct Counted {
    static std::atomic<int> alive;
    static std::atomic<int> destroyed;
    int value;
    explicit Counted(int v) : value(v) { alive.fetch_add(1); }
    ~Counted() { destroyed.fetch_add(1); alive.fetch_sub(1); }
};
std::atomic<int> Counted::alive{0};
std::atomic<int> Counted::destroyed{0};

// Deterministic address-reuse pool: the most recently freed block is handed
// out again by the next create(), so the "new object at the old address"
// scenario is exercised on every run instead of depending on malloc.
template <typename T>
class ReusePool {
public:
    template <typename... Args>
    T* create(Args&&... args) {
        void* mem = free_ ? free_ : ::operator new(sizeof(T));
        free_ = nullptr;
        return new (mem) T(std::forward<Args>(args)...);
    }
    void destroy(T* ptr) {
        ptr->~T();
        free_ = ptr;
    }
    ~ReusePool() { if (free_) ::operator delete(free_); }

private:
    void* free_ = nullptr;
};

void spin_until(const std::atomic<bool>& flag) {
    while (!flag.load(std::memory_order_acquire)) std::this_thread::yield();
}

void test_guard_type_traits() {
    static_assert(!std::is_copy_constructible<ReaderGuard<int>>::value,
                  "read guard must not be copy-constructible");
    static_assert(!std::is_copy_assignable<ReaderGuard<int>>::value,
                  "read guard must not be copy-assignable");
    static_assert(std::is_move_constructible<ReaderGuard<int>>::value,
                  "read guard must be move-constructible");
    static_assert(std::is_move_assignable<ReaderGuard<int>>::value,
                  "read guard must be move-assignable");
    static_assert(!std::is_copy_constructible<EpochReclaimer::Pin>::value,
                  "epoch pin must not be copyable");
    static_assert(std::is_move_constructible<EpochReclaimer::Pin>::value,
                  "epoch pin must be movable");
    static_assert(std::is_copy_constructible<Handle<int>>::value,
                  "handle token stays copyable");
    std::puts("[ok] guards are move-only, handles are copyable");
}

void test_retire_blocks_new_guards() {
    EpochReclaimer rec;
    DeferredCell<Counted> cell(rec);
    cell.publish(new Counted(7));
    Handle<Counted> handle = cell.handle();

    {
        auto guard = try_read(handle);
        assert(guard && (*guard)->value == 7);
        auto moved = std::move(*guard); // guards move
        assert(moved->value == 7);
    }

    const int destroyed_before = Counted::destroyed.load();
    cell.retire();
    assert(cell.retired());
    assert(!try_read(handle).has_value());        // old handle rejected
    assert(!try_read(cell.handle()).has_value()); // fresh handle on retired cell rejected

    while (rec.pending() != 0) rec.collect();
    assert(Counted::destroyed.load() == destroyed_before + 1);
    std::puts("[ok] retired handles yield no new guards; reclamation follows");
}

void test_paused_reader_delays_reclaim() {
    EpochReclaimer rec;
    DeferredCell<Counted> cell(rec);
    cell.publish(new Counted(42));
    Handle<Counted> handle = cell.handle();

    std::atomic<bool> reader_holding{false};
    std::atomic<bool> proceed{false};
    std::atomic<int> observed{0};

    // Reader pins the epoch, then pauses artificially before dereferencing.
    std::thread reader([&] {
        auto guard = try_read(handle);
        assert(guard.has_value());
        reader_holding.store(true, std::memory_order_release);
        spin_until(proceed);
        // Dereference only after main has retired the handle: the object
        // must still be intact.
        observed.store((*guard)->value, std::memory_order_release);
    });

    spin_until(reader_holding);

    const int destroyed_before = Counted::destroyed.load();
    cell.retire();
    for (int i = 0; i < 8; ++i) rec.collect();
    assert(Counted::destroyed.load() == destroyed_before &&
           "paused reader must block reclamation");
    assert(rec.pending() == 1 && "object waits in the limbo list");
    assert(!try_read(handle).has_value() && "no new guards while retired");

    proceed.store(true, std::memory_order_release);
    reader.join();
    assert(observed.load() == 42 && "paused reader still saw the old object");

    while (rec.pending() != 0) rec.collect();
    assert(Counted::destroyed.load() == destroyed_before + 1 &&
           "reclamation completes once the reader finished");
    std::puts("[ok] paused reader pins its epoch; reclaim waits for it");
}

void test_address_reuse_does_not_leak_through_old_handle() {
    EpochReclaimer rec;
    ReusePool<Counted> pool;
    DeferredCell<Counted> cell(rec, [&](Counted* p) { pool.destroy(p); });

    Counted* first = pool.create(42);
    cell.publish(first);
    Handle<Counted> stale = cell.handle();

    std::atomic<bool> reader_holding{false};
    std::atomic<bool> proceed{false};
    std::atomic<int> observed{0};

    std::thread reader([&] {
        auto guard = try_read(stale);
        assert(guard.has_value());
        reader_holding.store(true, std::memory_order_release);
        spin_until(proceed);
        observed.store((*guard)->value, std::memory_order_release);
    });
    spin_until(reader_holding);

    const int destroyed_before = Counted::destroyed.load();
    cell.retire();
    for (int i = 0; i < 8; ++i) rec.collect();
    // While the reader is paused the old block cannot be freed, so its
    // address cannot be reused underneath the in-flight read.
    assert(Counted::destroyed.load() == destroyed_before);

    proceed.store(true, std::memory_order_release);
    reader.join();
    assert(observed.load() == 42 && "in-flight read saw the old object, not a reuse");

    while (rec.pending() != 0) rec.collect();
    assert(Counted::destroyed.load() == destroyed_before + 1);

    // The pool deterministically reuses the just-freed address.
    Counted* second = pool.create(99);
    assert(static_cast<void*>(second) == static_cast<void*>(first) &&
           "test must exercise real address reuse");
    cell.publish(second);
    Handle<Counted> fresh = cell.handle();

    // ABA check: the stale handle names the old generation and must not
    // reach the new object, even though it sits at the same address.
    assert(!try_read(stale).has_value() && "stale handle must not see the new object");
    auto guard = try_read(fresh);
    assert(guard && (*guard)->value == 99 && "fresh handle sees the new object");
    guard = ReaderGuard<Counted>{};

    cell.retire();
    while (rec.pending() != 0) rec.collect();
    assert(Counted::destroyed.load() == destroyed_before + 2);
    assert(Counted::alive.load() == 0);
    std::puts("[ok] address reuse is safe: stale handles never read new objects");
}

} // namespace

int main() {
    test_guard_type_traits();
    test_retire_blocks_new_guards();
    test_paused_reader_delays_reclaim();
    test_address_reuse_does_not_leak_through_old_handle();
    std::puts("all tests passed");
    return 0;
}
