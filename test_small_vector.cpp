#include "small_vector.hpp"

#include <cstdio>
#include <vector>

static int failures = 0;
static long double_destructions = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            ++failures;                                                    \
            std::printf("CHECK failed at %s:%d: %s\n", __FILE__, __LINE__, \
                        #cond);                                            \
        }                                                                  \
    } while (0)

struct InjectError {};

// Element type with lifetime tracking and fault injection.
//   * live counts constructed-not-yet-destroyed objects (leak detection).
//   * the destructor flags double destruction of the same object.
//   * fail_at >= 0 arms injection: the (fail_at)-th subsequent construction
//     (0-based) throws InjectError.
// NoThrowMove selects whether the move constructor/assignment are noexcept.
// For NoThrowMove == true the move constructor never injects (a noexcept
// constructor must not throw); copies still can.
template <bool NoThrowMove>
struct Counted {
    static inline long live = 0;
    static inline long copy_ctors = 0;
    static inline long move_ctors = 0;
    static inline long fail_at = -1;

    int id;
    bool destroyed;

    static void inject() {
        if (fail_at < 0) return;
        if (fail_at == 0) {
            fail_at = -1;
            throw InjectError{};
        }
        --fail_at;
    }

    explicit Counted(int v = 0) : id(v), destroyed(false) {
        inject();
        ++live;
    }
    Counted(const Counted& o) : id(o.id), destroyed(false) {
        inject();
        ++live;
        ++copy_ctors;
    }
    Counted(Counted&& o) noexcept(NoThrowMove) : id(o.id), destroyed(false) {
        if constexpr (!NoThrowMove) inject();
        ++live;
        ++move_ctors;
    }
    Counted& operator=(const Counted& o) {
        id = o.id;
        return *this;
    }
    Counted& operator=(Counted&& o) noexcept(NoThrowMove) {
        id = o.id;
        return *this;
    }
    ~Counted() {
        if (destroyed) ++double_destructions;
        destroyed = true;
        --live;
    }
};

template <typename T, std::size_t N>
std::vector<int> ids_of(const small_vector<T, N>& v) {
    std::vector<int> out;
    out.reserve(v.size());
    for (const auto& e : v) out.push_back(e.id);
    return out;
}

#define CHECK_IDS(v, ...) CHECK(ids_of(v) == std::vector<int>(__VA_ARGS__))

static void test_basic() {
    using C = Counted<false>;
    small_vector<C, 4> v;
    CHECK(v.size() == 0);
    CHECK(v.capacity() == 4);
    CHECK(v.uses_inline_storage());
    for (int i = 0; i < 4; ++i) v.push_back(C(i * 10));
    CHECK(v.size() == 4);
    CHECK(v.uses_inline_storage());
    CHECK(v.capacity() == 4);
    v.push_back(C(40));  // exceeds inline capacity: migrate to heap
    CHECK(v.size() == 5);
    CHECK(!v.uses_inline_storage());
    CHECK(v.capacity() >= 5);
    CHECK_IDS(v, {0, 10, 20, 30, 40});
    for (int i = 0; i < 9; ++i) v.push_back(C(i));
    CHECK(v.size() == 14);
    CHECK(v.capacity() >= 14);
    CHECK(v.front().id == 0);
    CHECK(v.back().id == 8);
}

static void test_insert_positions() {
    using C = Counted<true>;
    for (std::size_t init = 0; init <= 9; ++init) {
        for (std::size_t pos = 0; pos <= init; ++pos) {
            small_vector<C, 4> v;
            for (std::size_t i = 0; i < init; ++i) v.push_back(C((int)i));
            std::vector<int> want = ids_of(v);
            want.insert(want.begin() + (std::ptrdiff_t)pos, 999);
            v.insert(v.begin() + (std::ptrdiff_t)pos, C(999));
            CHECK_IDS(v, want);
            CHECK(v.size() == init + 1);
        }
    }
}

// For every container state (inline / full-inline / heap), every insert
// position and every construction failure point: a failed insert must leave
// contents, size, capacity and storage location unchanged, with no leaks.
template <bool NT, bool RValue>
static void test_insert_exception_safety() {
    using C = Counted<NT>;
    constexpr std::size_t N = 4;
    for (std::size_t init = 0; init <= 9; ++init) {
        for (std::size_t pos = 0; pos <= init; ++pos) {
            for (long fail = -1; fail <= (long)init + 3; ++fail) {
                C::fail_at = -1;
                small_vector<C, N> v;
                for (std::size_t i = 0; i < init; ++i) v.push_back(C((int)i));
                const std::vector<int> before = ids_of(v);
                const std::size_t cap_before = v.capacity();
                const bool inl_before = v.uses_inline_storage();

                C arg(777);
                (void)arg;
                const long live_before = C::live;
                bool threw = false;
                C::fail_at = fail;
                try {
                    if constexpr (RValue) {
                        v.insert(v.begin() + (std::ptrdiff_t)pos, C(777));
                    } else {
                        v.insert(v.begin() + (std::ptrdiff_t)pos, arg);
                    }
                } catch (const InjectError&) {
                    threw = true;
                }
                C::fail_at = -1;

                if (threw) {
                    CHECK(v.size() == init);
                    CHECK(v.capacity() == cap_before);
                    CHECK(v.uses_inline_storage() == inl_before);
                    CHECK(ids_of(v) == before);
                } else {
                    CHECK(v.size() == init + 1);
                    std::vector<int> want = before;
                    want.insert(want.begin() + (std::ptrdiff_t)pos, 777);
                    CHECK(ids_of(v) == want);
                    if (init < cap_before) CHECK(v.capacity() == cap_before);
                }
                CHECK(C::live == live_before + (threw ? 0 : 1));
            }
        }
    }
}

static void test_self_reference() {
    using C = Counted<false>;
    {  // inline storage, spare capacity
        small_vector<C, 4> v;
        for (int i = 0; i < 3; ++i) v.push_back(C(i + 1));
        v.insert(v.begin(), v[2]);
        CHECK_IDS(v, {3, 1, 2, 3});
    }
    {  // inline storage full: self-referential insert triggers migration
        small_vector<C, 4> v;
        for (int i = 0; i < 4; ++i) v.push_back(C(i + 1));
        v.insert(v.begin() + 1, v[0]);
        CHECK_IDS(v, {1, 1, 2, 3, 4});
        CHECK(!v.uses_inline_storage());
    }
    {  // heap resident
        small_vector<C, 4> v;
        for (int i = 0; i < 8; ++i) v.push_back(C(i + 1));
        v.insert(v.end(), v[3]);
        CHECK_IDS(v, {1, 2, 3, 4, 5, 6, 7, 8, 4});
        v.insert(v.begin() + 2, v[7]);
        CHECK_IDS(v, {1, 2, 8, 3, 4, 5, 6, 7, 8, 4});
    }
    {  // rvalue self-reference
        small_vector<C, 4> v;
        for (int i = 0; i < 3; ++i) v.push_back(C(i + 1));
        v.insert(v.begin(), std::move(v[1]));
        CHECK_IDS(v, {2, 1, 2, 3});
    }
    {  // self-referential push_back with reallocation
        small_vector<C, 4> v;
        for (int i = 0; i < 4; ++i) v.push_back(C(i + 1));
        v.push_back(v[0]);
        CHECK_IDS(v, {1, 2, 3, 4, 1});
    }
}

static void test_move_only_when_nothrow() {
    {  // noexcept move: relocation must move, never copy
        using C = Counted<true>;
        small_vector<C, 2> v;
        for (int i = 0; i < 2; ++i) v.push_back(C(i));
        C::copy_ctors = 0;
        C::move_ctors = 0;
        v.reserve(64);
        CHECK(C::move_ctors == 2);
        CHECK(C::copy_ctors == 0);
    }
    {  // throwing move: relocation must fall back to copies
        using C = Counted<false>;
        small_vector<C, 2> v;
        for (int i = 0; i < 2; ++i) v.push_back(C(i));
        C::copy_ctors = 0;
        C::move_ctors = 0;
        v.reserve(64);
        CHECK(C::copy_ctors == 2);
        CHECK(C::move_ctors == 0);
    }
}

static void test_copy_and_assign() {
    using C = Counted<false>;
    small_vector<C, 4> a;
    for (int i = 0; i < 6; ++i) a.push_back(C(i));
    small_vector<C, 4> b(a);
    CHECK(ids_of(b) == ids_of(a));
    small_vector<C, 4> c;
    c = a;
    CHECK(ids_of(c) == ids_of(a));

    const long live_before = C::live;
    C::fail_at = 2;
    bool threw = false;
    try {
        small_vector<C, 4> d(a);
        (void)d;
    } catch (const InjectError&) {
        threw = true;
    }
    C::fail_at = -1;
    CHECK(threw);
    CHECK(C::live == live_before);  // no leak from the failed copy
    CHECK(a.size() == 6);

    small_vector<C, 4> e(std::move(b));
    CHECK(e.size() == 6);
    CHECK(b.size() == 0);
    CHECK_IDS(e, {0, 1, 2, 3, 4, 5});
}

int main() {
    test_basic();
    test_insert_positions();
    test_insert_exception_safety<false, false>();
    test_insert_exception_safety<false, true>();
    test_insert_exception_safety<true, false>();
    test_insert_exception_safety<true, true>();
    test_self_reference();
    test_move_only_when_nothrow();
    test_copy_and_assign();

    CHECK(Counted<false>::live == 0);
    CHECK(Counted<true>::live == 0);
    CHECK(double_destructions == 0);

    if (failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", failures);
    return 1;
}
