#include "bmff/audit.hpp"

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <iostream>
#include <limits>
#include <stdexcept>
#include <string>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;
using bmff::ErrorCode;
using u32 = std::uint32_t;
using u64 = std::uint64_t;
using usize = std::size_t;

int g_checks = 0;
int g_failures = 0;

void check_impl(bool condition, const char* expression, int line)
{
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
    }
}

#define CHECK(expression) check_impl((expression), #expression, __LINE__)

void append_u32(Bytes& bytes, u32 value)
{
    bytes.push_back(static_cast<std::uint8_t>((value >> 24U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 16U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>(value & 0xFFU));
}

void append_u64(Bytes& bytes, u64 value)
{
    bytes.push_back(static_cast<std::uint8_t>((value >> 56U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 48U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 40U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 32U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 24U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 16U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>((value >> 8U) & 0xFFU));
    bytes.push_back(static_cast<std::uint8_t>(value & 0xFFU));
}

void write_u32(Bytes& bytes, usize offset, u32 value)
{
    if (offset + 4U > bytes.size()) {
        throw std::runtime_error("test write_u32 out of range");
    }
    bytes[offset] = static_cast<std::uint8_t>((value >> 24U) & 0xFFU);
    bytes[offset + 1U] =
        static_cast<std::uint8_t>((value >> 16U) & 0xFFU);
    bytes[offset + 2U] = static_cast<std::uint8_t>((value >> 8U) & 0xFFU);
    bytes[offset + 3U] = static_cast<std::uint8_t>(value & 0xFFU);
}

void write_u64(Bytes& bytes, usize offset, u64 value)
{
    if (offset + 8U > bytes.size()) {
        throw std::runtime_error("test write_u64 out of range");
    }
    for (unsigned i = 0; i < 8U; ++i) {
        const unsigned shift = (7U - i) * 8U;
        bytes[offset + static_cast<usize>(i)] =
            static_cast<std::uint8_t>((value >> shift) & 0xFFU);
    }
}

u32 read_u32(const Bytes& bytes, usize offset)
{
    if (offset + 4U > bytes.size()) {
        throw std::runtime_error("test read_u32 out of range");
    }
    return (static_cast<u32>(bytes[offset]) << 24U) |
           (static_cast<u32>(bytes[offset + 1U]) << 16U) |
           (static_cast<u32>(bytes[offset + 2U]) << 8U) |
           static_cast<u32>(bytes[offset + 3U]);
}

Bytes make_box(u32 type, const Bytes& body)
{
    const usize total = body.size() + 8U;
    if (total > static_cast<usize>(std::numeric_limits<u32>::max())) {
        throw std::runtime_error("test box too large");
    }
    Bytes result;
    result.reserve(total);
    append_u32(result, static_cast<u32>(total));
    append_u32(result, type);
    result.insert(result.end(), body.begin(), body.end());
    return result;
}

usize find_box_start(const Bytes& bytes, u32 type)
{
    Bytes needle;
    append_u32(needle, type);
    const auto found =
        std::search(bytes.begin(), bytes.end(), needle.begin(), needle.end());
    if (found == bytes.end()) {
        throw std::runtime_error("test box type not found");
    }
    const usize type_offset =
        static_cast<usize>(std::distance(bytes.begin(), found));
    if (type_offset < 4U) {
        throw std::runtime_error("test box type has no size field");
    }
    return type_offset - 4U;
}

Bytes make_ftyp()
{
    Bytes body;
    append_u32(body, bmff::make_fourcc('i', 's', 'o', 'm'));
    append_u32(body, 0U);
    append_u32(body, bmff::make_fourcc('i', 's', 'o', 'm'));
    append_u32(body, bmff::make_fourcc('m', 'p', '4', '1'));
    return make_box(bmff::kFtyp, body);
}

struct StscSpec {
    u32 first_chunk = 1U;
    u32 samples_per_chunk = 1U;
    u32 sample_description_index = 1U;
};

struct SampleBuild {
    u32 sample_size = 0U;
    u32 sample_count = 2U;
    std::vector<u32> sample_sizes = {8U, 8U};
    std::vector<StscSpec> stsc = {StscSpec{1U, 2U, 1U}};
    std::vector<u64> chunk_payload_offsets = {0U};
    Bytes payload = Bytes(16U, static_cast<std::uint8_t>(0x11));
    bool use_co64 = false;
};

struct SampleFile {
    Bytes bytes;
    usize stsz_start = 0;
    usize chunk_entry_start = 0;
    usize mdat_start = 0;
    usize mdat_payload_start = 0;
    bool use_co64 = false;
};

SampleFile build_sample(const SampleBuild& spec)
{
    Bytes stsz_body;
    append_u32(stsz_body, 0U);
    append_u32(stsz_body, spec.sample_size);
    append_u32(stsz_body, spec.sample_count);
    if (spec.sample_size == 0U) {
        if (spec.sample_sizes.size() !=
            static_cast<usize>(spec.sample_count)) {
            throw std::runtime_error("test stsz size count mismatch");
        }
        for (u32 size : spec.sample_sizes) {
            append_u32(stsz_body, size);
        }
    }

    Bytes stsc_body;
    append_u32(stsc_body, 0U);
    append_u32(stsc_body, static_cast<u32>(spec.stsc.size()));
    for (const StscSpec& entry : spec.stsc) {
        append_u32(stsc_body, entry.first_chunk);
        append_u32(stsc_body, entry.samples_per_chunk);
        append_u32(stsc_body, entry.sample_description_index);
    }

    Bytes chunk_body;
    append_u32(chunk_body, 0U);
    append_u32(chunk_body,
               static_cast<u32>(spec.chunk_payload_offsets.size()));
    const usize entry_stride = spec.use_co64 ? 8U : 4U;
    chunk_body.insert(chunk_body.end(),
                      spec.chunk_payload_offsets.size() * entry_stride, 0U);

    Bytes stbl_body;
    const Bytes stsc_box = make_box(bmff::kStsc, stsc_body);
    const Bytes stsz_box = make_box(bmff::kStsz, stsz_body);
    const Bytes chunk_box =
        make_box(spec.use_co64 ? bmff::kCo64 : bmff::kStco, chunk_body);
    stbl_body.insert(stbl_body.end(), stsc_box.begin(), stsc_box.end());
    stbl_body.insert(stbl_body.end(), stsz_box.begin(), stsz_box.end());
    stbl_body.insert(stbl_body.end(), chunk_box.begin(), chunk_box.end());

    const Bytes stbl = make_box(bmff::kStbl, stbl_body);
    const Bytes minf = make_box(bmff::kMinf, stbl);
    const Bytes mdia = make_box(bmff::kMdia, minf);
    const Bytes trak = make_box(bmff::kTrak, mdia);
    const Bytes moov = make_box(bmff::kMoov, trak);
    const Bytes ftyp = make_ftyp();
    const Bytes mdat = make_box(bmff::kMdat, spec.payload);

    SampleFile result;
    result.use_co64 = spec.use_co64;
    result.bytes.insert(result.bytes.end(), ftyp.begin(), ftyp.end());
    result.bytes.insert(result.bytes.end(), moov.begin(), moov.end());
    result.bytes.insert(result.bytes.end(), mdat.begin(), mdat.end());

    result.stsz_start = find_box_start(result.bytes, bmff::kStsz);
    result.mdat_start = find_box_start(result.bytes, bmff::kMdat);
    result.mdat_payload_start = result.mdat_start + 8U;
    const usize chunk_start = find_box_start(
        result.bytes, spec.use_co64 ? bmff::kCo64 : bmff::kStco);
    result.chunk_entry_start = chunk_start + 16U;

    for (usize i = 0; i < spec.chunk_payload_offsets.size(); ++i) {
        const u64 relative = spec.chunk_payload_offsets[i];
        const u64 absolute =
            static_cast<u64>(result.mdat_payload_start) + relative;
        const usize entry = result.chunk_entry_start + i * entry_stride;
        if (spec.use_co64) {
            write_u64(result.bytes, entry, absolute);
        } else {
            if (absolute >
                static_cast<u64>(std::numeric_limits<u32>::max())) {
                throw std::runtime_error("test stco offset too large");
            }
            write_u32(result.bytes, entry, static_cast<u32>(absolute));
        }
    }
    return result;
}

bool has_error(const bmff::AuditResult& result, ErrorCode code)
{
    for (const bmff::AuditError& error : result.errors) {
        if (error.code == code) {
            return true;
        }
    }
    return false;
}

void report_invalid(const char* name, const bmff::AuditResult& result)
{
    if (!result.ok()) {
        const bmff::AuditError* error = result.first_error();
        if (error != nullptr) {
            std::cerr << name << ": " << bmff::to_string(error->code)
                      << " at " << error->offset << ": " << error->message
                      << '\n';
        }
    }
}

void test_valid_sample()
{
    const SampleFile sample = build_sample(SampleBuild{});
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(result.ok());
    CHECK(result.stats.boxes >= 8U);
    CHECK(result.stats.top_level_boxes == 3U);
    CHECK(result.stats.mdats == 1U);
    CHECK(result.stats.chunks == 1U);
    CHECK(result.stats.samples == 2U);
    CHECK(result.stats.sample_bytes == 16U);
    CHECK(result.stats.mdat_payload_bytes == 16U);
    CHECK(!result.boxes.empty());
    if (!result.boxes.empty()) {
        CHECK(result.boxes.front().type == bmff::kFtyp);
    }
    report_invalid("valid sample", result);
}

void test_co64_sample()
{
    SampleBuild spec;
    spec.use_co64 = true;
    const SampleFile sample = build_sample(spec);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(result.ok());
    CHECK(result.stats.samples == 2U);
    report_invalid("co64 sample", result);
}

void test_multi_entry_stsc()
{
    SampleBuild spec;
    spec.sample_count = 3U;
    spec.sample_sizes = {4U, 6U, 8U};
    spec.stsc = {
        StscSpec{1U, 1U, 1U},
        StscSpec{2U, 2U, 1U},
    };
    spec.chunk_payload_offsets = {0U, 4U};
    spec.payload = Bytes(18U, static_cast<std::uint8_t>(0x66));
    const SampleFile sample = build_sample(spec);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(result.ok());
    CHECK(result.stats.chunks == 2U);
    CHECK(result.stats.samples == 3U);
    CHECK(result.stats.sample_bytes == 18U);
    report_invalid("multi-entry stsc", result);
}

void test_size_zero_final_mdat()
{
    SampleFile sample = build_sample(SampleBuild{});
    write_u32(sample.bytes, sample.mdat_start, 0U);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(result.ok());
    CHECK(result.stats.mdats == 1U);
    report_invalid("size zero mdat", result);
}

void test_size_zero_single_ftyp()
{
    Bytes bytes = make_ftyp();
    write_u32(bytes, 0U, 0U);
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(result.ok());
    CHECK(result.stats.top_level_boxes == 1U);
    report_invalid("size zero ftyp", result);
}

void test_leading_polyglot()
{
    const Bytes junk =
        make_box(bmff::make_fourcc('j', 'u', 'n', 'k'),
                 Bytes{0xDEU, 0xADU, 0xBEU, 0xEFU});
    const SampleFile sample = build_sample(SampleBuild{});
    Bytes polyglot = junk;
    polyglot.insert(polyglot.end(), sample.bytes.begin(), sample.bytes.end());
    const bmff::AuditResult result = bmff::audit(polyglot);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::MissingFtyp));
}

void test_non_ascii_ftyp_brand()
{
    Bytes body;
    append_u32(body, bmff::make_fourcc('i', 's', 'o', 'm'));
    append_u32(body, 0U);
    append_u32(body, 0x7F454C46U);
    const Bytes bytes = make_box(bmff::kFtyp, body);
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::InvalidFtyp));
}

void test_trailing_polyglot()
{
    SampleFile sample = build_sample(SampleBuild{});
    const Bytes tail{0x7FU, 0x45U, 0x4CU, 0x46U};
    sample.bytes.insert(sample.bytes.end(), tail.begin(), tail.end());
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::TruncatedBoxHeader));
}

void test_largesize_overflow()
{
    Bytes bytes = make_ftyp();
    append_u32(bytes, 1U);
    append_u32(bytes, bmff::kMdat);
    append_u64(bytes, std::numeric_limits<u64>::max());
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::SizeOverflow));
}

void test_valid_largesize_box()
{
    SampleFile sample = build_sample(SampleBuild{});
    append_u32(sample.bytes, 1U);
    append_u32(sample.bytes, bmff::make_fourcc('f', 'r', 'e', 'e'));
    append_u64(sample.bytes, 16U);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(result.ok());
    CHECK(result.stats.top_level_boxes == 4U);
    report_invalid("largesize free box", result);
}

void test_small_box_size()
{
    Bytes bytes = make_ftyp();
    append_u32(bytes, 4U);
    append_u32(bytes, bmff::kMdat);
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::InvalidBoxSize));
}

void test_child_extends_past_parent()
{
    Bytes child;
    append_u32(child, 100U);
    append_u32(child, bmff::make_fourcc('f', 'r', 'e', 'e'));
    const Bytes moov = make_box(bmff::kMoov, child);
    Bytes bytes = make_ftyp();
    bytes.insert(bytes.end(), moov.begin(), moov.end());
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::BoxExtendsPastParent));
}

void test_chunk_points_into_moov()
{
    SampleFile sample = build_sample(SampleBuild{});
    write_u32(sample.bytes, sample.chunk_entry_start, 32U);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::ChunkOutsideMdat));
}

void test_depth_limit()
{
    Bytes nested;
    for (unsigned i = 0; i < 20U; ++i) {
        nested = make_box(bmff::kTrak, nested);
    }
    const Bytes moov = make_box(bmff::kMoov, nested);
    Bytes bytes = make_ftyp();
    bytes.insert(bytes.end(), moov.begin(), moov.end());
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::DepthLimitExceeded));
}

void test_duplicate_moov()
{
    SampleFile sample = build_sample(SampleBuild{});
    const usize moov_start = find_box_start(sample.bytes, bmff::kMoov);
    const u32 moov_size = read_u32(sample.bytes, moov_start);
    const auto moov_begin =
        sample.bytes.begin() + static_cast<std::ptrdiff_t>(moov_start);
    const auto moov_end =
        sample.bytes.begin() +
        static_cast<std::ptrdiff_t>(moov_start + static_cast<usize>(moov_size));
    const Bytes moov(moov_begin, moov_end);
    sample.bytes.insert(moov_end, moov.begin(), moov.end());
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::DuplicateMoov));
}

void test_stsz_length_mismatch()
{
    SampleFile sample = build_sample(SampleBuild{});
    write_u32(sample.bytes, sample.stsz_start + 16U, 3U);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::TableLengthMismatch));
}

void test_stco_length_mismatch()
{
    SampleFile sample = build_sample(SampleBuild{});
    write_u32(sample.bytes, sample.chunk_entry_start - 4U, 2U);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::TableLengthMismatch));
}

void test_overlapping_chunks()
{
    SampleBuild spec;
    spec.sample_count = 2U;
    spec.sample_sizes = {8U, 8U};
    spec.stsc = {StscSpec{1U, 1U, 1U}};
    spec.chunk_payload_offsets = {0U, 4U};
    spec.payload = Bytes(16U, static_cast<std::uint8_t>(0x22));
    const SampleFile sample = build_sample(spec);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::OverlappingChunks));
}

void test_mdat_gap()
{
    SampleBuild spec;
    spec.sample_count = 2U;
    spec.sample_sizes = {8U, 8U};
    spec.stsc = {StscSpec{1U, 1U, 1U}};
    spec.chunk_payload_offsets = {0U, 9U};
    spec.payload = Bytes(20U, static_cast<std::uint8_t>(0x33));
    const SampleFile sample = build_sample(spec);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::MdatCoverageGap));
}

void test_oversized_sample_dos()
{
    SampleBuild spec;
    spec.sample_size = std::numeric_limits<u32>::max();
    spec.sample_count = 1U;
    spec.sample_sizes.clear();
    spec.stsc = {StscSpec{1U, 1U, 1U}};
    spec.chunk_payload_offsets = {0U};
    spec.payload = Bytes(16U, static_cast<std::uint8_t>(0x44));
    const SampleFile sample = build_sample(spec);
    const bmff::AuditResult result = bmff::audit(sample.bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::SampleTooLarge));
}

void test_sample_count_limit()
{
    SampleBuild spec;
    spec.sample_size = 1U;
    spec.sample_count = 11U;
    spec.sample_sizes.clear();
    spec.stsc = {StscSpec{1U, 11U, 1U}};
    spec.chunk_payload_offsets = {0U};
    spec.payload = Bytes(11U, static_cast<std::uint8_t>(0x55));
    const SampleFile sample = build_sample(spec);

    bmff::AuditOptions options;
    options.max_sample_count = 10U;
    const bmff::AuditResult result = bmff::audit(sample.bytes, options);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::TooManySamples));
}

void test_zero_size_inside_container()
{
    Bytes child;
    append_u32(child, 0U);
    append_u32(child, bmff::make_fourcc('f', 'r', 'e', 'e'));
    const Bytes moov = make_box(bmff::kMoov, child);
    Bytes bytes = make_ftyp();
    bytes.insert(bytes.end(), moov.begin(), moov.end());
    const bmff::AuditResult result = bmff::audit(bytes);
    CHECK(!result.ok());
    CHECK(has_error(result, ErrorCode::SizeZeroInsideContainer));
}

void test_mutation_smoke()
{
    const SampleFile sample = build_sample(SampleBuild{});
    u64 state = 0x9E3779B97F4A7C15ULL;
    bool completed = true;
    for (usize iteration = 0; iteration < 512U; ++iteration) {
        Bytes mutated = sample.bytes;
        for (unsigned flip = 0; flip < 3U; ++flip) {
            state ^= state << 13U;
            state ^= state >> 7U;
            state ^= state << 17U;
            const usize position =
                static_cast<usize>(state % static_cast<u64>(mutated.size()));
            mutated[position] =
                static_cast<std::uint8_t>(mutated[position] ^
                                          static_cast<std::uint8_t>(
                                              (state >> 32U) & 0xFFU));
        }
        const bmff::AuditResult result = bmff::audit(mutated);
        completed = completed && (result.ok() || !result.errors.empty());
    }
    CHECK(completed);
}

} // namespace

int main()
{
    test_valid_sample();
    test_co64_sample();
    test_multi_entry_stsc();
    test_size_zero_final_mdat();
    test_size_zero_single_ftyp();
    test_leading_polyglot();
    test_non_ascii_ftyp_brand();
    test_trailing_polyglot();
    test_largesize_overflow();
    test_valid_largesize_box();
    test_small_box_size();
    test_child_extends_past_parent();
    test_chunk_points_into_moov();
    test_depth_limit();
    test_duplicate_moov();
    test_stsz_length_mismatch();
    test_stco_length_mismatch();
    test_overlapping_chunks();
    test_mdat_gap();
    test_oversized_sample_dos();
    test_sample_count_limit();
    test_zero_size_inside_container();
    test_mutation_smoke();

    if (g_failures != 0) {
        std::cerr << g_failures << " of " << g_checks << " checks failed\n";
        return 1;
    }
    std::cout << "All " << g_checks << " checks passed\n";
    return 0;
}
