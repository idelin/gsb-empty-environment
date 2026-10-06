// Tests for small_vector: liveness-tracked elements with injection of a
// throw at a chosen construction ordinal.
//
// Build: clang++ -std=c++17 -Wall -Wextra -Wpedantic -fsanitize=address,undefined -g test_small_vector.cpp -o test_small_vector

#include "small_vector.hpp"

#include <cstdio>
#include <stdexcept>

static int failures = 0;
#define CHECK(cond)                                                     \
    do {                                                                \
        if (!(cond)) {                                                  \
            ++failures;                                                 \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
        }                                                               \
    } while (0)

struct injected_throw : std::runtime_error {
    injected_throw() : std::runtime_error("injected construction failure") {}
};

// Element with a global liveness count and a "throw at the k-th
// construction" fault-injection point. Only constructors throw (assignments
// never do). NoexceptMove selects whether the move constructor/assignment
// are noexcept, which controls std::move_if_noexcept behavior.
template <bool NoexceptMove>
struct Counted {
    static inline long live = 0;
    static inline long total_constructs = 0;
    static inline long copy_constructs = 0;
    static inline long move_constructs = 0;
    static inline long double_destructs = 0;
    static inline long throw_at = -1;  // 1-based construction ordinal that throws

    int id;
    bool alive;

    static void reset_all() {
        live = 0;
        reset_counters();
    }
    static void reset_counters() {
        total_constructs = copy_constructs = move_constructs = 0;
        double_destructs = 0;
        throw_at = -1;
    }
    static void tick() {
        ++total_constructs;
        if (throw_at > 0 && total_constructs == throw_at) throw injected_throw();
    }

    explicit Counted(int i = 0) : id(i), alive(true) { tick(); ++live; }
    Counted(const Counted& o) : id(o.id), alive(true) { tick(); ++copy_constructs; ++live; }
    Counted(Counted&& o) noexcept(NoexceptMove) : id(o.id), alive(true) {
        tick();
        ++move_constructs;
        ++live;
        o.id = -1;
    }
    Counted& operator=(const Counted& o) {
        id = o.id;
        return *this;
    }
    Counted& operator=(Counted&& o) noexcept(NoexceptMove) {
        id = o.id;
        o.id = -1;
        return *this;
    }
    ~Counted() {
        if (!alive) {
            ++double_destructs;  // destructor ran twice on the same object
            return;
        }
        alive = false;
        --live;
    }
};

// ---------------------------------------------------------------------------
// Basic growth: inline -> heap migration, capacity progression, contents.
// ---------------------------------------------------------------------------
template <bool NM>
void test_basic_growth() {
    using C = Counted<NM>;
    C::reset_all();
    {
        small_vector<C, 4> v;
        CHECK(v.size() == 0 && v.capacity() == 4);
        for (int i = 0; i < 4; ++i) v.push_back(C(i));
        CHECK(v.size() == 4 && v.capacity() == 4);  // still inline
        v.push_back(C(4));                          // migrate to heap
        CHECK(v.size() == 5 && v.capacity() == 8);
        for (int i = 0; i < 5; ++i) CHECK(v[i].id == i);
        v.push_back(C(5));
        v.push_back(C(6));
        v.push_back(C(7));
        CHECK(v.capacity() == 8);
        v.push_back(C(8));
        CHECK(v.size() == 9 && v.capacity() == 16);
        for (int i = 0; i < 9; ++i) CHECK(v[i].id == i);
        v.emplace_back(9);
        CHECK(v.size() == 10 && v[9].id == 9);
        v.emplace(v.begin() + 2, 1000);
        CHECK(v.size() == 11 && v[2].id == 1000 && v[3].id == 2);

        small_vector<C, 4> w;
        w.insert(w.end(), C(77));  // insert into empty vector
        CHECK(w.size() == 1 && w[0].id == 77);
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// Move is preferred only when noexcept: relocation during growth must use
// moves for NM=true and copies for NM=false.
// ---------------------------------------------------------------------------
template <bool NM>
void test_move_if_noexcept() {
    using C = Counted<NM>;
    C::reset_all();
    {
        small_vector<C, 4> v;
        for (int i = 0; i < 4; ++i) v.push_back(C(i));
        C value(99);
        C::reset_counters();
        v.push_back(value);  // triggers growth; value itself is copied
        if (NM) {
            CHECK(C::move_constructs == 4);  // relocation uses noexcept moves
            CHECK(C::copy_constructs == 1);  // only the appended element
        } else {
            CHECK(C::move_constructs == 0);  // throwing move is never used
            CHECK(C::copy_constructs == 5);  // relocation falls back to copies
        }
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// Reallocating insert: inject a throw at EVERY construction position and
// verify contents, capacity, liveness and destruction counts are untouched.
// Uses NM=false so every relocation is a (throwing-capable) copy.
// ---------------------------------------------------------------------------
void test_insert_throw_every_position() {
    using C = Counted<false>;
    C::reset_all();
    {
        small_vector<C, 4> v;
        for (int i = 0; i < 4; ++i) v.push_back(C(i));  // inline storage full
        C value(100);

        long total = 0;
        {  // measure how many constructions one reallocating insert performs
            small_vector<C, 4> probe;
            for (int i = 0; i < 4; ++i) probe.push_back(C(i));
            C::reset_counters();
            probe.insert(probe.begin() + 2, value);
            total = C::total_constructs;
        }
        CHECK(total == 5);  // 4 relocations + 1 inserted element

        for (long k = 1; k <= total; ++k) {
            C::reset_counters();
            C::throw_at = k;
            const long live_before = C::live;
            bool threw = false;
            try {
                v.insert(v.begin() + 2, value);
            } catch (const injected_throw&) {
                threw = true;
            }
            C::throw_at = -1;
            CHECK(threw);
            CHECK(v.size() == 4 && v.capacity() == 4);  // capacity preserved
            for (int i = 0; i < 4; ++i) CHECK(v[i].id == i);  // contents preserved
            CHECK(C::live == live_before);              // no leak
            CHECK(C::double_destructs == 0);            // no double destruction
        }

        // sanity: with injection disabled the insert succeeds
        v.insert(v.begin() + 2, value);
        CHECK(v.size() == 5 && v.capacity() == 8);
        CHECK(v[0].id == 0 && v[1].id == 1 && v[2].id == 100 && v[3].id == 2 && v[4].id == 3);
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// In-place insert (capacity sufficient): the single construction may throw.
// ---------------------------------------------------------------------------
void test_inplace_insert_throw() {
    using C = Counted<false>;
    C::reset_all();
    {
        small_vector<C, 4> v;
        for (int i = 0; i < 3; ++i) v.push_back(C(i));  // size 3 < capacity 4
        C value(100);

        C::reset_counters();
        C::throw_at = 1;  // the only construction: the new last element
        const long live_before = C::live;
        bool threw = false;
        try {
            v.insert(v.begin() + 1, value);
        } catch (const injected_throw&) {
            threw = true;
        }
        C::throw_at = -1;
        CHECK(threw);
        CHECK(v.size() == 3 && v.capacity() == 4);
        for (int i = 0; i < 3; ++i) CHECK(v[i].id == i);
        CHECK(C::live == live_before);
        CHECK(C::double_destructs == 0);

        v.insert(v.begin() + 1, value);  // now it succeeds
        CHECK(v.size() == 4);
        CHECK(v[0].id == 0 && v[1].id == 100 && v[2].id == 1 && v[3].id == 2);
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// push_back growth: inject a throw at every construction position.
// ---------------------------------------------------------------------------
void test_push_back_throw_every_position() {
    using C = Counted<false>;
    C::reset_all();
    {
        small_vector<C, 4> v;
        for (int i = 0; i < 4; ++i) v.push_back(C(i));
        C value(100);

        long total = 0;
        {
            small_vector<C, 4> probe;
            for (int i = 0; i < 4; ++i) probe.push_back(C(i));
            C::reset_counters();
            probe.push_back(value);
            total = C::total_constructs;
        }
        CHECK(total == 5);  // 4 relocations + 1 appended element

        for (long k = 1; k <= total; ++k) {
            C::reset_counters();
            C::throw_at = k;
            const long live_before = C::live;
            bool threw = false;
            try {
                v.push_back(value);
            } catch (const injected_throw&) {
                threw = true;
            }
            C::throw_at = -1;
            CHECK(threw);
            CHECK(v.size() == 4 && v.capacity() == 4);
            for (int i = 0; i < 4; ++i) CHECK(v[i].id == i);
            CHECK(C::live == live_before);
            CHECK(C::double_destructs == 0);
        }
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// Copy constructor: inject a throw at every construction position.
// ---------------------------------------------------------------------------
void test_copy_ctor_throw_every_position() {
    using C = Counted<false>;
    C::reset_all();
    {
        small_vector<C, 4> a;
        for (int i = 0; i < 6; ++i) a.push_back(C(i));  // heap-allocated
        for (long k = 1; k <= 6; ++k) {
            C::reset_counters();
            C::throw_at = k;
            const long live_before = C::live;
            bool threw = false;
            try {
                small_vector<C, 4> b(a);
                (void)b;
            } catch (const injected_throw&) {
                threw = true;
            }
            C::throw_at = -1;
            CHECK(threw);
            CHECK(C::live == live_before);
            CHECK(C::double_destructs == 0);
            CHECK(a.size() == 6 && a.capacity() == 8);
            for (int i = 0; i < 6; ++i) CHECK(a[i].id == i);
        }
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// Self-referential insertion, with and without reallocation.
// ---------------------------------------------------------------------------
template <bool NM>
void test_self_insert() {
    using C = Counted<NM>;
    C::reset_all();
    {
        small_vector<C, 4> v;
        for (int i = 0; i < 4; ++i) v.push_back(C(i));  // 0 1 2 3, inline full

        v.insert(v.begin() + 1, v[0]);  // reallocating + aliased
        CHECK(v.size() == 5 && v.capacity() == 8);
        CHECK(v[0].id == 0 && v[1].id == 0 && v[2].id == 1 && v[3].id == 2 && v[4].id == 3);

        v.insert(v.begin(), v[4]);  // in-place + aliased
        CHECK(v.size() == 6);
        CHECK(v[0].id == 3 && v[1].id == 0 && v[2].id == 0 && v[3].id == 1 && v[4].id == 2 &&
              v[5].id == 3);

        v.push_back(v[0]);  // aliased push_back, capacity sufficient
        CHECK(v.size() == 7 && v[6].id == 3);

        v.push_back(C(7));  // size 8 == capacity 8
        v.push_back(v[1]);  // aliased push_back with growth
        CHECK(v.size() == 9 && v.capacity() == 16 && v[8].id == 0);

        v.insert(v.end(), v[0]);  // aliased insert at end
        CHECK(v.size() == 10 && v[9].id == 3);

        v.insert(v.begin() + 1, std::move(v[0]));  // aliased rvalue insert
        CHECK(v.size() == 11 && v[1].id == 3);
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

// ---------------------------------------------------------------------------
// Container copy/move sanity.
// ---------------------------------------------------------------------------
template <bool NM>
void test_copy_move_container() {
    using C = Counted<NM>;
    C::reset_all();
    {
        small_vector<C, 4> a;
        for (int i = 0; i < 6; ++i) a.push_back(C(i));  // heap
        small_vector<C, 4> b(a);
        CHECK(b.size() == 6 && b.capacity() >= 6);
        for (int i = 0; i < 6; ++i) CHECK(b[i].id == i);

        small_vector<C, 4> c;
        c = a;
        CHECK(c.size() == 6);
        for (int i = 0; i < 6; ++i) CHECK(c[i].id == i);

        small_vector<C, 4> d(std::move(b));  // heap steal
        CHECK(d.size() == 6 && b.size() == 0);
        for (int i = 0; i < 6; ++i) CHECK(d[i].id == i);

        small_vector<C, 4> e;
        for (int i = 0; i < 2; ++i) e.push_back(C(i));  // inline
        small_vector<C, 4> f(std::move(e));             // inline element-wise move
        CHECK(f.size() == 2 && f.capacity() == 4 && e.size() == 0);
        CHECK(f[0].id == 0 && f[1].id == 1);
    }
    CHECK(C::live == 0);
    CHECK(C::double_destructs == 0);
}

int main() {
    test_basic_growth<true>();
    test_basic_growth<false>();
    test_move_if_noexcept<true>();
    test_move_if_noexcept<false>();
    test_insert_throw_every_position();
    test_inplace_insert_throw();
    test_push_back_throw_every_position();
    test_copy_ctor_throw_every_position();
    test_self_insert<true>();
    test_self_insert<false>();
    test_copy_move_container<true>();
    test_copy_move_container<false>();

    if (failures == 0) {
        std::printf("ALL TESTS PASSED\n");
        return 0;
    }
    std::printf("%d CHECK(S) FAILED\n", failures);
    return 1;
}
