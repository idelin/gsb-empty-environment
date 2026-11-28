#ifndef BMFF_AUDIT_HPP
#define BMFF_AUDIT_HPP

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

namespace bmff {

constexpr std::uint32_t make_fourcc(char a, char b, char c, char d) noexcept
{
    return (static_cast<std::uint32_t>(static_cast<unsigned char>(a)) << 24U) |
           (static_cast<std::uint32_t>(static_cast<unsigned char>(b)) << 16U) |
           (static_cast<std::uint32_t>(static_cast<unsigned char>(c)) << 8U) |
           static_cast<std::uint32_t>(static_cast<unsigned char>(d));
}

inline constexpr std::uint32_t kFtyp = make_fourcc('f', 't', 'y', 'p');
inline constexpr std::uint32_t kMoov = make_fourcc('m', 'o', 'o', 'v');
inline constexpr std::uint32_t kTrak = make_fourcc('t', 'r', 'a', 'k');
inline constexpr std::uint32_t kMdia = make_fourcc('m', 'd', 'i', 'a');
inline constexpr std::uint32_t kMinf = make_fourcc('m', 'i', 'n', 'f');
inline constexpr std::uint32_t kStbl = make_fourcc('s', 't', 'b', 'l');
inline constexpr std::uint32_t kMdat = make_fourcc('m', 'd', 'a', 't');
inline constexpr std::uint32_t kStco = make_fourcc('s', 't', 'c', 'o');
inline constexpr std::uint32_t kCo64 = make_fourcc('c', 'o', '6', '4');
inline constexpr std::uint32_t kStsz = make_fourcc('s', 't', 's', 'z');
inline constexpr std::uint32_t kStsc = make_fourcc('s', 't', 's', 'c');

enum class ErrorCode {
    EmptyInput,
    MissingFtyp,
    InvalidFtyp,
    DuplicateFtyp,
    MisplacedFtyp,
    TruncatedBoxHeader,
    InvalidBoxSize,
    SizeOverflow,
    BoxExtendsPastParent,
    SizeZeroInsideContainer,
    DepthLimitExceeded,
    TooManyBoxes,
    DuplicateMoov,
    MisplacedMoov,
    MisplacedMdat,
    MisplacedSampleTable,
    NestedStbl,
    TableLengthMismatch,
    InvalidSampleTable,
    MissingSampleTable,
    TooManySamples,
    TooManyChunks,
    SampleTooLarge,
    TotalSampleBytesTooLarge,
    ChunkOutsideMdat,
    OverlappingChunks,
    MdatCoverageGap
};

const char* to_string(ErrorCode code) noexcept;

struct AuditError {
    ErrorCode code = ErrorCode::EmptyInput;
    std::uint64_t offset = 0;
    std::string message;
};

struct AuditOptions {
    std::uint32_t max_depth = 16U;
    std::uint64_t max_boxes = 1'000'000ULL;
    std::uint64_t max_sample_count = 10'000'000ULL;
    std::uint64_t max_chunk_count = 10'000'000ULL;
    std::uint64_t max_single_sample_size = 256ULL * 1024ULL * 1024ULL;
    std::uint64_t max_total_sample_bytes = 8ULL * 1024ULL * 1024ULL * 1024ULL;
    bool require_mdat_coverage = true;
};

struct BoxInfo {
    std::uint32_t type = 0;
    std::uint64_t offset = 0;
    std::uint64_t size = 0;
    std::uint64_t header_size = 0;
    std::uint64_t body_offset = 0;
    std::uint64_t body_size = 0;
    std::uint32_t depth = 0;
    std::size_t parent = static_cast<std::size_t>(-1);
};

struct MdatInfo {
    std::uint64_t box_offset = 0;
    std::uint64_t box_size = 0;
    std::uint64_t payload_offset = 0;
    std::uint64_t payload_size = 0;
    std::uint64_t payload_end = 0;
};

struct AuditStats {
    std::uint64_t boxes = 0;
    std::uint64_t top_level_boxes = 0;
    std::uint64_t mdats = 0;
    std::uint64_t sample_tables = 0;
    std::uint64_t chunks = 0;
    std::uint64_t samples = 0;
    std::uint64_t sample_bytes = 0;
    std::uint64_t mdat_payload_bytes = 0;
    std::uint32_t max_depth_seen = 0;
};

struct AuditResult {
    std::vector<AuditError> errors;
    std::vector<BoxInfo> boxes;
    std::vector<MdatInfo> mdats;
    AuditStats stats;

    bool ok() const noexcept { return errors.empty(); }

    const AuditError* first_error() const noexcept
    {
        return errors.empty() ? nullptr : &errors.front();
    }
};

std::string fourcc_to_string(std::uint32_t type);

AuditResult audit(const std::uint8_t* data, std::size_t size,
                  const AuditOptions& options = AuditOptions{});

inline AuditResult audit(const std::vector<std::uint8_t>& data,
                         const AuditOptions& options = AuditOptions{})
{
    return audit(data.data(), data.size(), options);
}

} // namespace bmff

#endif
