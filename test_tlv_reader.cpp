#include "netsec.hpp"

#include <cstddef>
#include <cstdint>
#include <cstdio>
#include <initializer_list>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;

int failures = 0;

#define CHECK(cond)                                                        \
    do {                                                                   \
        if (!(cond)) {                                                     \
            std::printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);    \
            ++failures;                                                    \
        }                                                                  \
    } while (0)

// Record with an explicitly supplied (possibly negative-as-unsigned) length.
Bytes raw_rec(std::uint16_t type, std::uint32_t len, const Bytes& val) {
    Bytes b;
    b.push_back(static_cast<std::uint8_t>(type >> 8));
    b.push_back(static_cast<std::uint8_t>(type));
    b.push_back(static_cast<std::uint8_t>(len >> 24));
    b.push_back(static_cast<std::uint8_t>(len >> 16));
    b.push_back(static_cast<std::uint8_t>(len >> 8));
    b.push_back(static_cast<std::uint8_t>(len));
    b.insert(b.end(), val.begin(), val.end());
    return b;
}

Bytes rec(std::uint16_t type, const Bytes& val) {
    return raw_rec(type, static_cast<std::uint32_t>(val.size()), val);
}

Bytes cat(std::initializer_list<Bytes> parts) {
    Bytes out;
    for (const auto& p : parts) out.insert(out.end(), p.begin(), p.end());
    return out;
}

struct Entry {
    std::uint16_t type;
    std::uint32_t len;
    unsigned depth;
    bool operator==(const Entry& o) const {
        return type == o.type && len == o.len && depth == o.depth;
    }
};

struct Sink {
    std::vector<Entry> log;
    netsec::tlv_reader::Callback cb() {
        return [this](std::uint16_t t, const std::uint8_t*, std::uint32_t n,
                      unsigned d) { log.push_back({t, n, d}); };
    }
};

void check_log(const Sink& s, std::initializer_list<Entry> want) {
    CHECK(s.log == std::vector<Entry>(want));
}

// Nested chain: `depth` nested wrappers (type 0x8001) around an empty leaf.
Bytes chain(unsigned depth, std::uint16_t leaf_type) {
    Bytes b = rec(leaf_type, {});
    for (unsigned i = 0; i < depth; ++i) b = rec(0x8001, b);
    return b;
}

void test_negative_lengths() {
    // -1, INT32_MIN, and other negative signed lengths.
    for (std::uint32_t raw : {0xFFFFFFFFu, 0x80000000u, 0x80000001u, 0xFFFFFFFEu}) {
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = raw_rec(0x0001, raw, {});
        r.feed(stream.data(), stream.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
        // Terminal state is sticky: later valid input is ignored.
        Bytes good = rec(0x0002, {1});
        r.feed(good.data(), good.size());
        CHECK(s.log.empty());
        CHECK(r.committed() == 0);
    }
}

void test_exact_fit() {
    Sink s;
    netsec::tlv_reader r(SIZE_MAX, s.cb());
    Bytes stream = rec(0x0001, {1, 2, 3});
    r.feed(stream.data(), stream.size());
    CHECK(!r.terminated());
    CHECK(r.committed() == stream.size());
    check_log(s, {{0x0001, 3, 0}});
}

void test_one_byte_more_than_available() {
    Sink s;
    netsec::tlv_reader r(SIZE_MAX, s.cb());
    Bytes stream = rec(0x0001, {1, 2, 3, 4});  // 10 bytes total
    r.feed(stream.data(), stream.size() - 1);  // one byte short: just waits
    CHECK(!r.terminated());
    CHECK(r.committed() == 0);
    CHECK(s.log.empty());
    r.feed(stream.data() + stream.size() - 1, 1);
    CHECK(!r.terminated());
    CHECK(r.committed() == stream.size());
    check_log(s, {{0x0001, 4, 0}});
}

void test_budget() {
    const Bytes stream = rec(0x0001, {1, 2, 3});  // 9 bytes

    {  // Budget exactly equal to the record size: accepted.
        Sink s;
        netsec::tlv_reader r(stream.size(), s.cb());
        r.feed(stream.data(), stream.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == stream.size());
        check_log(s, {{0x0001, 3, 0}});
    }
    {  // Budget one byte short: rejected as soon as the header arrives.
        Sink s;
        netsec::tlv_reader r(stream.size() - 1, s.cb());
        r.feed(stream.data(), netsec::tlv_reader::kHeaderSize);
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
    }
    {  // Cumulative budget across records: two fit, the third does not.
        Sink s;
        netsec::tlv_reader r(2 * stream.size(), s.cb());
        Bytes three = cat({stream, stream, stream});
        r.feed(three.data(), three.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 2 * stream.size());
        check_log(s, {{0x0001, 3, 0}, {0x0001, 3, 0}});
    }
}

void test_nested() {
    {  // Valid nesting: pre-order callbacks with depths.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = rec(0x8001, cat({rec(0x0002, {1, 2}), rec(0x0003, {})}));
        r.feed(stream.data(), stream.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == stream.size());
        check_log(s, {{0x8001, 14, 0}, {0x0002, 2, 1}, {0x0003, 0, 1}});
    }
    {  // Child one byte short of filling the parent value.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes inner = cat({raw_rec(0x0002, 3, {}), Bytes{0xAA, 0xBB}});
        Bytes stream = rec(0x8001, inner);  // child needs 9 bytes, has 8
        r.feed(stream.data(), stream.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
    }
    {  // Child length larger than the parent's remaining value.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = rec(0x8001, raw_rec(0x0002, 100, {}));
        r.feed(stream.data(), stream.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
    }
    {  // Trailing byte after the last child: value not exactly tiled.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = rec(0x8001, cat({rec(0x0002, {1}), Bytes{0xFF}}));
        r.feed(stream.data(), stream.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
    }
    {  // Negative length on a nested child.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = rec(0x8001, raw_rec(0x0002, 0xFFFFFFFFu, {}));
        r.feed(stream.data(), stream.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
    }
    {  // Empty nested value (zero children) is valid.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = rec(0x8001, {});
        r.feed(stream.data(), stream.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == stream.size());
        check_log(s, {{0x8001, 0, 0}});
    }
}

void test_depth() {
    {  // Deepest record at depth 8: accepted (9 records, depths 0..8).
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = chain(8, 0x0001);
        r.feed(stream.data(), stream.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == stream.size());
        CHECK(s.log.size() == 9);
        for (unsigned d = 0; d < 9; ++d) CHECK(s.log[d].depth == d);
    }
    {  // Deepest record at depth 9: rejected, zero callbacks.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = chain(9, 0x0001);
        r.feed(stream.data(), stream.size());
        CHECK(r.terminated());
        CHECK(r.committed() == 0);
        CHECK(s.log.empty());
    }
    {  // Nested-with-empty-value at depth 8 is fine (no children at depth 9).
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes stream = chain(8, 0x8001);
        r.feed(stream.data(), stream.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == stream.size());
        CHECK(s.log.size() == 9);
    }
}

void test_size_max_budget() {
    {  // Accumulation over many records with budget == SIZE_MAX.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        const Bytes one = rec(0x0007, Bytes(100, 0xAB));  // 106 bytes
        const std::size_t count = 1000;
        Bytes stream;
        for (std::size_t i = 0; i < count; ++i) {
            stream.insert(stream.end(), one.begin(), one.end());
        }
        r.feed(stream.data(), stream.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == count * one.size());
        CHECK(s.log.size() == count);
    }
    {  // Huge declared length must not wrap the running total: with
        // budget == SIZE_MAX the reader simply waits for the data.
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        Bytes good = rec(0x0001, {1, 2, 3});
        r.feed(good.data(), good.size());
        CHECK(r.committed() == good.size());
        Bytes huge = raw_rec(0x0002, 0x7FFFFFFFu, {});  // INT32_MAX length
        r.feed(huge.data(), huge.size());
        CHECK(!r.terminated());
        CHECK(r.committed() == good.size());
        CHECK(s.log.size() == 1);
    }
}

// Feeds `stream` split at every byte boundary (plus byte-by-byte) and checks
// that every split yields the reference log / committed / terminated state.
void check_all_splits(const Bytes& stream, const std::vector<Entry>& ref_log,
                      std::size_t ref_committed, bool ref_terminated) {
    for (std::size_t cut = 0; cut <= stream.size(); ++cut) {
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        r.feed(stream.data(), cut);
        r.feed(stream.data() + cut, stream.size() - cut);
        CHECK(s.log == ref_log);
        CHECK(r.committed() == ref_committed);
        CHECK(r.terminated() == ref_terminated);
    }
    {  // One byte per feed().
        Sink s;
        netsec::tlv_reader r(SIZE_MAX, s.cb());
        for (std::uint8_t b : stream) r.feed(&b, 1);
        CHECK(s.log == ref_log);
        CHECK(r.committed() == ref_committed);
        CHECK(r.terminated() == ref_terminated);
    }
}

void test_split_invariance() {
    {  // Valid stream: nested tree + empty plain + empty nested records.
        Bytes stream = cat({
            rec(0x8001, cat({rec(0x0002, {1, 2, 3}),
                             rec(0x8003, rec(0x0004, {9}))})),
            rec(0x0005, {}),
            rec(0x8006, {}),
        });
        Sink ref;
        netsec::tlv_reader rr(SIZE_MAX, ref.cb());
        rr.feed(stream.data(), stream.size());
        CHECK(!rr.terminated());
        CHECK(rr.committed() == stream.size());
        check_log(ref, {{0x8001, 22, 0},
                        {0x0002, 3, 1},
                        {0x8003, 7, 1},
                        {0x0004, 1, 2},
                        {0x0005, 0, 0},
                        {0x8006, 0, 0}});
        check_all_splits(stream, ref.log, rr.committed(), rr.terminated());
    }
    {  // Failing stream: first record commits, second has a bad child.
        Bytes stream = cat({
            rec(0x0009, {1}),
            rec(0x8001, raw_rec(0x0002, 50, {})),
        });
        Sink ref;
        netsec::tlv_reader rr(SIZE_MAX, ref.cb());
        rr.feed(stream.data(), stream.size());
        CHECK(rr.terminated());
        CHECK(rr.committed() == 7);  // stops at the bad record's start
        check_log(ref, {{0x0009, 1, 0}});
        check_all_splits(stream, ref.log, rr.committed(), rr.terminated());
    }
}

void test_atomic_top_level_commit() {
    // A valid record followed by a corrupt one: the valid record commits,
    // the corrupt one produces zero callbacks and freezes committed().
    Sink s;
    netsec::tlv_reader r(SIZE_MAX, s.cb());
    Bytes good = rec(0x0001, {1, 2, 3});
    Bytes bad = raw_rec(0x0002, 0x80000000u, {});
    Bytes stream = cat({good, bad});
    r.feed(stream.data(), stream.size());
    CHECK(r.terminated());
    CHECK(r.committed() == good.size());
    check_log(s, {{0x0001, 3, 0}});
    // Nothing further is ever delivered.
    r.feed(good.data(), good.size());
    CHECK(s.log.size() == 1);
    CHECK(r.committed() == good.size());
}

}  // namespace

int main() {
    test_negative_lengths();
    test_exact_fit();
    test_one_byte_more_than_available();
    test_budget();
    test_nested();
    test_depth();
    test_size_max_budget();
    test_split_invariance();
    test_atomic_top_level_commit();
    if (failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::printf("%d check(s) failed\n", failures);
    return 1;
}
