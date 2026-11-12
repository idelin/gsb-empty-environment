#include "netsec.hpp"

#include <algorithm>
#include <chrono>
#include <cstdio>
#include <cstdlib>
#include <random>
#include <vector>

namespace {

int g_checks = 0;
int g_failures = 0;

void record_check(bool condition, const char* expression, const char* file,
                  int line) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::fprintf(stderr, "FAIL %s:%d: %s\n", file, line, expression);
    }
}

}  // namespace

#define CHECK(cond) record_check((cond), #cond, __FILE__, __LINE__)

#define CHECK_EQ(a, b)                                                \
    do {                                                              \
        auto lhs_ = (a);                                              \
        auto rhs_ = (b);                                              \
        record_check(lhs_ == rhs_, #a " == " #b, __FILE__, __LINE__); \
    } while (0)

using namespace netsec;

namespace {

std::vector<std::uint8_t> make_content(std::size_t length,
                                       std::uint8_t seed) {
    std::vector<std::uint8_t> data(length);
    for (std::size_t i = 0; i < length; ++i) {
        data[i] = static_cast<std::uint8_t>((i + seed) % 256u);
    }
    // A trailing zero would be indistinguishable from padding and is removed
    // by the receiver per RFC 8446; round-trip tests therefore keep the last
    // content byte non-zero explicitly.
    if (length > 0u && data.back() == 0u) {
        data.back() = 1u;
    }
    return data;
}

void test_padded_length_math() {
    // Block quantization: content || type is rounded up to a 256 multiple.
    CHECK_EQ(padded_length(0), 256u);
    CHECK_EQ(padded_length(1), 256u);
    CHECK_EQ(padded_length(254), 256u);
    CHECK_EQ(padded_length(255), 256u);
    CHECK_EQ(padded_length(256), 512u);
    CHECK_EQ(padded_length(509), 512u);
    CHECK_EQ(padded_length(510), 512u);
    CHECK_EQ(padded_length(511), 512u);
    CHECK_EQ(padded_length(512), 768u);

    // Upper bucket boundaries.
    CHECK_EQ(padded_length(16127), 16128u);
    CHECK_EQ(padded_length(16128), 16384u);
    CHECK_EQ(padded_length(16382), 16384u);
    CHECK_EQ(padded_length(16383), 16384u);

    // 16384 content bytes + type = 16385, which cannot fit a 256 multiple
    // without exceeding the 2^14+1 inner-plaintext limit.
    CHECK_EQ(padded_length(16384), 0u);
    CHECK_EQ(padded_length(SIZE_MAX), 0u);

    // Every accepted size is a block multiple bounded by 16384.
    for (std::size_t len = 0; len <= 16383; ++len) {
        const std::size_t total = padded_length(len);
        CHECK(total != 0u);
        CHECK(total % kBlockSize == 0u);
        CHECK(total <= kMaxPaddedLength);
        CHECK(total >= len + 1u);
    }
}

void test_padding_layout_and_indistinguishability() {
    constexpr std::uint8_t kType = 23;  // application_data

    // All lengths sharing a 256-byte bucket produce identical record sizes,
    // so a passive observer cannot separate them by record length.
    for (std::size_t len = 0; len <= 255; ++len) {
        auto record = pad(make_content(len, 1).data(), len, kType);
        CHECK(record.has_value());
        CHECK_EQ(record->size(), 256u);
    }
    for (std::size_t len = 256; len <= 510; ++len) {
        auto record = pad(make_content(len, 1).data(), len, kType);
        CHECK(record.has_value());
        CHECK_EQ(record->size(), 512u);
    }

    // Structural checks at regular strides up to the maximum size.
    for (std::size_t len = 0; len <= kMaxContentLength; len += 97) {
        const auto content =
                make_content(len, static_cast<std::uint8_t>((len % 255) + 1));
        auto record = pad(content.data(), len, kType);
        CHECK(record.has_value());
        const std::size_t total = record->size();
        CHECK(total % kBlockSize == 0u);
        CHECK(total <= kMaxPaddedLength);
        CHECK(std::equal(content.begin(), content.end(), record->begin()));
        CHECK_EQ((*record)[len], kType);
        for (std::size_t j = len + 1u; j < total; ++j) {
            CHECK_EQ((*record)[j], 0u);
        }
    }

    // Exact-capacity output buffer succeeds, one byte short is rejected.
    const std::size_t len = 300u;
    const std::size_t total = padded_length(len);
    const auto content = make_content(len, 7);
    std::vector<std::uint8_t> out(total, 0xCC);
    std::size_t out_length = 0u;
    CHECK_EQ(pad(content.data(), len, kType, out.data(), total, &out_length),
             PadError::kOk);
    CHECK_EQ(out_length, total);
    std::vector<std::uint8_t> small(total - 1u, 0);
    CHECK_EQ(pad(content.data(), len, kType, small.data(), total - 1u,
                 &out_length),
             PadError::kOutputTooSmall);

    // In-place variant shares the send buffer layout.
    std::vector<std::uint8_t> in_place(total, 0u);
    std::copy(content.begin(), content.end(), in_place.begin());
    CHECK_EQ(pad_in_place(in_place.data(), len, total, kType, &out_length),
             PadError::kOk);
    CHECK_EQ(out_length, total);
    CHECK_EQ(in_place[len], kType);
    CHECK(std::all_of(
            in_place.begin() + static_cast<std::ptrdiff_t>(len + 1),
            in_place.end(), [](std::uint8_t b) { return b == 0u; }));
}

void test_round_trip() {
    std::mt19937 rng(0xC0FFEE);
    const std::uint8_t types[] = {20, 21, 22, 23, 24, 99, 255};
    const std::size_t lengths[] = {0,   1,   2,   254, 255, 256,
                                   511, 512, 1000, 16382, 16383};

    for (std::size_t len : lengths) {
        for (std::uint8_t type : types) {
            std::vector<std::uint8_t> content(len);
            std::uniform_int_distribution<int> dist(0, 255);
            for (std::uint8_t& b : content) {
                b = static_cast<std::uint8_t>(dist(rng));
            }
            if (len > 0u && content.back() == 0u) {
                content.back() = 0xFF;
            }

            auto record = pad(content.data(), len, type);
            CHECK(record.has_value());

            UnpadView view{};
            CHECK_EQ(unpad(record->data(), record->size(), &view),
                     UnpadError::kOk);
            CHECK_EQ(view.content_length, len);
            CHECK_EQ(view.content_type, type);
            CHECK(view.content == record->data());
            if (len > 0u) {
                CHECK(std::equal(content.begin(), content.end(),
                                 view.content));
            }
        }
    }
}

void test_all_zero_inner() {
    UnpadView view{};

    // Empty / null input is rejected before scanning.
    CHECK_EQ(unpad(nullptr, 16u, &view), UnpadError::kNullOrEmpty);
    CHECK_EQ(unpad(reinterpret_cast<const std::uint8_t*>(""), 0u, &view),
             UnpadError::kNullOrEmpty);
    CHECK_EQ(unpad(reinterpret_cast<const std::uint8_t*>("x"), 1u, nullptr),
             UnpadError::kNullOrEmpty);

    // All-zero records at several sizes, including the maximum one.
    for (const std::size_t total : {1u, 256u, 257u, 1024u, 16384u}) {
        const std::vector<std::uint8_t> zeros(total, 0u);
        CHECK_EQ(unpad(zeros.data(), total, &view),
                 UnpadError::kAllZeroInner);
        CHECK_EQ(recommended_alert(UnpadError::kAllZeroInner),
                 std::optional<std::uint8_t>(10));
    }
    CHECK(!recommended_alert(UnpadError::kOk).has_value());
}

void test_manual_records_and_internal_zeros() {
    UnpadView view{};

    // Record without padding: last non-zero octet is the type.
    const std::uint8_t no_padding[] = {0x01, 0x02, 0x17};
    CHECK_EQ(unpad(no_padding, sizeof(no_padding), &view), UnpadError::kOk);
    CHECK_EQ(view.content_type, 0x17);
    CHECK_EQ(view.content_length, 2u);
    CHECK_EQ(view.content[0], 0x01);
    CHECK_EQ(view.content[1], 0x02);

    // Zero octets inside content must be preserved.
    const std::uint8_t internal_zeros[] = {0x00, 0x00, 0x16, 0x00, 0x00};
    CHECK_EQ(unpad(internal_zeros, sizeof(internal_zeros), &view),
             UnpadError::kOk);
    CHECK_EQ(view.content_type, 0x16);
    CHECK_EQ(view.content_length, 2u);
    CHECK_EQ(view.content[0], 0u);
    CHECK_EQ(view.content[1], 0u);

    // Single non-zero octet: empty content, type only.
    const std::uint8_t lone_type[] = {0x16};
    CHECK_EQ(unpad(lone_type, 1u, &view), UnpadError::kOk);
    CHECK_EQ(view.content_type, 0x16);
    CHECK_EQ(view.content_length, 0u);

    // Type near the tail with zero padding behind it.
    std::vector<std::uint8_t> record(512u, 0u);
    record[509] = 0xAB;
    record[510] = 0xCD;
    record[511] = 0x15;  // alert
    CHECK_EQ(unpad(record.data(), record.size(), &view), UnpadError::kOk);
    CHECK_EQ(view.content_type, 0x15);
    CHECK_EQ(view.content_length, 511u);
    CHECK_EQ(view.content[509], 0xAB);
    CHECK_EQ(view.content[510], 0xCD);
}

void test_oversized_and_invalid_padding() {
    // Content that would push the inner plaintext past 2^14+1.
    std::vector<std::uint8_t> big(16384u, 0x01);
    CHECK(!pad(big.data(), big.size(), 23).has_value());

    std::uint8_t out[32];
    std::size_t out_length = 0u;
    CHECK_EQ(pad(big.data(), big.size(), 23, out, sizeof(out), &out_length),
             PadError::kContentTooLong);
    CHECK_EQ(pad(nullptr, 4u, 23, out, sizeof(out), &out_length),
             PadError::kNullInput);
    CHECK_EQ(pad(big.data(), 4u, 23, nullptr, sizeof(out), &out_length),
             PadError::kNullOutput);

    // Zero content type is illegal: it is indistinguishable from padding and
    // collides with the all-zero probe.
    CHECK(!pad(big.data(), 4u, 0).has_value());
    CHECK_EQ(pad(big.data(), 4u, 0, out, sizeof(out), &out_length),
             PadError::kZeroType);

    std::vector<std::uint8_t> in_place(64u, 1u);
    CHECK_EQ(pad_in_place(in_place.data(), 4u, 64u, 0, &out_length),
             PadError::kZeroType);
    CHECK_EQ(pad_in_place(nullptr, 4u, 64u, 23, &out_length),
             PadError::kNullInput);
    CHECK_EQ(pad_in_place(in_place.data(), 100u, 64u, 23, &out_length),
             PadError::kOutputTooSmall);
}

void test_strict_content_type() {
    UnpadView view{};
    const std::uint8_t unknown[] = {0x01, 0x99};

    // Lax mode (default): any non-zero trailing octet is a type.
    CHECK_EQ(unpad(unknown, sizeof(unknown), &view), UnpadError::kOk);
    CHECK_EQ(view.content_type, 0x99);

    // Strict mode: only RFC-defined ContentType values pass.
    CHECK_EQ(unpad(unknown, sizeof(unknown), &view, true),
             UnpadError::kUnknownContentType);
    CHECK_EQ(recommended_alert(UnpadError::kUnknownContentType),
             std::optional<std::uint8_t>(10));

    for (std::uint8_t type = 20; type <= 24; ++type) {
        const std::uint8_t known[] = {0xAA, type};
        CHECK_EQ(unpad(known, sizeof(known), &view, true), UnpadError::kOk);
        CHECK_EQ(view.content_type, type);
        CHECK(is_known_content_type(type));
    }
    CHECK(!is_known_content_type(19));
    CHECK(!is_known_content_type(25));

    // Strict mode still rejects the all-zero probe first.
    const std::vector<std::uint8_t> zeros(256u, 0u);
    CHECK_EQ(unpad(zeros.data(), zeros.size(), &view, true),
             UnpadError::kAllZeroInner);
}

// Best-effort constant-time smoke test. The scan always performs the same
// number of data-independent iterations; timing is environment sensitive, so
// this is opt-in via NETSEC_TIMING_TEST=1 and uses generous bounds.
void test_timing_smoke() {
    if (std::getenv("NETSEC_TIMING_TEST") == nullptr) {
        std::printf("[ SKIP ] timing smoke test (set NETSEC_TIMING_TEST=1)\n");
        return;
    }

    constexpr std::size_t kRecordSize = 16384u;
    constexpr int kRounds = 20000;

    auto measure = [](const std::vector<std::uint8_t>& record) {
        UnpadView view{};
        volatile std::uint8_t sink = 0;
        const auto start = std::chrono::steady_clock::now();
        for (int round = 0; round < kRounds; ++round) {
            if (unpad(record.data(), record.size(), &view) !=
                UnpadError::kOk) {
                std::abort();
            }
            sink ^= view.content_type;
        }
        const auto end = std::chrono::steady_clock::now();
        (void)sink;
        return static_cast<double>(
                std::chrono::duration_cast<std::chrono::nanoseconds>(
                        end - start)
                        .count());
    };

    // Same record length, type at opposite ends: scan work must be equal.
    std::vector<std::uint8_t> type_front(kRecordSize, 0u);
    type_front[0] = 23;
    std::vector<std::uint8_t> type_back(kRecordSize, 0u);
    type_back[kRecordSize - 1u] = 23;

    const double front_ns = measure(type_front);
    const double back_ns = measure(type_back);
    std::printf("[ TIMING ] front=%.0fns back=%.0fns per record\n",
                front_ns / kRounds, back_ns / kRounds);

    const double ratio = std::max(front_ns, back_ns) /
                         std::min(front_ns, back_ns);
    CHECK(ratio < 2.0);
}

}  // namespace

int main() {
    test_padded_length_math();
    test_padding_layout_and_indistinguishability();
    test_round_trip();
    test_all_zero_inner();
    test_manual_records_and_internal_zeros();
    test_oversized_and_invalid_padding();
    test_strict_content_type();
    test_timing_smoke();

    std::printf("%d checks, %d failures\n", g_checks, g_failures);
    return g_failures == 0 ? 0 : 1;
}
