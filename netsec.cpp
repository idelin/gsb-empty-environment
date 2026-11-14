#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cstring>
#include <iomanip>
#include <sstream>
#include <utility>

namespace netsec {
namespace {

// ---------------------------------------------------------------------------
// SHA-512
// ---------------------------------------------------------------------------

constexpr std::array<std::uint64_t, 80> kSha512K = {
    0x428a2f98d728ae22ULL, 0x7137449123ef65cdULL, 0xb5c0fbcfec4d3b2fULL,
    0xe9b5dba58189dbbcULL, 0x3956c25bf348b538ULL, 0x59f111f1b605d019ULL,
    0x923f82a4af194f9bULL, 0xab1c5ed5da6d8118ULL, 0xd807aa98a3030242ULL,
    0x12835b0145706fbeULL, 0x243185be4ee4b28cULL, 0x550c7dc3d5ffb4e2ULL,
    0x72be5d74f27b896fULL, 0x80deb1fe3b1696b1ULL, 0x9bdc06a725c71235ULL,
    0xc19bf174cf692694ULL, 0xe49b69c19ef14ad2ULL, 0xefbe4786384f25e3ULL,
    0x0fc19dc68b8cd5b5ULL, 0x240ca1cc77ac9c65ULL, 0x2de92c6f592b0275ULL,
    0x4a7484aa6ea6e483ULL, 0x5cb0a9dcbd41fbd4ULL, 0x76f988da831153b5ULL,
    0x983e5152ee66dfabULL, 0xa831c66d2db43210ULL, 0xb00327c898fb213fULL,
    0xbf597fc7beef0ee4ULL, 0xc6e00bf33da88fc2ULL, 0xd5a79147930aa725ULL,
    0x06ca6351e003826fULL, 0x142929670a0e6e70ULL, 0x27b70a8546d22ffcULL,
    0x2e1b21385c26c926ULL, 0x4d2c6dfc5ac42aedULL, 0x53380d139d95b3dfULL,
    0x650a73548baf63deULL, 0x766a0abb3c77b2a8ULL, 0x81c2c92e47edaee6ULL,
    0x92722c851482353bULL, 0xa2bfe8a14cf10364ULL, 0xa81a664bbc423001ULL,
    0xc24b8b70d0f89791ULL, 0xc76c51a30654be30ULL, 0xd192e819d6ef5218ULL,
    0xd69906245565a910ULL, 0xf40e35855771202aULL, 0x106aa07032bbd1b8ULL,
    0x19a4c116b8d2d0c8ULL, 0x1e376c085141ab53ULL, 0x2748774cdf8eeb99ULL,
    0x34b0bcb5e19b48a8ULL, 0x391c0cb3c5c95a63ULL, 0x4ed8aa4ae3418acbULL,
    0x5b9cca4f7763e373ULL, 0x682e6ff3d6b2b8a3ULL, 0x748f82ee5defb2fcULL,
    0x78a5636f43172f60ULL, 0x84c87814a1f0ab72ULL, 0x8cc702081a6439ecULL,
    0x90befffa23631e28ULL, 0xa4506cebde82bde9ULL, 0xbef9a3f7b2c67915ULL,
    0xc67178f2e372532bULL, 0xca273eceea26619cULL, 0xd186b8c721c0c207ULL,
    0xeada7dd6cde0eb1eULL, 0xf57d4f7fee6ed178ULL, 0x06f067aa72176fbaULL,
    0x0a637dc5a2c898a6ULL, 0x113f9804bef90daeULL, 0x1b710b35131c471bULL,
    0x28db77f523047d84ULL, 0x32caab7b40c72493ULL, 0x3c9ebe0a15c9bebcULL,
    0x431d67c49c100d4cULL, 0x4cc5d4becb3e42b6ULL, 0x597f299cfc657e2aULL,
    0x5fcb6fab3ad6faecULL, 0x6c44198c4a475817ULL,
};

constexpr std::array<std::uint32_t, 64> kSha256K = {
    0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u, 0x3956c25bu,
    0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u, 0xd807aa98u, 0x12835b01u,
    0x243185beu, 0x550c7dc3u, 0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u,
    0xc19bf174u, 0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
    0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau, 0x983e5152u,
    0xa831c66du, 0xb00327c8u, 0xbf597fc7u, 0xc6e00bf3u, 0xd5a79147u,
    0x06ca6351u, 0x14292967u, 0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu,
    0x53380d13u, 0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
    0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u, 0xd192e819u,
    0xd6990624u, 0xf40e3585u, 0x106aa070u, 0x19a4c116u, 0x1e376c08u,
    0x2748774cu, 0x34b0bcb5u, 0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu,
    0x682e6ff3u, 0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
    0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
};

constexpr std::uint64_t rotr64(std::uint64_t value, unsigned amount) noexcept {
    return (value >> amount) | (value << (64 - amount));
}

constexpr std::uint32_t rotr32(std::uint32_t value, unsigned amount) noexcept {
    return (value >> amount) | (value << (32 - amount));
}

std::uint64_t load_be64(const Byte* data) noexcept {
    return (static_cast<std::uint64_t>(data[0]) << 56) |
           (static_cast<std::uint64_t>(data[1]) << 48) |
           (static_cast<std::uint64_t>(data[2]) << 40) |
           (static_cast<std::uint64_t>(data[3]) << 32) |
           (static_cast<std::uint64_t>(data[4]) << 24) |
           (static_cast<std::uint64_t>(data[5]) << 16) |
           (static_cast<std::uint64_t>(data[6]) << 8) |
           static_cast<std::uint64_t>(data[7]);
}

std::uint32_t load_be32(const Byte* data) noexcept {
    return (static_cast<std::uint32_t>(data[0]) << 24) |
           (static_cast<std::uint32_t>(data[1]) << 16) |
           (static_cast<std::uint32_t>(data[2]) << 8) |
           static_cast<std::uint32_t>(data[3]);
}

void store_be64(Byte* out, std::uint64_t value) noexcept {
    for (int i = 7; i >= 0; --i) {
        out[i] = static_cast<Byte>(value & 0xffu);
        value >>= 8;
    }
}

void store_be32(Byte* out, std::uint32_t value) noexcept {
    for (int i = 3; i >= 0; --i) {
        out[i] = static_cast<Byte>(value & 0xffu);
        value >>= 8;
    }
}

void sha512_compress(std::array<std::uint64_t, 8>& state, const Byte* block) noexcept {
    std::array<std::uint64_t, 80> schedule{};
    for (std::size_t i = 0; i < 16; ++i) {
        schedule[i] = load_be64(block + i * 8);
    }
    for (std::size_t i = 16; i < 80; ++i) {
        const std::uint64_t s0 =
            rotr64(schedule[i - 15], 1) ^ rotr64(schedule[i - 15], 8) ^
            (schedule[i - 15] >> 7);
        const std::uint64_t s1 =
            rotr64(schedule[i - 2], 19) ^ rotr64(schedule[i - 2], 61) ^
            (schedule[i - 2] >> 6);
        schedule[i] = schedule[i - 16] + s0 + schedule[i - 7] + s1;
    }

    std::uint64_t a = state[0];
    std::uint64_t b = state[1];
    std::uint64_t c = state[2];
    std::uint64_t d = state[3];
    std::uint64_t e = state[4];
    std::uint64_t f = state[5];
    std::uint64_t g = state[6];
    std::uint64_t h = state[7];

    for (std::size_t i = 0; i < 80; ++i) {
        const std::uint64_t sum1 =
            rotr64(e, 14) ^ rotr64(e, 18) ^ rotr64(e, 41);
        const std::uint64_t choose = (e & f) ^ ((~e) & g);
        const std::uint64_t temp1 = h + sum1 + choose + kSha512K[i] + schedule[i];
        const std::uint64_t sum0 =
            rotr64(a, 28) ^ rotr64(a, 34) ^ rotr64(a, 39);
        const std::uint64_t majority = (a & b) ^ (a & c) ^ (b & c);
        const std::uint64_t temp2 = sum0 + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

void sha256_compress(std::array<std::uint32_t, 8>& state, const Byte* block) noexcept {
    std::array<std::uint32_t, 64> schedule{};
    for (std::size_t i = 0; i < 16; ++i) {
        schedule[i] = load_be32(block + i * 4);
    }
    for (std::size_t i = 16; i < 64; ++i) {
        const std::uint32_t s0 =
            rotr32(schedule[i - 15], 7) ^ rotr32(schedule[i - 15], 18) ^
            (schedule[i - 15] >> 3);
        const std::uint32_t s1 =
            rotr32(schedule[i - 2], 17) ^ rotr32(schedule[i - 2], 19) ^
            (schedule[i - 2] >> 10);
        schedule[i] = schedule[i - 16] + s0 + schedule[i - 7] + s1;
    }

    std::uint32_t a = state[0];
    std::uint32_t b = state[1];
    std::uint32_t c = state[2];
    std::uint32_t d = state[3];
    std::uint32_t e = state[4];
    std::uint32_t f = state[5];
    std::uint32_t g = state[6];
    std::uint32_t h = state[7];

    for (std::size_t i = 0; i < 64; ++i) {
        const std::uint32_t sum1 =
            rotr32(e, 6) ^ rotr32(e, 11) ^ rotr32(e, 25);
        const std::uint32_t choose = (e & f) ^ ((~e) & g);
        const std::uint32_t temp1 = h + sum1 + choose + kSha256K[i] + schedule[i];
        const std::uint32_t sum0 =
            rotr32(a, 2) ^ rotr32(a, 13) ^ rotr32(a, 22);
        const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
        const std::uint32_t temp2 = sum0 + majority;

        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state[0] += a;
    state[1] += b;
    state[2] += c;
    state[3] += d;
    state[4] += e;
    state[5] += f;
    state[6] += g;
    state[7] += h;
}

Bytes pad_message(ByteView data, std::size_t block_size, std::size_t length_field_size) {
    Bytes padded(data.begin(), data.end());
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(data.size()) * 8u;
    padded.push_back(0x80);
    while ((padded.size() % block_size) != (block_size - length_field_size)) {
        padded.push_back(0x00);
    }
    for (std::size_t i = length_field_size; i > 0; --i) {
        const std::size_t shift = (i - 1) * 8;
        padded.push_back(shift < 64
                             ? static_cast<Byte>((bit_length >> shift) & 0xffu)
                             : static_cast<Byte>(0));
    }
    return padded;
}

// ---------------------------------------------------------------------------
// Little-endian SMB wire helpers
// ---------------------------------------------------------------------------

std::uint16_t read_le16(ByteView data, std::size_t offset) noexcept {
    return static_cast<std::uint16_t>(data[offset]) |
           static_cast<std::uint16_t>(static_cast<std::uint16_t>(data[offset + 1]) << 8);
}

std::uint32_t read_le32(ByteView data, std::size_t offset) noexcept {
    return static_cast<std::uint32_t>(data[offset]) |
           (static_cast<std::uint32_t>(data[offset + 1]) << 8) |
           (static_cast<std::uint32_t>(data[offset + 2]) << 16) |
           (static_cast<std::uint32_t>(data[offset + 3]) << 24);
}

void append_be16(Bytes& out, std::uint16_t value) {
    out.push_back(static_cast<Byte>((value >> 8) & 0xffu));
    out.push_back(static_cast<Byte>(value & 0xffu));
}

void append_be32(Bytes& out, std::uint32_t value) {
    out.push_back(static_cast<Byte>((value >> 24) & 0xffu));
    out.push_back(static_cast<Byte>((value >> 16) & 0xffu));
    out.push_back(static_cast<Byte>((value >> 8) & 0xffu));
    out.push_back(static_cast<Byte>(value & 0xffu));
}

void append_be64(Bytes& out, std::uint64_t value) {
    append_be32(out, static_cast<std::uint32_t>(value >> 32));
    append_be32(out, static_cast<std::uint32_t>(value & 0xffffffffu));
}

std::string lowercase_host(std::string_view host) {
    std::string result(host);
    for (char& ch : result) {
        if (ch >= 'A' && ch <= 'Z') {
            ch = static_cast<char>(ch - 'A' + 'a');
        }
    }
    if (result.size() > 1 && result.back() == '.') {
        result.pop_back();
    }
    return result;
}

Digest256 key_commitment(ByteView key) {
    static constexpr std::string_view domain = "SMB311-AUTHENTICATED-SESSION-KEY-V1";
    Bytes material(domain.begin(), domain.end());
    material.insert(material.end(), key.begin(), key.end());
    return sha256(material);
}

bool is_known_dialect(std::uint16_t value) noexcept {
    switch (value) {
        case 0x0202:
        case 0x0210:
        case 0x0300:
        case 0x0302:
        case 0x0311:
            return true;
        default:
            return false;
    }
}

int dialect_rank(Dialect dialect) noexcept {
    return static_cast<int>(static_cast<std::uint16_t>(dialect));
}

bool is_known_cipher(std::uint16_t value) noexcept {
    switch (value) {
        case 0x0000:
        case 0x0001:
        case 0x0002:
        case 0x0003:
        case 0x0004:
            return true;
        default:
            return false;
    }
}

bool is_known_compression(std::uint16_t value) noexcept {
    return value <= 0x0006;
}

std::size_t align8(std::size_t value) noexcept {
    return (value + 7u) & ~std::size_t{7u};
}

[[noreturn]] void raise(ErrorCode code, std::string message) {
    throw Error(code, std::move(message));
}

constexpr std::uint32_t kStatusSuccess = 0x00000000u;
constexpr std::uint32_t kStatusMoreProcessingRequired = 0xc0000016u;
constexpr std::uint16_t kSmb2CommandNegotiate = 0x0000u;
constexpr std::uint16_t kSmb2CommandSessionSetup = 0x0001u;
constexpr std::uint16_t kNegotiateContextPreauth = 0x0001u;
constexpr std::uint16_t kNegotiateContextEncryption = 0x0002u;
constexpr std::uint16_t kNegotiateContextCompression = 0x0003u;

std::uint32_t header_status(ByteView raw) noexcept {
    return read_le32(raw, 8);
}

std::vector<Dialect> parse_dialect_list(ByteView raw, std::size_t offset,
                                        std::uint16_t count) {
    if (count == 0) {
        return {};
    }
    if (offset > raw.size() || static_cast<std::size_t>(count) * 2u > raw.size() - offset) {
        return {};
    }
    std::vector<Dialect> result;
    result.reserve(count);
    for (std::uint16_t i = 0; i < count; ++i) {
        const std::uint16_t value = read_le16(raw, offset + static_cast<std::size_t>(i) * 2u);
        if (!is_known_dialect(value)) {
            return {};
        }
        result.push_back(static_cast<Dialect>(value));
    }
    return result;
}

bool contains_dialect(const std::vector<Dialect>& dialects, Dialect dialect) noexcept {
    return std::find(dialects.begin(), dialects.end(), dialect) != dialects.end();
}

struct ParsedContexts {
    bool saw_preauth = false;
    bool saw_encryption = false;
    std::vector<PreauthHashAlgorithm> preauth_algorithms;
    std::vector<Cipher> ciphers;
};

ParsedContexts parse_negotiate_contexts(ByteView raw, std::uint32_t offset,
                                        std::uint16_t count,
                                        bool reject_unknown_algorithms,
                                        bool reject_unknown_contexts) {
    ParsedContexts parsed;
    if (count == 0) {
        return parsed;
    }
    if (offset < kSmb2HeaderSize || offset > raw.size()) {
        raise(ErrorCode::InvalidNegotiateContext,
              "negotiate context offset is outside the message");
    }

    std::size_t position = offset;
    for (std::uint16_t index = 0; index < count; ++index) {
        if (position > raw.size() || raw.size() - position < 8u) {
            raise(ErrorCode::MessageTruncated, "truncated negotiate context header");
        }
        const std::uint16_t type = read_le16(raw, position);
        const std::uint16_t length = read_le16(raw, position + 2);
        const std::size_t data_offset = position + 8u;
        if (data_offset > raw.size() || length > raw.size() - data_offset) {
            raise(ErrorCode::MessageTruncated, "truncated negotiate context data");
        }

        switch (type) {
            case kNegotiateContextPreauth: {
                if (parsed.saw_preauth) {
                    raise(ErrorCode::DuplicateNegotiateContext,
                          "duplicate pre-authentication integrity context");
                }
                parsed.saw_preauth = true;
                if (length < 4u) {
                    raise(ErrorCode::MessageTruncated,
                          "truncated pre-authentication context");
                }
                const std::uint16_t algorithm_count = read_le16(raw, data_offset);
                const std::uint16_t salt_length = read_le16(raw, data_offset + 2);
                const std::size_t algorithm_bytes =
                    static_cast<std::size_t>(algorithm_count) * 2u;
                if (algorithm_count == 0u || algorithm_bytes > length - 4u ||
                    salt_length > length - 4u - algorithm_bytes) {
                    raise(ErrorCode::UnknownMandatoryAlgorithm,
                          "pre-authentication context has no usable algorithm");
                }
                std::unordered_set<std::uint16_t> seen;
                for (std::uint16_t i = 0; i < algorithm_count; ++i) {
                    const std::uint16_t value =
                        read_le16(raw, data_offset + 4u + static_cast<std::size_t>(i) * 2u);
                    if (!seen.insert(value).second) {
                        raise(ErrorCode::DuplicateAlgorithm,
                              "duplicate pre-authentication hash algorithm");
                    }
                    if (value == static_cast<std::uint16_t>(PreauthHashAlgorithm::Sha512)) {
                        parsed.preauth_algorithms.push_back(PreauthHashAlgorithm::Sha512);
                    } else if (reject_unknown_algorithms) {
                        raise(ErrorCode::UnknownPreauthAlgorithm,
                              "unknown mandatory pre-authentication hash algorithm");
                    }
                }
                if (parsed.preauth_algorithms.empty()) {
                    raise(ErrorCode::UnknownMandatoryAlgorithm,
                          "no supported pre-authentication hash algorithm");
                }
                break;
            }
            case kNegotiateContextEncryption: {
                if (parsed.saw_encryption) {
                    raise(ErrorCode::DuplicateNegotiateContext,
                          "duplicate encryption capabilities context");
                }
                parsed.saw_encryption = true;
                if (length < 2u) {
                    raise(ErrorCode::MessageTruncated,
                          "truncated encryption capabilities context");
                }
                const std::uint16_t cipher_count = read_le16(raw, data_offset);
                if (cipher_count == 0u ||
                    static_cast<std::size_t>(cipher_count) * 2u > length - 2u) {
                    raise(ErrorCode::UnknownMandatoryAlgorithm,
                          "encryption context has no usable cipher");
                }
                std::unordered_set<std::uint16_t> seen;
                for (std::uint16_t i = 0; i < cipher_count; ++i) {
                    const std::uint16_t value =
                        read_le16(raw, data_offset + 2u + static_cast<std::size_t>(i) * 2u);
                    if (!seen.insert(value).second) {
                        raise(ErrorCode::DuplicateAlgorithm,
                              "duplicate encryption cipher");
                    }
                    if (is_known_cipher(value)) {
                        parsed.ciphers.push_back(static_cast<Cipher>(value));
                    } else if (reject_unknown_algorithms) {
                        raise(ErrorCode::UnknownCipherAlgorithm,
                              "unknown mandatory encryption cipher");
                    }
                }
                if (parsed.ciphers.empty()) {
                    raise(ErrorCode::UnknownMandatoryAlgorithm,
                          "no supported encryption cipher");
                }
                break;
            }
            case kNegotiateContextCompression: {
                if (length < 2u) {
                    raise(ErrorCode::MessageTruncated,
                          "truncated compression capabilities context");
                }
                const std::uint16_t algorithm_count = read_le16(raw, data_offset);
                if (algorithm_count == 0u ||
                    static_cast<std::size_t>(algorithm_count) * 2u > length - 2u) {
                    raise(ErrorCode::UnknownMandatoryAlgorithm,
                          "compression context has no usable algorithm");
                }
                std::unordered_set<std::uint16_t> seen;
                for (std::uint16_t i = 0; i < algorithm_count; ++i) {
                    const std::uint16_t value =
                        read_le16(raw, data_offset + 2u + static_cast<std::size_t>(i) * 2u);
                    if (!seen.insert(value).second) {
                        raise(ErrorCode::DuplicateAlgorithm,
                              "duplicate compression algorithm");
                    }
                    if (!is_known_compression(value) && reject_unknown_algorithms) {
                        raise(ErrorCode::UnknownCompressionAlgorithm,
                              "unknown mandatory compression algorithm");
                    }
                }
                break;
            }
            default:
                if (reject_unknown_contexts) {
                    raise(ErrorCode::UnknownMandatoryAlgorithm,
                          "unknown mandatory negotiate context");
                }
                break;
        }

        const std::size_t consumed = 8u + length;
        if (index + 1u < count) {
            const std::size_t next = position + align8(consumed);
            if (next > raw.size() || next <= position) {
                raise(ErrorCode::MessageTruncated,
                      "truncated negotiate context padding");
            }
            position = next;
        }
    }
    return parsed;
}

}  // namespace

// ---------------------------------------------------------------------------
// Error and string helpers
// ---------------------------------------------------------------------------

Error::Error(ErrorCode code, std::string message)
    : std::runtime_error(std::move(message)), code_(code) {}

std::string_view to_string(ErrorCode code) noexcept {
    switch (code) {
        case ErrorCode::None: return "None";
        case ErrorCode::InvalidArgument: return "InvalidArgument";
        case ErrorCode::ChainResetNotAllowed: return "ChainResetNotAllowed";
        case ErrorCode::EmptyMessage: return "EmptyMessage";
        case ErrorCode::MessageTruncated: return "MessageTruncated";
        case ErrorCode::MessageNotSmb2: return "MessageNotSmb2";
        case ErrorCode::UnexpectedCommand: return "UnexpectedCommand";
        case ErrorCode::CompoundMessageUnsupported: return "CompoundMessageUnsupported";
        case ErrorCode::UnexpectedMessage: return "UnexpectedMessage";
        case ErrorCode::NegotiationIncomplete: return "NegotiationIncomplete";
        case ErrorCode::NegotiationAlreadyFinished: return "NegotiationAlreadyFinished";
        case ErrorCode::SessionSetupFailed: return "SessionSetupFailed";
        case ErrorCode::DuplicateNegotiateContext: return "DuplicateNegotiateContext";
        case ErrorCode::DuplicateAlgorithm: return "DuplicateAlgorithm";
        case ErrorCode::UnknownPreauthAlgorithm: return "UnknownPreauthAlgorithm";
        case ErrorCode::UnknownCipherAlgorithm: return "UnknownCipherAlgorithm";
        case ErrorCode::UnknownCompressionAlgorithm: return "UnknownCompressionAlgorithm";
        case ErrorCode::UnknownMandatoryAlgorithm: return "UnknownMandatoryAlgorithm";
        case ErrorCode::MissingPreauthIntegrityContext: return "MissingPreauthIntegrityContext";
        case ErrorCode::InvalidNegotiateContext: return "InvalidNegotiateContext";
        case ErrorCode::InvalidSecurityBuffer: return "InvalidSecurityBuffer";
        case ErrorCode::DialectNotOffered: return "DialectNotOffered";
        case ErrorCode::UnsupportedDialect: return "UnsupportedDialect";
        case ErrorCode::DialectDowngrade: return "DialectDowngrade";
        case ErrorCode::DialectMismatch: return "DialectMismatch";
        case ErrorCode::ServerGuidMismatch: return "ServerGuidMismatch";
        case ErrorCode::CipherMismatch: return "CipherMismatch";
        case ErrorCode::SigningMismatch: return "SigningMismatch";
        case ErrorCode::PreauthAlgorithmMismatch: return "PreauthAlgorithmMismatch";
        case ErrorCode::PeerIdentityMismatch: return "PeerIdentityMismatch";
        case ErrorCode::PreauthHashMismatch: return "PreauthHashMismatch";
        case ErrorCode::ProofMismatch: return "ProofMismatch";
        case ErrorCode::DuplicateChannel: return "DuplicateChannel";
        case ErrorCode::StaleAttempt: return "StaleAttempt";
        case ErrorCode::AttemptFailed: return "AttemptFailed";
        case ErrorCode::SessionNotEstablished: return "SessionNotEstablished";
        case ErrorCode::AsyncFailure: return "AsyncFailure";
        case ErrorCode::InternalError: return "InternalError";
    }
    return "Unknown";
}

bool Digest512::is_zero() const noexcept {
    for (Byte value : bytes) {
        if (value != 0) {
            return false;
        }
    }
    return true;
}

std::string Digest512::to_hex() const {
    std::ostringstream out;
    out << std::hex << std::setfill('0');
    for (Byte value : bytes) {
        out << std::setw(2) << static_cast<unsigned>(value);
    }
    return out.str();
}

std::string Digest256::to_hex() const {
    std::ostringstream out;
    out << std::hex << std::setfill('0');
    for (Byte value : bytes) {
        out << std::setw(2) << static_cast<unsigned>(value);
    }
    return out.str();
}

std::string Guid::to_hex() const {
    std::ostringstream out;
    out << std::hex << std::setfill('0');
    for (Byte value : bytes) {
        out << std::setw(2) << static_cast<unsigned>(value);
    }
    return out.str();
}

// ---------------------------------------------------------------------------
// Cryptographic primitives
// ---------------------------------------------------------------------------

Digest512 sha512(ByteView data) noexcept {
    std::array<std::uint64_t, 8> state = {
        0x6a09e667f3bcc908ULL, 0xbb67ae8584caa73bULL,
        0x3c6ef372fe94f82bULL, 0xa54ff53a5f1d36f1ULL,
        0x510e527fade682d1ULL, 0x9b05688c2b3e6c1fULL,
        0x1f83d9abfb41bd6bULL, 0x5be0cd19137e2179ULL,
    };
    const Bytes padded = pad_message(data, 128u, 16u);
    for (std::size_t offset = 0; offset < padded.size(); offset += 128u) {
        sha512_compress(state, padded.data() + offset);
    }
    Digest512 result;
    for (std::size_t i = 0; i < state.size(); ++i) {
        store_be64(result.bytes.data() + i * 8u, state[i]);
    }
    return result;
}

Digest256 sha256(ByteView data) noexcept {
    std::array<std::uint32_t, 8> state = {
        0x6a09e667u, 0xbb67ae85u, 0x3c6ef372u, 0xa54ff53au,
        0x510e527fu, 0x9b05688cu, 0x1f83d9abu, 0x5be0cd19u,
    };
    const Bytes padded = pad_message(data, 64u, 8u);
    for (std::size_t offset = 0; offset < padded.size(); offset += 64u) {
        sha256_compress(state, padded.data() + offset);
    }
    Digest256 result;
    for (std::size_t i = 0; i < state.size(); ++i) {
        store_be32(result.bytes.data() + i * 4u, state[i]);
    }
    return result;
}

Bytes hmac_sha256(ByteView key, ByteView data) {
    std::array<Byte, 64> key_block{};
    if (key.size() > key_block.size()) {
        const Digest256 hashed_key = sha256(key);
        std::copy(hashed_key.bytes.begin(), hashed_key.bytes.end(), key_block.begin());
    } else {
        std::copy(key.begin(), key.end(), key_block.begin());
    }

    Bytes inner;
    inner.reserve(64u + data.size());
    for (Byte value : key_block) {
        inner.push_back(static_cast<Byte>(value ^ 0x36u));
    }
    inner.insert(inner.end(), data.begin(), data.end());
    const Digest256 inner_hash = sha256(inner);

    Bytes outer;
    outer.reserve(96u);
    for (Byte value : key_block) {
        outer.push_back(static_cast<Byte>(value ^ 0x5cu));
    }
    outer.insert(outer.end(), inner_hash.bytes.begin(), inner_hash.bytes.end());
    const Digest256 outer_hash = sha256(outer);
    return Bytes(outer_hash.bytes.begin(), outer_hash.bytes.end());
}

bool constant_time_equal(ByteView lhs, ByteView rhs) noexcept {
    if (lhs.size() != rhs.size()) {
        return false;
    }
    Byte difference = 0;
    for (std::size_t i = 0; i < lhs.size(); ++i) {
        difference = static_cast<Byte>(difference | (lhs[i] ^ rhs[i]));
    }
    return difference == 0;
}

Bytes to_bytes(std::string_view value) {
    return Bytes(value.begin(), value.end());
}

bool PeerIdentity::operator==(const PeerIdentity& other) const noexcept {
    return lowercase_host(host) == lowercase_host(other.host) &&
           certificate_sha256 == other.certificate_sha256;
}

// ---------------------------------------------------------------------------
// Pre-authentication chain
// ---------------------------------------------------------------------------

PreauthIntegrityChain::PreauthIntegrityChain() = default;

void PreauthIntegrityChain::reset() {
    if (!messages_.empty()) {
        raise(ErrorCode::ChainResetNotAllowed,
              "pre-authentication chain cannot be reset after hashing started");
    }
    digest_ = Digest512{};
}

void PreauthIntegrityChain::append(MessageKind kind, ByteView raw_message) {
    switch (kind) {
        case MessageKind::NegotiateRequest:
        case MessageKind::NegotiateResponse:
        case MessageKind::SessionSetupRequest:
        case MessageKind::SessionSetupResponse:
            break;
        default:
            raise(ErrorCode::InvalidArgument, "message kind is not pre-authentication relevant");
    }
    if (raw_message.empty()) {
        raise(ErrorCode::EmptyMessage, "refusing to hash an empty SMB message");
    }

    // Push the immutable copy first: allocation failure cannot leave the
    // digest advanced without a corresponding transcript entry.
    Bytes copy(raw_message.begin(), raw_message.end());
    Bytes material;
    material.reserve(digest_.bytes.size() + copy.size());
    material.insert(material.end(), digest_.bytes.begin(), digest_.bytes.end());
    material.insert(material.end(), copy.begin(), copy.end());

    messages_.push_back(std::move(copy));
    digest_ = sha512(material);
}

// ---------------------------------------------------------------------------
// Channel negotiation
// ---------------------------------------------------------------------------

ChannelNegotiation::ChannelNegotiation(ChannelOptions options)
    : options_(std::move(options)) {}

bool ChannelNegotiation::complete() const noexcept {
    return !failed_ && stage_ == Stage::Complete;
}

bool ChannelNegotiation::failed() const noexcept {
    return failed_;
}

bool ChannelNegotiation::finished() const noexcept {
    return finished_;
}

const Digest512& ChannelNegotiation::current_hash() const noexcept {
    return chain_.digest();
}

const SecurityParameters& ChannelNegotiation::security() const noexcept {
    return security_;
}

void ChannelNegotiation::check_transport_message(MessageKind kind, ByteView raw) const {
    if (raw.size() < kSmb2HeaderSize) {
        raise(ErrorCode::MessageTruncated, "SMB2 message is shorter than its header");
    }
    if (!(raw[0] == 0xfeu && raw[1] == 'S' && raw[2] == 'M' && raw[3] == 'B')) {
        raise(ErrorCode::MessageNotSmb2, "message does not start with an SMB2 header");
    }
    if (read_le16(raw, 4) != kSmb2HeaderSize) {
        raise(ErrorCode::MessageNotSmb2, "invalid SMB2 header structure size");
    }
    if (read_le32(raw, 20) != 0u) {
        raise(ErrorCode::CompoundMessageUnsupported,
              "compound SMB2 messages are not valid pre-authentication inputs");
    }

    const std::uint16_t command = read_le16(raw, 12);
    const std::size_t body_size = raw.size() - kSmb2HeaderSize;

    switch (kind) {
        case MessageKind::NegotiateRequest:
            if (command != kSmb2CommandNegotiate) {
                raise(ErrorCode::UnexpectedCommand, "expected NEGOTIATE request");
            }
            if (body_size < 36u) {
                raise(ErrorCode::MessageTruncated, "truncated NEGOTIATE request");
            }
            if (read_le16(raw, kSmb2HeaderSize) < 36u) {
                raise(ErrorCode::MessageTruncated, "invalid NEGOTIATE request structure size");
            }
            break;
        case MessageKind::NegotiateResponse:
            if (command != kSmb2CommandNegotiate) {
                raise(ErrorCode::UnexpectedCommand, "expected NEGOTIATE response");
            }
            if (body_size < 64u) {
                raise(ErrorCode::MessageTruncated, "truncated NEGOTIATE response");
            }
            if (read_le16(raw, kSmb2HeaderSize) < 64u) {
                raise(ErrorCode::MessageTruncated, "invalid NEGOTIATE response structure size");
            }
            if (header_status(raw) != kStatusSuccess) {
                raise(ErrorCode::SessionSetupFailed, "NEGOTIATE failed");
            }
            break;
        case MessageKind::SessionSetupRequest:
            if (command != kSmb2CommandSessionSetup) {
                raise(ErrorCode::UnexpectedCommand, "expected SESSION_SETUP request");
            }
            if (body_size < 24u) {
                raise(ErrorCode::MessageTruncated, "truncated SESSION_SETUP request");
            }
            {
                const std::uint16_t security_offset =
                    read_le16(raw, kSmb2HeaderSize + 12u);
                const std::uint16_t security_length =
                    read_le16(raw, kSmb2HeaderSize + 14u);
                if (security_length != 0u) {
                    if (security_offset < kSmb2HeaderSize ||
                        security_offset > raw.size() ||
                        security_length > raw.size() - security_offset) {
                        raise(ErrorCode::InvalidSecurityBuffer,
                              "SESSION_SETUP security buffer is outside the message");
                    }
                }
            }
            break;
        case MessageKind::SessionSetupResponse:
            if (command != kSmb2CommandSessionSetup) {
                raise(ErrorCode::UnexpectedCommand, "expected SESSION_SETUP response");
            }
            if (body_size < 8u) {
                raise(ErrorCode::MessageTruncated, "truncated SESSION_SETUP response");
            }
            {
                const std::uint16_t security_offset =
                    read_le16(raw, kSmb2HeaderSize + 4u);
                const std::uint16_t security_length =
                    read_le16(raw, kSmb2HeaderSize + 6u);
                if (security_length != 0u) {
                    if (security_offset < kSmb2HeaderSize ||
                        security_offset > raw.size() ||
                        security_length > raw.size() - security_offset) {
                        raise(ErrorCode::InvalidSecurityBuffer,
                              "SESSION_SETUP response security buffer is outside the message");
                    }
                }
            }
            if (header_status(raw) != kStatusSuccess &&
                header_status(raw) != kStatusMoreProcessingRequired) {
                raise(ErrorCode::SessionSetupFailed, "SESSION_SETUP failed");
            }
            break;
    }
}

void ChannelNegotiation::parse_negotiate_request(ByteView raw) {
    const std::size_t base = kSmb2HeaderSize;
    const std::uint16_t dialect_count = read_le16(raw, base + 2u);
    if (dialect_count == 0u) {
        raise(ErrorCode::UnsupportedDialect, "NEGOTIATE request offers no dialects");
    }

    std::vector<Dialect> candidates_36;
    std::vector<Dialect> candidates_44;
    if (base + 36u <= raw.size()) {
        candidates_36 = parse_dialect_list(raw, base + 36u, dialect_count);
    }
    if (base + 44u <= raw.size()) {
        candidates_44 = parse_dialect_list(raw, base + 44u, dialect_count);
    }

    std::uint32_t context_offset = 0;
    std::uint16_t context_count = 0;
    if (raw.size() >= base + 44u) {
        context_offset = read_le32(raw, base + 36u);
        context_count = read_le16(raw, base + 40u);
    }
    // Older clients put their dialect list directly at body offset 36.  If
    // the SMB 3.1.1 context fields do not point at a plausible location,
    // interpret the bytes as the legacy layout instead of misreading dialects
    // as a context count.
    if (context_count != 0u &&
        (context_offset < kSmb2HeaderSize || context_offset > raw.size())) {
        context_offset = 0;
        context_count = 0;
    }

    if (context_count != 0u) {
        if (candidates_44.empty()) {
            raise(ErrorCode::MessageTruncated,
                  "NEGOTIATE request dialect list or context is truncated");
        }
        offered_dialects_ = std::move(candidates_44);
    } else if (!candidates_44.empty() &&
               (candidates_36.empty() ||
                contains_dialect(candidates_44, Dialect::Smb311))) {
        offered_dialects_ = std::move(candidates_44);
    } else if (!candidates_36.empty()) {
        offered_dialects_ = std::move(candidates_36);
    } else if (!candidates_44.empty()) {
        offered_dialects_ = std::move(candidates_44);
    } else {
        raise(ErrorCode::UnsupportedDialect,
              "NEGOTIATE request contains an unknown dialect list");
    }

    if (context_count != 0u) {
        const ParsedContexts contexts =
            parse_negotiate_contexts(raw, context_offset, context_count,
                                     options_.reject_unknown_algorithms,
                                     options_.reject_unknown_contexts);
        saw_preauth_context_ = contexts.saw_preauth;
        offered_ciphers_ = contexts.ciphers;
        offered_preauth_algorithms_ = contexts.preauth_algorithms;
    }

    if (contains_dialect(offered_dialects_, Dialect::Smb311) &&
        options_.require_smb311_preauth_context && !saw_preauth_context_) {
        raise(ErrorCode::MissingPreauthIntegrityContext,
              "SMB 3.1.1 NEGOTIATE request has no pre-authentication context");
    }
}

void ChannelNegotiation::parse_negotiate_response(ByteView raw) {
    const std::size_t base = kSmb2HeaderSize;
    const std::uint16_t dialect_value = read_le16(raw, base + 4u);
    if (!is_known_dialect(dialect_value)) {
        raise(ErrorCode::UnsupportedDialect, "NEGOTIATE response selected unknown dialect");
    }
    security_.dialect = static_cast<Dialect>(dialect_value);
    dialect_offered_ = contains_dialect(offered_dialects_, security_.dialect);

    std::copy_n(raw.begin() + static_cast<std::ptrdiff_t>(base + 8u),
                security_.server_guid.bytes.size(),
                security_.server_guid.bytes.begin());

    const std::uint16_t context_count = read_le16(raw, base + 6u);
    const std::uint16_t security_offset = read_le16(raw, base + 56u);
    const std::uint16_t security_length = read_le16(raw, base + 58u);
    if (security_length != 0u) {
        if (security_offset < kSmb2HeaderSize || security_offset > raw.size() ||
            security_length > raw.size() - security_offset) {
            raise(ErrorCode::InvalidSecurityBuffer,
                  "NEGOTIATE response security buffer is outside the message");
        }
    }

    ParsedContexts contexts;
    if (context_count != 0u) {
        const std::uint32_t context_offset = read_le32(raw, base + 60u);
        contexts = parse_negotiate_contexts(raw, context_offset, context_count,
                                            options_.reject_unknown_algorithms,
                                            options_.reject_unknown_contexts);
    }

    if (security_.dialect == Dialect::Smb311 &&
        options_.require_smb311_preauth_context && !contexts.saw_preauth) {
        raise(ErrorCode::MissingPreauthIntegrityContext,
              "SMB 3.1.1 NEGOTIATE response has no pre-authentication context");
    }

    security_.preauth_hash = PreauthHashAlgorithm::Sha512;
    if (!contexts.preauth_algorithms.empty()) {
        if (offered_preauth_algorithms_.empty() ||
            std::find(offered_preauth_algorithms_.begin(),
                      offered_preauth_algorithms_.end(),
                      contexts.preauth_algorithms.front()) ==
                offered_preauth_algorithms_.end()) {
            raise(ErrorCode::PreauthAlgorithmMismatch,
                  "server selected a pre-authentication algorithm that was not offered");
        }
        security_.preauth_hash = contexts.preauth_algorithms.front();
    }

    security_.cipher = Cipher::None;
    if (contexts.saw_encryption) {
        if (offered_ciphers_.empty()) {
            raise(ErrorCode::CipherMismatch,
                  "server selected an encryption cipher that was not offered");
        }
        for (Cipher cipher : contexts.ciphers) {
            if (std::find(offered_ciphers_.begin(), offered_ciphers_.end(), cipher) ==
                offered_ciphers_.end()) {
                raise(ErrorCode::CipherMismatch,
                      "server negotiated an encryption cipher that was not offered");
            }
        }
        if (options_.selected_cipher.has_value()) {
            if (std::find(contexts.ciphers.begin(), contexts.ciphers.end(),
                          *options_.selected_cipher) == contexts.ciphers.end()) {
                raise(ErrorCode::UnknownCipherAlgorithm,
                      "configured cipher was not negotiated by the server");
            }
            security_.cipher = *options_.selected_cipher;
        } else {
            security_.cipher = contexts.ciphers.front();
        }
    } else if (options_.selected_cipher.has_value() &&
               *options_.selected_cipher != Cipher::None) {
        raise(ErrorCode::UnknownCipherAlgorithm,
              "configured cipher was not offered by the server");
    }

    if (options_.selected_signing.has_value()) {
        security_.signing = *options_.selected_signing;
    } else {
        security_.signing = security_.dialect == Dialect::Smb311
                                ? SigningAlgorithm::AesCmac
                                : SigningAlgorithm::HmacSha256;
    }
}

void ChannelNegotiation::validate_and_update(MessageKind kind, ByteView raw) {
    if (failed_) {
        raise(failure_reason_, "negotiation attempt has already failed");
    }
    if (finished_) {
        raise(ErrorCode::NegotiationAlreadyFinished,
              "negotiation transcript is already finished");
    }

    const bool expected =
        (stage_ == Stage::NegotiateRequest && kind == MessageKind::NegotiateRequest) ||
        (stage_ == Stage::NegotiateResponse && kind == MessageKind::NegotiateResponse) ||
        (stage_ == Stage::SessionSetupRequest && kind == MessageKind::SessionSetupRequest) ||
        (stage_ == Stage::SessionSetupResponse && kind == MessageKind::SessionSetupResponse);
    if (!expected) {
        raise(ErrorCode::UnexpectedMessage,
              "pre-authentication message arrived out of order");
    }

    check_transport_message(kind, raw);

    switch (kind) {
        case MessageKind::NegotiateRequest:
            parse_negotiate_request(raw);
            break;
        case MessageKind::NegotiateResponse:
            parse_negotiate_response(raw);
            break;
        case MessageKind::SessionSetupRequest:
            break;
        case MessageKind::SessionSetupResponse: {
            const std::uint32_t status = header_status(raw);
            if (status == kStatusMoreProcessingRequired) {
                completed_session_setup_ = false;
                final_session_setup_success_ = false;
            } else if (status == kStatusSuccess) {
                completed_session_setup_ = true;
                final_session_setup_success_ = true;
            }
            break;
        }
    }

    chain_.append(kind, raw);

    switch (kind) {
        case MessageKind::NegotiateRequest:
            stage_ = Stage::NegotiateResponse;
            break;
        case MessageKind::NegotiateResponse:
            stage_ = Stage::SessionSetupRequest;
            break;
        case MessageKind::SessionSetupRequest:
            stage_ = Stage::SessionSetupResponse;
            break;
        case MessageKind::SessionSetupResponse:
            stage_ = completed_session_setup_ ? Stage::Complete : Stage::SessionSetupRequest;
            break;
    }
}

void ChannelNegotiation::append(MessageKind kind, ByteView raw_message) {
    validate_and_update(kind, raw_message);
}

ChannelTranscript ChannelNegotiation::finish() {
    if (failed_) {
        raise(failure_reason_, "cannot finish a failed negotiation attempt");
    }
    if (!completed_session_setup_ ||
        (options_.require_successful_session_setup && !final_session_setup_success_) ||
        stage_ != Stage::Complete) {
        raise(ErrorCode::NegotiationIncomplete,
              "NEGOTIATE and a successful SESSION_SETUP are required");
    }
    if (finished_) {
        raise(ErrorCode::NegotiationAlreadyFinished,
              "negotiation transcript is already finished");
    }

    ChannelTranscript transcript;
    transcript.channel_id = options_.channel_id;
    transcript.security = security_;
    transcript.peer = options_.peer;
    transcript.preauth_hash = chain_.digest();
    transcript.dialect_offered = dialect_offered_;
    transcript.message_count = chain_.message_count();
    finished_ = true;
    return transcript;
}

void ChannelNegotiation::fail(ErrorCode reason) {
    if (finished_) {
        raise(ErrorCode::NegotiationAlreadyFinished,
              "cannot fail a transcript that has already been published");
    }
    failed_ = true;
    failure_reason_ = reason == ErrorCode::None ? ErrorCode::AsyncFailure : reason;
    stage_ = Stage::Failed;
    completed_session_setup_ = false;
    final_session_setup_success_ = false;
}

// ---------------------------------------------------------------------------
// Session identity and channel proof
// ---------------------------------------------------------------------------

namespace {

Bytes canonical_channel_bytes(const ChannelTranscript& channel) {
    static constexpr std::string_view domain = "SMB311-PREAUTH-CHANNEL-V1";
    Bytes out(domain.begin(), domain.end());
    append_be64(out, channel.channel_id);
    append_be16(out, static_cast<std::uint16_t>(channel.security.dialect));
    append_be16(out, static_cast<std::uint16_t>(channel.security.cipher));
    append_be16(out, static_cast<std::uint16_t>(channel.security.signing));
    append_be16(out, static_cast<std::uint16_t>(channel.security.preauth_hash));
    out.insert(out.end(), channel.security.server_guid.bytes.begin(),
               channel.security.server_guid.bytes.end());
    out.push_back(channel.dialect_offered ? 1u : 0u);

    append_be32(out, static_cast<std::uint32_t>(channel.peer.certificate_sha256.size()));
    out.insert(out.end(), channel.peer.certificate_sha256.begin(),
               channel.peer.certificate_sha256.end());
    const std::string host = lowercase_host(channel.peer.host);
    append_be32(out, static_cast<std::uint32_t>(host.size()));
    out.insert(out.end(), host.begin(), host.end());

    out.insert(out.end(), channel.preauth_hash.bytes.begin(),
               channel.preauth_hash.bytes.end());
    append_be64(out, static_cast<std::uint64_t>(channel.message_count));
    return out;
}

Digest256 identity_binding(const SecurityParameters& security,
                           const PeerIdentity& peer,
                           const Digest512& preauth_hash,
                           const Digest256& key_commitment_digest) {
    static constexpr std::string_view domain = "SMB311-SESSION-IDENTITY-V1";
    Bytes out(domain.begin(), domain.end());
    append_be16(out, static_cast<std::uint16_t>(security.dialect));
    append_be16(out, static_cast<std::uint16_t>(security.cipher));
    append_be16(out, static_cast<std::uint16_t>(security.signing));
    append_be16(out, static_cast<std::uint16_t>(security.preauth_hash));
    out.insert(out.end(), security.server_guid.bytes.begin(),
               security.server_guid.bytes.end());

    append_be32(out, static_cast<std::uint32_t>(peer.certificate_sha256.size()));
    out.insert(out.end(), peer.certificate_sha256.begin(),
               peer.certificate_sha256.end());
    const std::string host = lowercase_host(peer.host);
    append_be32(out, static_cast<std::uint32_t>(host.size()));
    out.insert(out.end(), host.begin(), host.end());

    out.insert(out.end(), preauth_hash.bytes.begin(), preauth_hash.bytes.end());
    out.insert(out.end(), key_commitment_digest.bytes.begin(),
               key_commitment_digest.bytes.end());
    return sha256(out);
}

}  // namespace

SessionIdentity::SessionIdentity(SecurityParameters security, PeerIdentity peer,
                                 Digest512 hash, ByteView authenticated_session_key)
    : security_(std::move(security)),
      peer_(std::move(peer)),
      preauth_hash_(hash),
      key_commitment_(key_commitment(authenticated_session_key)),
      binding_(identity_binding(security_, peer_, preauth_hash_, key_commitment_)) {}

Bytes compute_preauth_proof(ByteView authenticated_session_key,
                            const ChannelTranscript& channel) {
    if (authenticated_session_key.empty()) {
        raise(ErrorCode::InvalidArgument,
              "authenticated session key must not be empty");
    }
    return hmac_sha256(authenticated_session_key, canonical_channel_bytes(channel));
}

bool verify_preauth_proof(ByteView authenticated_session_key,
                          const ChannelTranscript& channel,
                          ByteView proof) noexcept {
    if (authenticated_session_key.empty() || proof.size() != kProofSize) {
        return false;
    }
    try {
        const Bytes expected = compute_preauth_proof(authenticated_session_key, channel);
        return constant_time_equal(expected, proof);
    } catch (...) {
        return false;
    }
}

// ---------------------------------------------------------------------------
// Session negotiation
// ---------------------------------------------------------------------------

SessionNegotiation::SessionNegotiation(SessionIdentity identity, Bytes session_key,
                                       std::vector<BoundChannel> channels)
    : identity_(std::move(identity)),
      session_key_(std::move(session_key)),
      channels_(std::move(channels)) {
    for (const BoundChannel& channel : channels_) {
        channel_ids_.insert(channel.channel_id);
    }
}

SessionNegotiation::~SessionNegotiation() {
    std::fill(session_key_.begin(), session_key_.end(), static_cast<Byte>(0));
}

SessionNegotiation SessionNegotiation::establish(const ChannelTranscript& channel,
                                                  ByteView authenticated_session_key,
                                                  ByteView channel_proof) {
    if (authenticated_session_key.empty()) {
        raise(ErrorCode::InvalidArgument,
              "authenticated session key must not be empty");
    }
    if (channel_proof.size() != kProofSize) {
        raise(ErrorCode::ProofMismatch,
              "channel proof has an invalid length");
    }
    if (!channel.dialect_offered) {
        raise(ErrorCode::DialectNotOffered,
              "server selected a dialect that the client did not offer");
    }
    if (channel.security.dialect != Dialect::Smb311) {
        raise(ErrorCode::UnsupportedDialect,
              "initial SMB session must negotiate SMB 3.1.1");
    }
    if (channel.security.preauth_hash != PreauthHashAlgorithm::Sha512) {
        raise(ErrorCode::PreauthAlgorithmMismatch,
              "SMB 3.1.1 requires SHA-512 pre-authentication integrity");
    }
    if (channel.message_count == 0u) {
        raise(ErrorCode::PreauthHashMismatch,
              "channel has no hashed pre-authentication messages");
    }
    if (!verify_preauth_proof(authenticated_session_key, channel, channel_proof)) {
        raise(ErrorCode::ProofMismatch,
              "channel proof does not authenticate the complete pre-authentication chain");
    }

    SessionIdentity identity(channel.security, channel.peer, channel.preauth_hash,
                             authenticated_session_key);
    BoundChannel bound;
    bound.channel_id = channel.channel_id;
    bound.security = channel.security;
    bound.peer = channel.peer;
    bound.preauth_hash = channel.preauth_hash;
    bound.session_binding = identity.binding_digest();

    std::vector<BoundChannel> channels;
    channels.push_back(std::move(bound));
    return SessionNegotiation(std::move(identity),
                              Bytes(authenticated_session_key.begin(),
                                    authenticated_session_key.end()),
                              std::move(channels));
}

std::optional<SessionNegotiation> SessionNegotiation::try_establish(
    const ChannelTranscript& channel, ByteView authenticated_session_key,
    ByteView channel_proof, ErrorCode* error) noexcept {
    try {
        if (error != nullptr) {
            *error = ErrorCode::None;
        }
        return establish(channel, authenticated_session_key, channel_proof);
    } catch (const Error& ex) {
        if (error != nullptr) {
            *error = ex.code();
        }
    } catch (...) {
        if (error != nullptr) {
            *error = ErrorCode::InternalError;
        }
    }
    return std::nullopt;
}

ErrorCode SessionNegotiation::validate_channel(const ChannelTranscript& channel,
                                               bool reconnect) const noexcept {
    (void)reconnect;
    if (dialect_rank(channel.security.dialect) < dialect_rank(identity_.dialect())) {
        return ErrorCode::DialectDowngrade;
    }
    if (!channel.dialect_offered) {
        return ErrorCode::DialectNotOffered;
    }
    if (channel.security.dialect != identity_.dialect()) {
        return ErrorCode::DialectMismatch;
    }
    if (!(channel.security.server_guid == identity_.server_guid())) {
        return ErrorCode::ServerGuidMismatch;
    }
    if (channel.security.cipher != identity_.cipher()) {
        return ErrorCode::CipherMismatch;
    }
    if (channel.security.signing != identity_.signing()) {
        return ErrorCode::SigningMismatch;
    }
    if (channel.security.preauth_hash != identity_.preauth_algorithm()) {
        return ErrorCode::PreauthAlgorithmMismatch;
    }
    if (!(channel.peer == identity_.peer())) {
        return ErrorCode::PeerIdentityMismatch;
    }
    return ErrorCode::None;
}

BoundChannel SessionNegotiation::join(const ChannelTranscript& channel,
                                      ByteView authenticated_session_key,
                                      ByteView channel_proof) {
    if (authenticated_session_key.empty()) {
        raise(ErrorCode::InvalidArgument,
              "authenticated session key must not be empty");
    }
    // A caller-provided key is not another authentication domain: it must be
    // the exact key established for this session.
    if (!constant_time_equal(authenticated_session_key, session_key_)) {
        raise(ErrorCode::ProofMismatch,
              "supplied key is not the session's authenticated key");
    }
    return join(channel, channel_proof);
}

BoundChannel SessionNegotiation::join(const ChannelTranscript& channel,
                                      ByteView channel_proof) {
    if (channel_ids_.contains(channel.channel_id)) {
        raise(ErrorCode::DuplicateChannel,
              "channel id is already bound to this session");
    }
    const ErrorCode mismatch = validate_channel(channel, false);
    if (mismatch != ErrorCode::None) {
        raise(mismatch, "auxiliary channel does not inherit session security");
    }
    if (!verify_preauth_proof(session_key_, channel, channel_proof)) {
        raise(ErrorCode::ProofMismatch,
              "auxiliary channel proof does not authenticate its pre-authentication chain");
    }

    BoundChannel bound;
    bound.channel_id = channel.channel_id;
    bound.security = channel.security;
    bound.peer = channel.peer;
    bound.preauth_hash = channel.preauth_hash;
    bound.session_binding = identity_.binding_digest();

    // All checks passed; publish atomically.
    channels_.push_back(bound);
    channel_ids_.insert(bound.channel_id);
    return bound;
}

std::optional<ErrorCode> SessionNegotiation::try_join(
    const ChannelTranscript& channel, ByteView channel_proof) noexcept {
    try {
        (void)join(channel, channel_proof);
        return std::nullopt;
    } catch (const Error& ex) {
        return ex.code();
    } catch (...) {
        return ErrorCode::InternalError;
    }
}

std::optional<ErrorCode> SessionNegotiation::try_join(
    const ChannelTranscript& channel, ByteView authenticated_session_key,
    ByteView channel_proof) noexcept {
    try {
        (void)join(channel, authenticated_session_key, channel_proof);
        return std::nullopt;
    } catch (const Error& ex) {
        return ex.code();
    } catch (...) {
        return ErrorCode::InternalError;
    }
}

SessionNegotiation SessionNegotiation::reconnect(
    const SessionIdentity& original_identity, const ChannelTranscript& new_channel,
    ByteView authenticated_session_key, ByteView channel_proof) {
    if (authenticated_session_key.empty()) {
        raise(ErrorCode::InvalidArgument,
              "authenticated session key must not be empty");
    }
    if (channel_proof.size() != kProofSize) {
        raise(ErrorCode::ProofMismatch,
              "channel proof has an invalid length");
    }
    // Bind the reconnect key back to the original authenticated session even
    // though SessionIdentity deliberately does not carry the key itself.
    if (!(key_commitment(authenticated_session_key) ==
          original_identity.authenticated_key_commitment())) {
        raise(ErrorCode::ProofMismatch,
              "reconnect key is not the original session's authenticated key");
    }

    SessionNegotiation temporary(original_identity,
                                 Bytes(authenticated_session_key.begin(),
                                       authenticated_session_key.end()),
                                 {});
    const ErrorCode mismatch = temporary.validate_channel(new_channel, true);
    if (mismatch != ErrorCode::None) {
        raise(mismatch, "reconnect channel does not match the immutable session identity");
    }
    if (!verify_preauth_proof(authenticated_session_key, new_channel, channel_proof)) {
        raise(ErrorCode::ProofMismatch,
              "reconnect channel proof does not authenticate its pre-authentication chain");
    }

    BoundChannel bound;
    bound.channel_id = new_channel.channel_id;
    bound.security = original_identity.security();
    bound.peer = original_identity.peer();
    bound.preauth_hash = new_channel.preauth_hash;
    bound.session_binding = original_identity.binding_digest();

    std::vector<BoundChannel> channels;
    channels.push_back(std::move(bound));
    return SessionNegotiation(original_identity,
                              Bytes(authenticated_session_key.begin(),
                                    authenticated_session_key.end()),
                              std::move(channels));
}

std::optional<SessionNegotiation> SessionNegotiation::try_reconnect(
    const SessionIdentity& original_identity, const ChannelTranscript& new_channel,
    ByteView authenticated_session_key, ByteView channel_proof,
    ErrorCode* error) noexcept {
    try {
        if (error != nullptr) {
            *error = ErrorCode::None;
        }
        return reconnect(original_identity, new_channel, authenticated_session_key,
                         channel_proof);
    } catch (const Error& ex) {
        if (error != nullptr) {
            *error = ex.code();
        }
    } catch (...) {
        if (error != nullptr) {
            *error = ErrorCode::InternalError;
        }
    }
    return std::nullopt;
}

// ---------------------------------------------------------------------------
// Asynchronous attempt coordinator
// ---------------------------------------------------------------------------

AsyncSessionNegotiation::AsyncSessionNegotiation(SessionNegotiation session) noexcept
    : session_(std::move(session)) {}

AsyncSessionNegotiation AsyncSessionNegotiation::establish(
    const ChannelTranscript& channel, ByteView authenticated_session_key,
    ByteView channel_proof) {
    return AsyncSessionNegotiation(
        SessionNegotiation::establish(channel, authenticated_session_key, channel_proof));
}

AsyncSessionNegotiation::AttemptId AsyncSessionNegotiation::begin_channel(
    ChannelOptions options) {
    std::lock_guard<std::mutex> lock(mutex_);
    const std::uint64_t id = next_attempt_id_++;
    attempts_.emplace(id, AttemptState{ChannelNegotiation(std::move(options)), false, false});
    return AttemptId{0, id};
}

AsyncSessionNegotiation::AttemptState* AsyncSessionNegotiation::find(
    AttemptId attempt, ErrorCode& error) noexcept {
    if (attempt.epoch != 0 || retired_attempts_.contains(attempt.id)) {
        error = ErrorCode::StaleAttempt;
        return nullptr;
    }
    const auto iter = attempts_.find(attempt.id);
    if (iter == attempts_.end()) {
        error = ErrorCode::StaleAttempt;
        return nullptr;
    }
    if (iter->second.failed) {
        error = ErrorCode::AttemptFailed;
        return nullptr;
    }
    if (iter->second.completed) {
        error = ErrorCode::StaleAttempt;
        return nullptr;
    }
    return &iter->second;
}

void AsyncSessionNegotiation::append(AttemptId attempt, MessageKind kind,
                                     ByteView raw_message) {
    std::lock_guard<std::mutex> lock(mutex_);
    ErrorCode error = ErrorCode::None;
    AttemptState* state = find(attempt, error);
    if (state == nullptr) {
        raise(error, "asynchronous channel attempt is not active");
    }
    try {
        state->negotiation.append(kind, raw_message);
    } catch (...) {
        state->failed = true;
        retired_attempts_.insert(attempt.id);
        throw;
    }
}

void AsyncSessionNegotiation::fail(AttemptId attempt, ErrorCode reason) {
    std::lock_guard<std::mutex> lock(mutex_);
    ErrorCode error = ErrorCode::None;
    AttemptState* state = find(attempt, error);
    if (state == nullptr) {
        raise(error, "asynchronous channel attempt is not active");
    }
    state->failed = true;
    state->negotiation.fail(reason);
    retired_attempts_.insert(attempt.id);
}

bool AsyncSessionNegotiation::complete(AttemptId attempt, ByteView channel_proof,
                                       ErrorCode* error) {
    std::lock_guard<std::mutex> lock(mutex_);
    ErrorCode local_error = ErrorCode::None;
    AttemptState* state = find(attempt, local_error);
    if (state == nullptr) {
        if (error != nullptr) {
            *error = local_error;
        }
        return false;
    }
    try {
        const ChannelTranscript transcript = state->negotiation.finish();
        (void)session_.join(transcript, channel_proof);
        state->completed = true;
        retired_attempts_.insert(attempt.id);
        if (error != nullptr) {
            *error = ErrorCode::None;
        }
        return true;
    } catch (const Error& ex) {
        state->failed = true;
        retired_attempts_.insert(attempt.id);
        if (error != nullptr) {
            *error = ex.code();
        }
    } catch (...) {
        state->failed = true;
        retired_attempts_.insert(attempt.id);
        if (error != nullptr) {
            *error = ErrorCode::InternalError;
        }
    }
    return false;
}

bool AsyncSessionNegotiation::complete(AttemptId attempt,
                                       ByteView authenticated_session_key,
                                       ByteView channel_proof, ErrorCode* error) {
    std::lock_guard<std::mutex> lock(mutex_);
    ErrorCode local_error = ErrorCode::None;
    AttemptState* state = find(attempt, local_error);
    if (state == nullptr) {
        if (error != nullptr) {
            *error = local_error;
        }
        return false;
    }
    try {
        const ChannelTranscript transcript = state->negotiation.finish();
        (void)session_.join(transcript, authenticated_session_key, channel_proof);
        state->completed = true;
        retired_attempts_.insert(attempt.id);
        if (error != nullptr) {
            *error = ErrorCode::None;
        }
        return true;
    } catch (const Error& ex) {
        state->failed = true;
        retired_attempts_.insert(attempt.id);
        if (error != nullptr) {
            *error = ex.code();
        }
    } catch (...) {
        state->failed = true;
        retired_attempts_.insert(attempt.id);
        if (error != nullptr) {
            *error = ErrorCode::InternalError;
        }
    }
    return false;
}

SessionNegotiation AsyncSessionNegotiation::take_session() {
    std::lock_guard<std::mutex> lock(mutex_);
    return std::move(session_);
}

}  // namespace netsec
