#include <cstdio>
#include <cstring>
#include <vector>

#include "netsec.hpp"

namespace {

int failures = 0;

#define EXPECT(cond)                                                  \
    do {                                                              \
        if (!(cond)) {                                                \
            std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__,        \
                         __LINE__, #cond);                            \
            ++failures;                                               \
        }                                                             \
    } while (0)

// Third-party Future plugin that stores a self-referential pointer, as in
// the async-runtime scenario.
struct SelfRefFuture {
    long state;
    const SelfRefFuture* self;
    explicit SelfRefFuture(long s) : state(s), self(this) {}
};

void pin_future(netsec::PinRegistry& reg, const SelfRefFuture* f,
                const char* name, bool movable = false) {
    reg.pin(f, sizeof(SelfRefFuture),
            reinterpret_cast<const void* const*>(&f->self), name, movable);
}

std::size_t count_kind(const netsec::PinRegistry& reg,
                       netsec::ViolationKind kind) {
    std::size_t n = 0;
    for (const auto& v : reg.violations()) {
        if (v.kind == kind) ++n;
    }
    return n;
}

// Simulated scheduler byte-swap (e.g. std::swap on the plugin objects),
// instrumented the way the runtime would intercept memcpy.
void swap_slots(netsec::PinRegistry& reg, void* a, void* b, std::size_t n) {
    std::vector<unsigned char> tmp(n);
    std::memcpy(tmp.data(), a, n);
    reg.notify_write(a, n);
    std::memcpy(a, b, n);
    reg.notify_write(b, n);
    std::memcpy(b, tmp.data(), n);
}

void test_legal_pinned_lifecycle() {
    netsec::PinRegistry reg;
    auto* slot = new SelfRefFuture(42);
    pin_future(reg, slot, "future-1");

    EXPECT(reg.is_pinned(slot));
    EXPECT(reg.check_all());
    EXPECT(reg.check(slot));

    reg.notify_destruct_begin(slot);
    slot->~SelfRefFuture();
    reg.notify_destruct_end(slot);
    reg.unpin(slot);
    reg.notify_free(slot);
    operator delete(slot);

    EXPECT(!reg.is_pinned(slot));
    EXPECT(reg.violations().empty());
    EXPECT(reg.first_violation() == nullptr);
}

void test_swap_after_pin() {
    netsec::PinRegistry reg;
    SelfRefFuture a(1), b(2);
    pin_future(reg, &a, "future-a");
    pin_future(reg, &b, "future-b");
    EXPECT(reg.check_all());

    swap_slots(reg, &a, &b, sizeof(SelfRefFuture));

    EXPECT(!reg.check_all());
    EXPECT(count_kind(reg, netsec::ViolationKind::SlotOverwritten) >= 2);
    // After the byte swap, a's self field holds &b and vice versa.
    EXPECT(count_kind(reg, netsec::ViolationKind::DanglingSelfPointer) >= 1);
    const netsec::Violation* first = reg.first_violation();
    EXPECT(first != nullptr);
    EXPECT(first->kind == netsec::ViolationKind::SlotOverwritten);
    EXPECT(first->sequence == 1);
}

void test_slot_reuse_before_destruction() {
    netsec::PinRegistry reg;
    SelfRefFuture f(7);
    pin_future(reg, &f, "future-reused");

    // Runtime recycles the slot for a new allocation without running the
    // destructor first.
    reg.notify_reuse(&f);
    EXPECT(count_kind(reg, netsec::ViolationKind::PrematureReuse) == 1);
    const netsec::Violation* first = reg.first_violation();
    EXPECT(first != nullptr);
    EXPECT(first->kind == netsec::ViolationKind::PrematureReuse);
    EXPECT(first->slot == &f);

    // Freeing before destruction completes is likewise a violation.
    reg.notify_free(&f);
    EXPECT(count_kind(reg, netsec::ViolationKind::PrematureReuse) == 2);
    EXPECT(!reg.is_pinned(&f));
}

void test_movable_type() {
    netsec::PinRegistry reg;

    // Movable object: relocated with scheduler notification, self pointer
    // fixed up by the object itself.
    SelfRefFuture movable(10);
    pin_future(reg, &movable, "future-movable", /*movable=*/true);
    alignas(SelfRefFuture) unsigned char buffer[sizeof(SelfRefFuture)];
    auto* dst = reinterpret_cast<SelfRefFuture*>(buffer);
    std::memcpy(dst, &movable, sizeof(SelfRefFuture));
    dst->self = dst;
    reg.notify_move(&movable, dst);
    EXPECT(reg.violations().empty());
    EXPECT(reg.is_pinned(dst));
    EXPECT(!reg.is_pinned(&movable));
    EXPECT(reg.check_all());

    // Non-movable object: any move is reported, and it is the first
    // violation event for this registry run.
    SelfRefFuture pinned(11);
    pin_future(reg, &pinned, "future-pinned");
    alignas(SelfRefFuture) unsigned char buffer2[sizeof(SelfRefFuture)];
    auto* dst2 = reinterpret_cast<SelfRefFuture*>(buffer2);
    std::memcpy(dst2, &pinned, sizeof(SelfRefFuture));
    reg.notify_move(&pinned, dst2);
    EXPECT(count_kind(reg, netsec::ViolationKind::IllegalMove) == 1);
    const netsec::Violation* first = reg.first_violation();
    EXPECT(first != nullptr);
    EXPECT(first->kind == netsec::ViolationKind::IllegalMove);
    EXPECT(first->object_name == "future-pinned");
}

void test_assignment_overwrite_and_dangling_self() {
    netsec::PinRegistry reg;
    SelfRefFuture f(3);
    pin_future(reg, &f, "future-assigned");

    // Simulated assignment / memcpy into the pinned slot.
    SelfRefFuture other(4);
    reg.notify_write(&f, sizeof f);
    std::memcpy(&f, &other, sizeof f);

    EXPECT(!reg.check(&f));
    EXPECT(count_kind(reg, netsec::ViolationKind::SlotOverwritten) >= 1);
    // The copied bytes carry other's self pointer, now dangling.
    EXPECT(count_kind(reg, netsec::ViolationKind::DanglingSelfPointer) >= 1);
    EXPECT(reg.first_violation()->kind ==
           netsec::ViolationKind::SlotOverwritten);
}

}  // namespace

int main() {
    test_legal_pinned_lifecycle();
    test_swap_after_pin();
    test_slot_reuse_before_destruction();
    test_movable_type();
    test_assignment_overwrite_and_dangling_self();

    if (failures == 0) {
        std::printf("all pin-invariant tests passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d expectation(s) failed\n", failures);
    return 1;
}
