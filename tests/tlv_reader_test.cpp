#include "tlv_reader.h"

#include <algorithm>
#include <cassert>
#include <cstdint>
#include <iostream>
#include <vector>

namespace {

using tlv::Error;
using tlv::Reader;

void appendBe16(std::vector<uint8_t>& out, uint16_t value) {
    out.push_back(static_cast<uint8_t>(value >> 8));
    out.push_back(static_cast<uint8_t>(value));
}

void appendBe32(std::vector<uint8_t>& out, uint32_t value) {
    out.push_back(static_cast<uint8_t>(value >> 24));
    out.push_back(static_cast<uint8_t>(value >> 16));
    out.push_back(static_cast<uint8_t>(value >> 8));
    out.push_back(static_cast<uint8_t>(value));
}

std::vector<uint8_t> leaf(uint16_t type,
                          const std::vector<uint8_t>& value = {}) {
    std::vector<uint8_t> out;
    appendBe16(out, type);
    appendBe32(out, static_cast<uint32_t>(value.size()));
    out.insert(out.end(), value.begin(), value.end());
    return out;
}

std::vector<uint8_t> nested(uint16_t base_type,
                            const std::vector<uint8_t>& body) {
    std::vector<uint8_t> out;
    appendBe16(out, static_cast<uint16_t>(0x8000u | base_type));
    appendBe32(out, static_cast<uint32_t>(body.size()));
    out.insert(out.end(), body.begin(), body.end());
    return out;
}

std::vector<uint8_t> leafWithRawLength(uint16_t type, uint32_t raw_length,
                                       const std::vector<uint8_t>& value) {
    std::vector<uint8_t> out;
    appendBe16(out, type);
    appendBe32(out, raw_length);
    out.insert(out.end(), value.begin(), value.end());
    return out;
}

void appendVec(std::vector<uint8_t>& dst, std::vector<uint8_t> src) {
    dst.insert(dst.end(), src.begin(), src.end());
}

struct Event {
    uint16_t type;
    std::vector<uint8_t> value;
};

struct Snapshot {
    std::vector<Event> events;
    size_t committed = 0;
    size_t charged = 0;
    Error error = Error::None;
    bool failed = false;
};

Reader makeReader(Snapshot& snapshot, size_t budget = SIZE_MAX) {
    return Reader(
        [&snapshot](uint16_t type, const uint8_t* value, size_t length) {
            Event event;
            event.type = type;
            event.value.assign(value, value + length);
            snapshot.events.push_back(std::move(event));
        },
        budget);
}

Snapshot runOneShot(const std::vector<uint8_t>& input, size_t budget) {
    Snapshot snapshot;
    Reader reader = makeReader(snapshot, budget);
    reader.feed(input);
    snapshot.committed = reader.committed();
    snapshot.charged = reader.charged();
    snapshot.error = reader.error();
    snapshot.failed = reader.failed();
    return snapshot;
}

void expectSameAsOneShot(const std::vector<uint8_t>& input, size_t budget,
                         const char* name) {
    Snapshot reference = runOneShot(input, budget);

    for (size_t split = 1; split <= input.size(); ++split) {
        Snapshot snapshot;
        Reader reader = makeReader(snapshot, budget);
        bool terminal_seen = false;
        for (size_t offset = 0; offset < input.size();) {
            size_t chunk = std::min(split, input.size() - offset);
            bool ok = reader.feed(input.data() + offset, chunk);
            offset += chunk;
            if (!ok) {
                terminal_seen = true;
                // All remaining feeds must stay inert.
                while (offset < input.size()) {
                    assert(!reader.feed(input.data() + offset, 1));
                    ++offset;
                }
            }
        }
        assert(reader.failed() == reference.failed);
        assert(reader.error() == reference.error);
        assert(reader.committed() == reference.committed);
        assert(reader.charged() == reference.charged);
        assert(terminal_seen == reference.failed);
        assert(snapshot.events.size() == reference.events.size());
        for (size_t i = 0; i < reference.events.size(); ++i) {
            assert(snapshot.events[i].type == reference.events[i].type);
            assert(snapshot.events[i].value == reference.events[i].value);
        }
    }
    std::cout << "  split-invariant ok: " << name << " (" << input.size()
              << " bytes, " << input.size() << " cuts)\n";
}

std::vector<uint8_t> depthChain(int depth, uint16_t leaf_type) {
    // The returned chain has `depth` record frames in total (the root is
    // frame 1 and the deepest is frame `depth`) plus one terminal leaf, so
    // the starting leaf is wrapped exactly `depth - 1` times.
    std::vector<uint8_t> body = leaf(leaf_type, {0xAA, 0xBB});
    for (int level = depth - 1; level >= 1; --level) {
        body = nested(static_cast<uint16_t>(level), body);
    }
    return body;
}

int g_checks = 0;

#define CHECK(condition)                                                     \
    do {                                                                     \
        ++g_checks;                                                          \
        if (!(condition)) {                                                  \
            std::cerr << "CHECK failed: " #condition " (" << __FILE__ << ":" \
                      << __LINE__ << ")\n";                                  \
            return false;                                                    \
        }                                                                    \
    } while (0)

bool testLeafAndPreorder() {
    std::vector<uint8_t> child_body;
    appendVec(child_body, leaf(0x0010, {0x11}));
    appendVec(child_body, leaf(0x0011, {0x22, 0x33}));
    std::vector<uint8_t> input;
    appendVec(input, nested(1, child_body));
    appendVec(input, leaf(0x0002, {0x44}));

    Snapshot snapshot;
    Reader reader = makeReader(snapshot);
    CHECK(reader.feed(input));
    CHECK(snapshot.events.size() == 4);
    CHECK(snapshot.events[0].type == 0x8001); // parent first (pre-order)
    CHECK(snapshot.events[1].type == 0x0010);
    CHECK(snapshot.events[2].type == 0x0011);
    CHECK(snapshot.events[3].type == 0x0002);
    CHECK(snapshot.events[0].value == child_body);
    CHECK(reader.committed() == input.size());
    expectSameAsOneShot(input, SIZE_MAX, "preorder");
    return true;
}

bool testSplitHeaderAndValue() {
    std::vector<uint8_t> input = leaf(0x0003, {1, 2, 3, 4});
    Snapshot snapshot;
    Reader reader = makeReader(snapshot);
    for (uint8_t byte : input) {
        CHECK(reader.feed(&byte, 1));
    }
    CHECK(snapshot.events.size() == 1);
    CHECK(snapshot.events[0].value == std::vector<uint8_t>({1, 2, 3, 4}));
    CHECK(reader.committed() == input.size());
    expectSameAsOneShot(input, SIZE_MAX, "byte-split");
    return true;
}

bool testNegativeLengths() {
    for (uint32_t raw : {0xFFFFFFFFu /* -1 */, 0x80000000u /* INT32_MIN */}) {
        std::vector<uint8_t> bad_root = leafWithRawLength(0x0001, raw, {0x00});
        Snapshot root_snapshot;
        Reader root_reader = makeReader(root_snapshot);
        CHECK(!root_reader.feed(bad_root));
        CHECK(root_reader.error() == Error::NegativeLength);
        CHECK(root_reader.committed() == 0);
        CHECK(root_snapshot.events.empty());

        // Terminal state: later feeds never callback.
        std::vector<uint8_t> good = leaf(0x0002, {9});
        CHECK(!root_reader.feed(good));
        CHECK(root_snapshot.events.empty());
        expectSameAsOneShot(bad_root, SIZE_MAX, "negative root");

        // Same raw length inside a parent: zero callbacks for the subtree.
        std::vector<uint8_t> bad_child = leafWithRawLength(0x0003, raw, {});
        std::vector<uint8_t> parent = nested(1, bad_child);
        Snapshot child_snapshot;
        Reader child_reader = makeReader(child_snapshot);
        CHECK(!child_reader.feed(parent));
        CHECK(child_reader.error() == Error::NegativeLength);
        CHECK(child_reader.committed() == 0);
        CHECK(child_snapshot.events.empty());
        expectSameAsOneShot(parent, SIZE_MAX, "negative child");
    }
    return true;
}

bool testRootExactlyRemainingAndOneExtra() {
    std::vector<uint8_t> record = leaf(0x0001, {1, 2, 3});

    Snapshot exact;
    Reader exact_reader = makeReader(exact, record.size());
    CHECK(exact_reader.feed(record));
    CHECK(exact_reader.error() == Error::None);
    CHECK(exact_reader.committed() == record.size());
    CHECK(exact.events.size() == 1);

    // Budget one byte short of the record.
    Snapshot short_budget;
    Reader short_reader = makeReader(short_budget, record.size() - 1);
    CHECK(!short_reader.feed(record));
    CHECK(short_reader.error() == Error::QuotaExceeded);
    CHECK(short_reader.committed() == 0);
    CHECK(short_budget.events.empty());

    // One extra trailing byte is a pending next record, not an error.
    std::vector<uint8_t> with_extra = record;
    with_extra.push_back(0xAB);
    Snapshot trailing;
    Reader trailing_reader = makeReader(trailing);
    CHECK(trailing_reader.feed(with_extra));
    CHECK(trailing_reader.committed() == record.size());
    CHECK(trailing.events.size() == 1);
    expectSameAsOneShot(with_extra, SIZE_MAX, "trailing byte");
    return true;
}

bool testChildOverrun() {
    // Child claims ten value bytes but the parent only supplies two.
    std::vector<uint8_t> lying_child =
        leafWithRawLength(0x0010, 10, {0x01, 0x02});
    std::vector<uint8_t> parent = nested(1, lying_child);

    Snapshot snapshot;
    Reader reader = makeReader(snapshot);
    CHECK(!reader.feed(parent));
    CHECK(reader.error() == Error::ChildOverrun);
    CHECK(reader.committed() == 0);
    CHECK(snapshot.events.empty());
    expectSameAsOneShot(parent, SIZE_MAX, "child overrun");
    return true;
}

bool testChildTruncated() {
    // Parent value is exactly one byte short of a complete child header.
    std::vector<uint8_t> child = leaf(0x0010, {0x01, 0x02}); // 8 bytes
    std::vector<uint8_t> parent;
    appendBe16(parent, 0x8001);
    const size_t declared_value = child.size() - 1; // only 7 bytes supplied
    appendBe32(parent, static_cast<uint32_t>(declared_value));
    parent.insert(parent.end(), child.begin(),
                  child.begin() + declared_value);

    Snapshot snapshot;
    Reader reader = makeReader(snapshot);
    CHECK(!reader.feed(parent));
    // The child's declared record end runs past the parent's value region.
    CHECK(reader.error() == Error::ChildOverrun);
    CHECK(reader.committed() == 0);
    CHECK(snapshot.events.empty());
    expectSameAsOneShot(parent, SIZE_MAX, "child truncated");
    return true;
}

bool testChildLeftover() {
    // Parent declares a 7-byte value: one complete 6-byte empty child leaves
    // exactly one unparseable byte, so the value is not exactly filled.
    std::vector<uint8_t> body = leaf(0x0010, {});
    body.push_back(0x7F);
    assert(body.size() == 7);
    std::vector<uint8_t> parent = nested(1, body);

    Snapshot snapshot;
    Reader reader = makeReader(snapshot);
    CHECK(!reader.feed(parent));
    CHECK((reader.error() == Error::ChildTruncated ||
           reader.error() == Error::ChildLeftover));
    CHECK(reader.committed() == 0);
    expectSameAsOneShot(parent, SIZE_MAX, "child leftover");
    return true;
}

bool testDepthLimits() {
    std::vector<uint8_t> ok_chain = depthChain(8, 0x0020);
    Snapshot ok_snapshot;
    Reader ok_reader = makeReader(ok_snapshot);
    CHECK(ok_reader.feed(ok_chain));
    CHECK(ok_reader.error() == Error::None);
    CHECK(ok_reader.committed() == ok_chain.size());
    CHECK(ok_snapshot.events.size() == 8); // 7 nested frames + final leaf
    expectSameAsOneShot(ok_chain, SIZE_MAX, "depth 8");

    std::vector<uint8_t> deep_chain = depthChain(9, 0x0020);
    Snapshot deep_snapshot;
    Reader deep_reader = makeReader(deep_snapshot);
    CHECK(!deep_reader.feed(deep_chain));
    CHECK(deep_reader.error() == Error::DepthExceeded);
    CHECK(deep_reader.committed() == 0);
    CHECK(deep_snapshot.events.empty());
    expectSameAsOneShot(deep_chain, SIZE_MAX, "depth 9");
    return true;
}

bool testSizeMaxBudget() {
    // Repeated records accumulate without overflow under a SIZE_MAX budget.
    std::vector<uint8_t> input;
    for (int i = 0; i < 1000; ++i) {
        appendVec(input, leaf(0x0001, {1, 2, 3, 4, 5}));
    }
    Snapshot snapshot;
    Reader reader = makeReader(snapshot, SIZE_MAX);
    CHECK(reader.feed(input));
    CHECK(reader.error() == Error::None);
    CHECK(reader.charged() == input.size());
    CHECK(reader.committed() == input.size());
    CHECK(snapshot.events.size() == 1000);
    expectSameAsOneShot(input, SIZE_MAX, "SIZE_MAX accumulation");

    // A deliberately tight budget rejects without wrapping counters.
    Snapshot tight_snapshot;
    Reader tight_reader = makeReader(tight_snapshot, input.size() - 1);
    CHECK(!tight_reader.feed(input));
    CHECK(tight_reader.error() == Error::QuotaExceeded);
    size_t record_size = leaf(0x0001, {1, 2, 3, 4, 5}).size();
    CHECK(tight_reader.committed() ==
          ((input.size() - 1) / record_size) * record_size);
    CHECK(tight_reader.committed() < input.size());
    return true;
}

bool testQuotaAcrossFragments() {
    std::vector<uint8_t> first = leaf(0x0001, {1}); // 7 bytes
    std::vector<uint8_t> second = leaf(0x0002, {2}); // 7 bytes
    std::vector<uint8_t> input = first;
    input.insert(input.end(), second.begin(), second.end());

    Snapshot snapshot;
    Reader reader = makeReader(snapshot, input.size());
    CHECK(reader.feed(first));
    CHECK(reader.committed() == first.size());
    CHECK(reader.charged() == first.size());
    CHECK(snapshot.events.size() == 1);
    CHECK(reader.feed(second));
    CHECK(reader.committed() == input.size());
    CHECK(reader.charged() == input.size());
    CHECK(snapshot.events.size() == 2);

    // Exhausting the budget between records freezes committed at record start.
    Snapshot exhausted;
    Reader exhausted_reader = makeReader(exhausted, first.size());
    CHECK(exhausted_reader.feed(first));
    CHECK(!exhausted_reader.feed(second));
    CHECK(exhausted_reader.error() == Error::QuotaExceeded);
    CHECK(exhausted_reader.committed() == first.size());
    CHECK(exhausted.events.size() == 1);
    return true;
}

bool testQuotaChargesNestedHeaders() {
    // A parent's header and value (including every child header) must all be
    // charged against the cumulative budget, not just leaf payload bytes.
    std::vector<uint8_t> body;
    appendVec(body, leaf(0x0010, {0x01, 0x02}));
    appendVec(body, leaf(0x0011, {0x03}));
    std::vector<uint8_t> record = nested(1, body);

    Snapshot exact;
    Reader exact_reader = makeReader(exact, record.size());
    CHECK(exact_reader.feed(record));
    CHECK(exact_reader.charged() == record.size());
    CHECK(exact_reader.committed() == record.size());

    // Budget stops one byte inside the record: no commit and no callback.
    Snapshot tight;
    Reader tight_reader = makeReader(tight, record.size() - 1);
    CHECK(!tight_reader.feed(record));
    CHECK(tight_reader.error() == Error::QuotaExceeded);
    CHECK(tight_reader.committed() == 0);
    CHECK(tight.events.empty());

    // Header-only budget rejects a larger record even before its value ends.
    Snapshot header_only;
    Reader header_reader = makeReader(header_only, Reader::kHeaderSize - 1);
    CHECK(!header_reader.feed(record));
    CHECK(header_reader.error() == Error::QuotaExceeded);
    CHECK(header_only.events.empty());
    expectSameAsOneShot(record, record.size(), "nested quota exact");
    return true;
}

bool testEmptyNestedAndEmptyLeaf() {
    std::vector<uint8_t> empty_parent = nested(1, {});
    std::vector<uint8_t> input;
    appendVec(input, empty_parent);
    appendVec(input, leaf(0x0002, {}));

    Snapshot snapshot;
    Reader reader = makeReader(snapshot);
    CHECK(reader.feed(input));
    CHECK(snapshot.events.size() == 2);
    CHECK(snapshot.events[0].type == 0x8001);
    CHECK(snapshot.events[0].value.empty());
    CHECK(snapshot.events[1].type == 0x0002);
    CHECK(snapshot.events[1].value.empty());
    CHECK(reader.committed() == input.size());
    expectSameAsOneShot(input, SIZE_MAX, "empty records");
    return true;
}

bool testIncompleteAcrossFeeds() {
    std::vector<uint8_t> input = leaf(0x0004, {1, 2, 3});
    Snapshot snapshot;
    Reader reader = makeReader(snapshot);

    // Header alone, then one value byte at a time: no premature callback.
    CHECK(reader.feed(input.data(), Reader::kHeaderSize));
    CHECK(snapshot.events.empty());
    CHECK(reader.committed() == 0);
    for (size_t i = Reader::kHeaderSize; i < input.size(); ++i) {
        bool is_last = (i + 1 == input.size());
        CHECK(reader.feed(input.data() + i, 1));
        CHECK(snapshot.events.size() == (is_last ? 1u : 0u));
    }
    CHECK(snapshot.events.size() == 1);
    CHECK(reader.committed() == input.size());
    return true;
}

bool testReset() {
    Snapshot snapshot;
    Reader reader = makeReader(snapshot, 10);
    std::vector<uint8_t> too_big = leaf(0x0001, {1, 2, 3, 4, 5});
    CHECK(!reader.feed(too_big));
    CHECK(reader.failed());

    reader.reset(64);
    CHECK(!reader.failed());
    CHECK(reader.committed() == 0);
    CHECK(reader.charged() == 0);
    CHECK(reader.error() == Error::None);
    std::vector<uint8_t> good = leaf(0x0002, {7});
    CHECK(reader.feed(good));
    CHECK(snapshot.events.size() == 1);
    return true;
}

bool testMixedStreamEveryCut() {
    // One rich stream covering nesting, siblings, empty values and malformed
    // tail; exercised with every possible fixed-width fragment size.
    std::vector<uint8_t> valid_body;
    appendVec(valid_body, leaf(0x0010, {0x01}));
    appendVec(valid_body, nested(2, leaf(0x0011, {})));
    std::vector<uint8_t> input;
    appendVec(input, nested(1, valid_body));
    appendVec(input, leaf(0x0003, {9, 8}));
    size_t good_bytes = input.size();
    appendVec(input, leafWithRawLength(0x0004, 0x80000000u, {}));

    Snapshot reference = runOneShot(input, SIZE_MAX);
    CHECK(reference.failed);
    CHECK(reference.error == Error::NegativeLength);
    CHECK(reference.committed == good_bytes);
    CHECK(reference.events.size() == 5);
    expectSameAsOneShot(input, SIZE_MAX, "mixed stream");
    return true;
}

using TestFn = bool (*)();

} // namespace

int main() {
    const TestFn tests[] = {
        testLeafAndPreorder,      testSplitHeaderAndValue,
        testNegativeLengths,      testRootExactlyRemainingAndOneExtra,
        testChildOverrun,         testChildTruncated,
        testChildLeftover,        testDepthLimits,
        testSizeMaxBudget,        testQuotaAcrossFragments,
        testQuotaChargesNestedHeaders,
        testEmptyNestedAndEmptyLeaf, testIncompleteAcrossFeeds,
        testReset,                testMixedStreamEveryCut,
    };

    int failures = 0;
    for (TestFn test : tests) {
        g_checks = 0;
        std::cout << "[ RUN  ] " << g_checks << "\n";
        if (!test()) {
            ++failures;
            std::cout << "[ FAIL ]\n";
        } else {
            std::cout << "[ PASS ]\n";
        }
    }
    if (failures != 0) {
        std::cerr << failures << " test group(s) failed\n";
        return 1;
    }
    std::cout << "All TLV reader tests passed\n";
    return 0;
}
