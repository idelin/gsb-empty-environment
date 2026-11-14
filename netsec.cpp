#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cerrno>
#include <cstring>
#include <fstream>
#include <iomanip>
#include <limits>
#include <sstream>
#include <stdexcept>
#include <system_error>

#if defined(__unix__) || defined(__APPLE__)
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
#endif

namespace {

using netsec::Octets;

constexpr std::uint32_t rotateLeft(std::uint32_t value, unsigned bits) {
    return (value << bits) | (value >> (32u - bits));
}

constexpr std::uint32_t rotateRight(std::uint32_t value, unsigned bits) {
    return (value >> bits) | (value << (32u - bits));
}

Octets sha1(const Octets& input) {
    Octets data = input;
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(data.size()) * 8u;

    data.push_back(0x80u);
    while ((data.size() % 64u) != 56u) {
        data.push_back(0u);
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        data.push_back(static_cast<std::uint8_t>(bit_length >> shift));
    }

    std::uint32_t h0 = 0x67452301u;
    std::uint32_t h1 = 0xefcdab89u;
    std::uint32_t h2 = 0x98badcfeu;
    std::uint32_t h3 = 0x10325476u;
    std::uint32_t h4 = 0xc3d2e1f0u;

    for (std::size_t offset = 0; offset < data.size(); offset += 64) {
        std::array<std::uint32_t, 80> words{};
        for (std::size_t index = 0; index < 16; ++index) {
            const std::size_t position = offset + index * 4;
            words[index] =
                (static_cast<std::uint32_t>(data[position]) << 24u) |
                (static_cast<std::uint32_t>(data[position + 1]) << 16u) |
                (static_cast<std::uint32_t>(data[position + 2]) << 8u) |
                static_cast<std::uint32_t>(data[position + 3]);
        }
        for (std::size_t index = 16; index < 80; ++index) {
            words[index] = rotateLeft(
                words[index - 3] ^ words[index - 8] ^ words[index - 14] ^
                    words[index - 16],
                1);
        }

        std::uint32_t a = h0;
        std::uint32_t b = h1;
        std::uint32_t c = h2;
        std::uint32_t d = h3;
        std::uint32_t e = h4;

        for (std::size_t index = 0; index < 80; ++index) {
            std::uint32_t function = 0;
            std::uint32_t constant = 0;
            if (index < 20) {
                function = (b & c) | ((~b) & d);
                constant = 0x5a827999u;
            } else if (index < 40) {
                function = b ^ c ^ d;
                constant = 0x6ed9eba1u;
            } else if (index < 60) {
                function = (b & c) | (b & d) | (c & d);
                constant = 0x8f1bbcdcu;
            } else {
                function = b ^ c ^ d;
                constant = 0xca62c1d6u;
            }
            const std::uint32_t temporary =
                rotateLeft(a, 5) + function + e + constant + words[index];
            e = d;
            d = c;
            c = rotateLeft(b, 30);
            b = a;
            a = temporary;
        }

        h0 += a;
        h1 += b;
        h2 += c;
        h3 += d;
        h4 += e;
    }

    Octets digest;
    digest.reserve(20);
    for (const std::uint32_t value : {h0, h1, h2, h3, h4}) {
        digest.push_back(static_cast<std::uint8_t>(value >> 24u));
        digest.push_back(static_cast<std::uint8_t>(value >> 16u));
        digest.push_back(static_cast<std::uint8_t>(value >> 8u));
        digest.push_back(static_cast<std::uint8_t>(value));
    }
    return digest;
}

Octets md5(const Octets& input) {
    static constexpr std::array<std::uint32_t, 64> constants = {
        0xd76aa478u, 0xe8c7b756u, 0x242070dbu, 0xc1bdceeeu,
        0xf57c0fafu, 0x4787c62au, 0xa8304613u, 0xfd469501u,
        0x698098d8u, 0x8b44f7afu, 0xffff5bb1u, 0x895cd7beu,
        0x6b901122u, 0xfd987193u, 0xa679438eu, 0x49b40821u,
        0xf61e2562u, 0xc040b340u, 0x265e5a51u, 0xe9b6c7aau,
        0xd62f105du, 0x02441453u, 0xd8a1e681u, 0xe7d3fbc8u,
        0x21e1cde6u, 0xc33707d6u, 0xf4d50d87u, 0x455a14edu,
        0xa9e3e905u, 0xfcefa3f8u, 0x676f02d9u, 0x8d2a4c8au,
        0xfffa3942u, 0x8771f681u, 0x6d9d6122u, 0xfde5380cu,
        0xa4beea44u, 0x4bdecfa9u, 0xf6bb4b60u, 0xbebfbc70u,
        0x289b7ec6u, 0xeaa127fau, 0xd4ef3085u, 0x04881d05u,
        0xd9d4d039u, 0xe6db99e5u, 0x1fa27cf8u, 0xc4ac5665u,
        0xf4292244u, 0x432aff97u, 0xab9423a7u, 0xfc93a039u,
        0x655b59c3u, 0x8f0ccc92u, 0xffeff47du, 0x85845dd1u,
        0x6fa87e4fu, 0xfe2ce6e0u, 0xa3014314u, 0x4e0811a1u,
        0xf7537e82u, 0xbd3af235u, 0x2ad7d2bbu, 0xeb86d391u,
    };
    static constexpr std::array<unsigned, 64> shifts = {
        7u,  12u, 17u, 22u, 7u,  12u, 17u, 22u,
        7u,  12u, 17u, 22u, 7u,  12u, 17u, 22u,
        5u,  9u,  14u, 20u, 5u,  9u,  14u, 20u,
        5u,  9u,  14u, 20u, 5u,  9u,  14u, 20u,
        4u,  11u, 16u, 23u, 4u,  11u, 16u, 23u,
        4u,  11u, 16u, 23u, 4u,  11u, 16u, 23u,
        6u,  10u, 15u, 21u, 6u,  10u, 15u, 21u,
        6u,  10u, 15u, 21u, 6u,  10u, 15u, 21u,
    };

    Octets data = input;
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(data.size()) * 8u;
    data.push_back(0x80u);
    while ((data.size() % 64u) != 56u) {
        data.push_back(0u);
    }
    for (int shift = 0; shift < 64; shift += 8) {
        data.push_back(static_cast<std::uint8_t>(bit_length >> shift));
    }

    std::uint32_t a0 = 0x67452301u;
    std::uint32_t b0 = 0xefcdab89u;
    std::uint32_t c0 = 0x98badcfeu;
    std::uint32_t d0 = 0x10325476u;

    for (std::size_t offset = 0; offset < data.size(); offset += 64) {
        std::array<std::uint32_t, 16> words{};
        for (std::size_t index = 0; index < 16; ++index) {
            const std::size_t position = offset + index * 4;
            words[index] =
                static_cast<std::uint32_t>(data[position]) |
                (static_cast<std::uint32_t>(data[position + 1]) << 8u) |
                (static_cast<std::uint32_t>(data[position + 2]) << 16u) |
                (static_cast<std::uint32_t>(data[position + 3]) << 24u);
        }

        std::uint32_t a = a0;
        std::uint32_t b = b0;
        std::uint32_t c = c0;
        std::uint32_t d = d0;
        for (std::size_t index = 0; index < 64; ++index) {
            std::uint32_t function = 0;
            std::size_t word_index = 0;
            if (index < 16) {
                function = (b & c) | ((~b) & d);
                word_index = index;
            } else if (index < 32) {
                function = (d & b) | ((~d) & c);
                word_index = (5u * index + 1u) % 16u;
            } else if (index < 48) {
                function = b ^ c ^ d;
                word_index = (3u * index + 5u) % 16u;
            } else {
                function = c ^ (b | (~d));
                word_index = (7u * index) % 16u;
            }
            const std::uint32_t temporary =
                function + a + constants[index] + words[word_index];
            a = d;
            d = c;
            c = b;
            b += rotateLeft(temporary, shifts[index]);
        }

        a0 += a;
        b0 += b;
        c0 += c;
        d0 += d;
    }

    Octets digest;
    digest.reserve(16);
    for (const std::uint32_t value : {a0, b0, c0, d0}) {
        digest.push_back(static_cast<std::uint8_t>(value));
        digest.push_back(static_cast<std::uint8_t>(value >> 8u));
        digest.push_back(static_cast<std::uint8_t>(value >> 16u));
        digest.push_back(static_cast<std::uint8_t>(value >> 24u));
    }
    return digest;
}

Octets sha256(const Octets& input) {
    static constexpr std::array<std::uint32_t, 64> constants = {
        0x428a2f98u, 0x71374491u, 0xb5c0fbcfu, 0xe9b5dba5u,
        0x3956c25bu, 0x59f111f1u, 0x923f82a4u, 0xab1c5ed5u,
        0xd807aa98u, 0x12835b01u, 0x243185beu, 0x550c7dc3u,
        0x72be5d74u, 0x80deb1feu, 0x9bdc06a7u, 0xc19bf174u,
        0xe49b69c1u, 0xefbe4786u, 0x0fc19dc6u, 0x240ca1ccu,
        0x2de92c6fu, 0x4a7484aau, 0x5cb0a9dcu, 0x76f988dau,
        0x983e5152u, 0xa831c66du, 0xb00327c8u, 0xbf597fc7u,
        0xc6e00bf3u, 0xd5a79147u, 0x06ca6351u, 0x14292967u,
        0x27b70a85u, 0x2e1b2138u, 0x4d2c6dfcu, 0x53380d13u,
        0x650a7354u, 0x766a0abbu, 0x81c2c92eu, 0x92722c85u,
        0xa2bfe8a1u, 0xa81a664bu, 0xc24b8b70u, 0xc76c51a3u,
        0xd192e819u, 0xd6990624u, 0xf40e3585u, 0x106aa070u,
        0x19a4c116u, 0x1e376c08u, 0x2748774cu, 0x34b0bcb5u,
        0x391c0cb3u, 0x4ed8aa4au, 0x5b9cca4fu, 0x682e6ff3u,
        0x748f82eeu, 0x78a5636fu, 0x84c87814u, 0x8cc70208u,
        0x90befffau, 0xa4506cebu, 0xbef9a3f7u, 0xc67178f2u,
    };

    Octets data = input;
    const std::uint64_t bit_length =
        static_cast<std::uint64_t>(data.size()) * 8u;
    data.push_back(0x80u);
    while ((data.size() % 64u) != 56u) {
        data.push_back(0u);
    }
    for (int shift = 56; shift >= 0; shift -= 8) {
        data.push_back(static_cast<std::uint8_t>(bit_length >> shift));
    }

    std::array<std::uint32_t, 8> state = {
        0x6a09e667u,
        0xbb67ae85u,
        0x3c6ef372u,
        0xa54ff53au,
        0x510e527fu,
        0x9b05688cu,
        0x1f83d9abu,
        0x5be0cd19u,
    };

    for (std::size_t offset = 0; offset < data.size(); offset += 64) {
        std::array<std::uint32_t, 64> words{};
        for (std::size_t index = 0; index < 16; ++index) {
            const std::size_t position = offset + index * 4;
            words[index] =
                (static_cast<std::uint32_t>(data[position]) << 24u) |
                (static_cast<std::uint32_t>(data[position + 1]) << 16u) |
                (static_cast<std::uint32_t>(data[position + 2]) << 8u) |
                static_cast<std::uint32_t>(data[position + 3]);
        }
        for (std::size_t index = 16; index < 64; ++index) {
            const std::uint32_t s0 =
                rotateRight(words[index - 15], 7) ^
                rotateRight(words[index - 15], 18) ^
                (words[index - 15] >> 3u);
            const std::uint32_t s1 =
                rotateRight(words[index - 2], 17) ^
                rotateRight(words[index - 2], 19) ^
                (words[index - 2] >> 10u);
            words[index] =
                words[index - 16] + s0 + words[index - 7] + s1;
        }

        std::uint32_t a = state[0];
        std::uint32_t b = state[1];
        std::uint32_t c = state[2];
        std::uint32_t d = state[3];
        std::uint32_t e = state[4];
        std::uint32_t f = state[5];
        std::uint32_t g = state[6];
        std::uint32_t h = state[7];

        for (std::size_t index = 0; index < 64; ++index) {
            const std::uint32_t sum1 =
                rotateRight(e, 6) ^ rotateRight(e, 11) ^ rotateRight(e, 25);
            const std::uint32_t choice = (e & f) ^ ((~e) & g);
            const std::uint32_t temporary1 =
                h + sum1 + choice + constants[index] + words[index];
            const std::uint32_t sum0 =
                rotateRight(a, 2) ^ rotateRight(a, 13) ^ rotateRight(a, 22);
            const std::uint32_t majority = (a & b) ^ (a & c) ^ (b & c);
            const std::uint32_t temporary2 = sum0 + majority;

            h = g;
            g = f;
            f = e;
            e = d + temporary1;
            d = c;
            c = b;
            b = a;
            a = temporary1 + temporary2;
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

    Octets digest;
    digest.reserve(32);
    for (const std::uint32_t value : state) {
        digest.push_back(static_cast<std::uint8_t>(value >> 24u));
        digest.push_back(static_cast<std::uint8_t>(value >> 16u));
        digest.push_back(static_cast<std::uint8_t>(value >> 8u));
        digest.push_back(static_cast<std::uint8_t>(value));
    }
    return digest;
}

Octets digest(netsec::AuthProtocol protocol, const Octets& data) {
    switch (protocol) {
        case netsec::AuthProtocol::HmacMd5:
            return md5(data);
        case netsec::AuthProtocol::HmacSha1:
            return sha1(data);
        case netsec::AuthProtocol::HmacSha256:
            return sha256(data);
        case netsec::AuthProtocol::None:
            return {};
    }
    throw std::invalid_argument("unsupported authentication protocol");
}

bool validAuthProtocol(netsec::AuthProtocol protocol) {
    switch (protocol) {
        case netsec::AuthProtocol::None:
        case netsec::AuthProtocol::HmacMd5:
        case netsec::AuthProtocol::HmacSha1:
        case netsec::AuthProtocol::HmacSha256:
            return true;
    }
    return false;
}

bool validPrivProtocol(netsec::PrivProtocol protocol) {
    switch (protocol) {
        case netsec::PrivProtocol::None:
        case netsec::PrivProtocol::Des:
        case netsec::PrivProtocol::TripleDes:
        case netsec::PrivProtocol::Aes128:
        case netsec::PrivProtocol::Aes192:
        case netsec::PrivProtocol::Aes256:
            return true;
    }
    return false;
}

bool constantTimeEqual(const Octets& left, const Octets& right) {
    const std::size_t length = std::max(left.size(), right.size());
    std::uint8_t difference =
        left.size() == right.size() ? 0u : static_cast<std::uint8_t>(1u);
    for (std::size_t index = 0; index < length; ++index) {
        const std::uint8_t a = index < left.size() ? left[index] : 0u;
        const std::uint8_t b = index < right.size() ? right[index] : 0u;
        difference = static_cast<std::uint8_t>(difference | (a ^ b));
    }
    return difference == 0u;
}

std::string hexEncode(const Octets& bytes) {
    static constexpr char digits[] = "0123456789abcdef";
    std::string result;
    result.reserve(bytes.size() * 2);
    for (const std::uint8_t byte : bytes) {
        result.push_back(digits[byte >> 4u]);
        result.push_back(digits[byte & 0x0fu]);
    }
    return result;
}

int hexValue(char character) {
    if (character >= '0' && character <= '9') {
        return character - '0';
    }
    if (character >= 'a' && character <= 'f') {
        return character - 'a' + 10;
    }
    if (character >= 'A' && character <= 'F') {
        return character - 'A' + 10;
    }
    return -1;
}

Octets hexDecode(std::string_view text) {
    if ((text.size() % 2u) != 0u) {
        throw std::runtime_error("invalid hexadecimal engine ID");
    }
    Octets result;
    result.reserve(text.size() / 2u);
    for (std::size_t index = 0; index < text.size(); index += 2) {
        const int high = hexValue(text[index]);
        const int low = hexValue(text[index + 1]);
        if (high < 0 || low < 0) {
            throw std::runtime_error("invalid hexadecimal engine ID");
        }
        result.push_back(
            static_cast<std::uint8_t>((high << 4) | low));
    }
    return result;
}

std::uint64_t fnv1a(std::string_view text) {
    std::uint64_t value = 0xcbf29ce484222325ULL;
    for (const unsigned char character : text) {
        value ^= character;
        value *= 0x100000001b3ULL;
    }
    return value;
}

std::string checksumText(std::uint64_t value) {
    std::ostringstream stream;
    stream << std::hex << std::setw(16) << std::setfill('0') << value;
    return stream.str();
}

std::uint64_t parseUnsigned(std::string_view text) {
    if (text.empty()) {
        throw std::runtime_error("empty numeric engine state field");
    }
    std::uint64_t value = 0;
    for (const char character : text) {
        if (character < '0' || character > '9') {
            throw std::runtime_error("invalid numeric engine state field");
        }
        const std::uint64_t digit =
            static_cast<std::uint64_t>(character - '0');
        if (value >
            (std::numeric_limits<std::uint64_t>::max() - digit) / 10u) {
            throw std::runtime_error("engine state value overflow");
        }
        value = value * 10u + digit;
    }
    return value;
}

std::string_view withoutCarriageReturn(std::string_view line) {
    if (!line.empty() && line.back() == '\r') {
        line.remove_suffix(1);
    }
    return line;
}

std::string readTextFile(const std::filesystem::path& path) {
    std::ifstream input(path, std::ios::binary);
    if (!input) {
        throw std::runtime_error("unable to read engine state store");
    }
    std::ostringstream contents;
    contents << input.rdbuf();
    if (!input.good() && !input.eof()) {
        throw std::runtime_error("unable to read engine state store");
    }
    return contents.str();
}

void writeFileDurably(
    const std::filesystem::path& path,
    std::string_view contents) {
    const std::filesystem::path parent = path.parent_path();
    if (!parent.empty()) {
        std::filesystem::create_directories(parent);
    }

#if defined(__unix__) || defined(__APPLE__)
    std::filesystem::path temporary = path;
    temporary += ".tmp";

    int flags = O_WRONLY | O_CREAT | O_TRUNC;
#ifdef O_CLOEXEC
    flags |= O_CLOEXEC;
#endif
#ifdef O_NOFOLLOW
    flags |= O_NOFOLLOW;
#endif
    int descriptor =
        ::open(temporary.c_str(), flags, S_IRUSR | S_IWUSR);
    if (descriptor < 0) {
        throw std::runtime_error("unable to open engine state store");
    }

    try {
        if (::fchmod(descriptor, S_IRUSR | S_IWUSR) != 0) {
            throw std::runtime_error(
                "unable to protect engine state store");
        }
        std::size_t written = 0;
        while (written < contents.size()) {
            const ssize_t result = ::write(
                descriptor,
                contents.data() + written,
                contents.size() - written);
            if (result < 0) {
                if (errno == EINTR) {
                    continue;
                }
                throw std::runtime_error("unable to write engine state store");
            }
            written += static_cast<std::size_t>(result);
        }
        if (::fsync(descriptor) != 0) {
            throw std::runtime_error("unable to sync engine state store");
        }
        const int close_result = ::close(descriptor);
        descriptor = -1;
        if (close_result != 0) {
            throw std::runtime_error("unable to close engine state store");
        }
        if (::rename(temporary.c_str(), path.c_str()) != 0) {
            throw std::runtime_error("unable to replace engine state store");
        }

        if (!parent.empty()) {
            const int directory = ::open(parent.c_str(), O_RDONLY);
            if (directory >= 0) {
                (void)::fsync(directory);
                (void)::close(directory);
            }
        }
    } catch (...) {
        if (descriptor >= 0) {
            (void)::close(descriptor);
        }
        std::error_code ignored;
        std::filesystem::remove(temporary, ignored);
        throw;
    }
#else
    std::filesystem::path temporary = path;
    temporary += ".tmp";
    {
        std::ofstream output(
            temporary, std::ios::binary | std::ios::trunc);
        if (!output) {
            throw std::runtime_error("unable to open engine state store");
        }
        output.write(
            contents.data(), static_cast<std::streamsize>(contents.size()));
        output.flush();
        if (!output) {
            throw std::runtime_error("unable to write engine state store");
        }
    }
    std::error_code error;
    std::filesystem::rename(temporary, path, error);
    if (error) {
        throw std::runtime_error("unable to replace engine state store");
    }
#endif
}

void validateEngineId(const Octets& engine_id) {
    if (engine_id.empty() || engine_id.size() > 64u) {
        throw std::invalid_argument("engine ID must contain 1 to 64 bytes");
    }
}

void validateConfig(const netsec::EngineConfig& config) {
    validateEngineId(config.engine_id);
    if (config.boots > netsec::kMaxEngineBoots) {
        throw std::invalid_argument("engine boots is out of range");
    }
    if (config.replay_capacity == 0u) {
        throw std::invalid_argument("replay capacity must be positive");
    }
}

void validatePersisted(const netsec::PersistedEngineState& state) {
    validateEngineId(state.engine_id);
    if (state.boots > netsec::kMaxEngineBoots) {
        throw std::runtime_error("persisted engine boots is out of range");
    }
}

bool validUser(const netsec::UsmUser& user) {
    if (user.name.empty() || user.key_generation == 0u ||
        !validAuthProtocol(user.auth_protocol) ||
        !validPrivProtocol(user.priv_protocol)) {
        return false;
    }
    if (user.auth_protocol != netsec::AuthProtocol::None &&
        user.auth_key.empty()) {
        return false;
    }
    if (user.priv_protocol != netsec::PrivProtocol::None &&
        user.privacy_key.empty()) {
        return false;
    }
    return true;
}

void appendU32(Octets& output, std::uint32_t value) {
    output.push_back(static_cast<std::uint8_t>(value >> 24u));
    output.push_back(static_cast<std::uint8_t>(value >> 16u));
    output.push_back(static_cast<std::uint8_t>(value >> 8u));
    output.push_back(static_cast<std::uint8_t>(value));
}

void appendBytes(Octets& output, const Octets& value) {
    appendU32(output, static_cast<std::uint32_t>(value.size()));
    output.insert(output.end(), value.begin(), value.end());
}

void appendString(Octets& output, std::string_view value) {
    appendU32(output, static_cast<std::uint32_t>(value.size()));
    output.insert(output.end(), value.begin(), value.end());
}

Octets hmacDigest(
    netsec::AuthProtocol protocol,
    const Octets& key,
    const Octets& message) {
    if (protocol == netsec::AuthProtocol::None) {
        return {};
    }

    constexpr std::size_t block_size = 64;
    Octets normalized_key = key;
    if (normalized_key.size() > block_size) {
        normalized_key = digest(protocol, normalized_key);
    }
    normalized_key.resize(block_size, 0u);

    Octets inner(block_size);
    Octets outer(block_size);
    for (std::size_t index = 0; index < block_size; ++index) {
        inner[index] =
            static_cast<std::uint8_t>(normalized_key[index] ^ 0x36u);
        outer[index] =
            static_cast<std::uint8_t>(normalized_key[index] ^ 0x5cu);
    }
    inner.insert(inner.end(), message.begin(), message.end());
    const Octets inner_digest = digest(protocol, inner);
    outer.insert(outer.end(), inner_digest.begin(), inner_digest.end());
    return digest(protocol, outer);
}

}  // namespace

namespace netsec {

std::string_view to_string(AuthProtocol protocol) noexcept {
    switch (protocol) {
        case AuthProtocol::None:
            return "none";
        case AuthProtocol::HmacMd5:
            return "hmac-md5";
        case AuthProtocol::HmacSha1:
            return "hmac-sha1";
        case AuthProtocol::HmacSha256:
            return "hmac-sha256";
    }
    return "unknown";
}

std::string_view to_string(PrivProtocol protocol) noexcept {
    switch (protocol) {
        case PrivProtocol::None:
            return "none";
        case PrivProtocol::Des:
            return "des";
        case PrivProtocol::TripleDes:
            return "3des";
        case PrivProtocol::Aes128:
            return "aes-128";
        case PrivProtocol::Aes192:
            return "aes-192";
        case PrivProtocol::Aes256:
            return "aes-256";
    }
    return "unknown";
}

std::optional<PersistedEngineState> MemoryEngineStateStore::load() const {
    const std::lock_guard lock(mutex_);
    return state_;
}

void MemoryEngineStateStore::save(const PersistedEngineState& state) {
    validatePersisted(state);
    const std::lock_guard lock(mutex_);
    state_ = state;
}

FileEngineStateStore::FileEngineStateStore(std::filesystem::path path)
    : path_(std::move(path)) {
    if (path_.empty()) {
        throw std::invalid_argument("engine state store path is empty");
    }
}

std::optional<PersistedEngineState> FileEngineStateStore::load() const {
    const std::lock_guard lock(mutex_);
    std::error_code error;
    const bool exists = std::filesystem::exists(path_, error);
    if (error) {
        throw std::runtime_error("unable to inspect engine state store");
    }
    if (!exists) {
        return std::nullopt;
    }

    const std::string text = readTextFile(path_);
    if (text.size() > 4'096u) {
        throw std::runtime_error("engine state store is too large");
    }
    const std::size_t checksum_position = text.rfind("checksum: ");
    if (checksum_position == std::string::npos ||
        (checksum_position != 0u &&
         text[checksum_position - 1u] != '\n')) {
        throw std::runtime_error("invalid engine state store format");
    }

    const std::string_view body(text.data(), checksum_position);
    std::string_view checksum_line(text);
    checksum_line.remove_prefix(checksum_position + 10u);
    if (!checksum_line.empty() && checksum_line.back() == '\n') {
        checksum_line.remove_suffix(1);
    }
    checksum_line = withoutCarriageReturn(checksum_line);
    const std::string expected_checksum = checksumText(fnv1a(body));
    if (checksum_line.size() != 16u ||
        std::string_view(expected_checksum) != checksum_line) {
        throw std::runtime_error("engine state store checksum mismatch");
    }

    std::istringstream lines{std::string(body)};
    std::string line;
    if (!std::getline(lines, line) ||
        withoutCarriageReturn(line) != "NETSEC-ENGINE-STATE 1") {
        throw std::runtime_error("invalid engine state store header");
    }

    PersistedEngineState state;
    bool have_engine_id = false;
    bool have_boots = false;
    bool have_time = false;
    while (std::getline(lines, line)) {
        const std::string_view field = withoutCarriageReturn(line);
        if (field.empty()) {
            continue;
        }
        const std::size_t separator = field.find(": ");
        if (separator == std::string_view::npos) {
            throw std::runtime_error("invalid engine state store field");
        }
        const std::string_view name = field.substr(0, separator);
        const std::string_view value = field.substr(separator + 2u);
        if (name == "engine_id" && !have_engine_id) {
            state.engine_id = hexDecode(value);
            have_engine_id = true;
        } else if (name == "boots" && !have_boots) {
            const std::uint64_t boots = parseUnsigned(value);
            if (boots > std::numeric_limits<std::uint32_t>::max()) {
                throw std::runtime_error("engine boots value overflow");
            }
            state.boots = static_cast<std::uint32_t>(boots);
            have_boots = true;
        } else if (name == "engine_time" && !have_time) {
            const std::uint64_t engine_time = parseUnsigned(value);
            if (engine_time >
                std::numeric_limits<std::uint32_t>::max()) {
                throw std::runtime_error("engine time value overflow");
            }
            state.engine_time = static_cast<std::uint32_t>(engine_time);
            have_time = true;
        } else {
            throw std::runtime_error("invalid engine state store field");
        }
    }

    if (!have_engine_id || !have_boots || !have_time) {
        throw std::runtime_error("incomplete engine state store");
    }
    validatePersisted(state);
    return state;
}

void FileEngineStateStore::save(const PersistedEngineState& state) {
    validatePersisted(state);
    const std::lock_guard lock(mutex_);

    std::ostringstream body;
    body << "NETSEC-ENGINE-STATE 1\n";
    body << "engine_id: " << hexEncode(state.engine_id) << '\n';
    body << "boots: " << state.boots << '\n';
    body << "engine_time: " << state.engine_time << '\n';
    const std::string encoded_body = body.str();

    std::string contents = encoded_body;
    contents += "checksum: ";
    contents += checksumText(fnv1a(encoded_body));
    contents += '\n';
    writeFileDurably(path_, contents);
}

const std::filesystem::path& FileEngineStateStore::path() const noexcept {
    return path_;
}

EngineIdentity::EngineIdentity(
    EngineConfig config,
    std::shared_ptr<EngineStateStore> store,
    MonotonicClock clock)
    : store_(std::move(store)),
      clock_(std::move(clock)),
      time_window_seconds_(config.time_window_seconds) {
    validateConfig(config);
    if (!store_) {
        store_ = std::make_shared<MemoryEngineStateStore>();
    }
    if (!clock_) {
        clock_ = [] { return std::chrono::steady_clock::now(); };
    }
    clock_origin_ = clock_();

    const std::optional<PersistedEngineState> persisted = store_->load();
    if (!persisted) {
        engine_id_ = config.engine_id;
        boots_ = config.boots;
        base_engine_time_ = config.engine_time;
        saveOrThrow(boots_, base_engine_time_);
        return;
    }

    validatePersisted(*persisted);
    boots_ = std::max(config.boots, persisted->boots);
    base_engine_time_ = std::max(config.engine_time, persisted->engine_time);

    if (persisted->engine_id != config.engine_id) {
        engine_id_ = persisted->engine_id;
        resynchronization_required_ = true;
        return;
    }

    engine_id_ = config.engine_id;
    if (boots_ != persisted->boots ||
        base_engine_time_ != persisted->engine_time) {
        saveOrThrow(boots_, base_engine_time_);
    }
}

EngineIdentity::~EngineIdentity() {
    (void)persistCheckpoint();
}

void EngineIdentity::saveOrThrow(
    std::uint32_t boots,
    std::uint32_t engine_time) const {
    store_->save(PersistedEngineState{engine_id_, boots, engine_time});
}

EngineSnapshot EngineIdentity::snapshot() const {
    const std::lock_guard lock(mutex_);
    const auto now = clock_();
    auto elapsed = std::chrono::duration_cast<std::chrono::seconds>(
                       now - clock_origin_)
                       .count();
    if (elapsed < 0) {
        elapsed = 0;
    }
    const std::uint64_t total =
        static_cast<std::uint64_t>(base_engine_time_) +
        static_cast<std::uint64_t>(elapsed);
    const std::uint32_t engine_time =
        total > std::numeric_limits<std::uint32_t>::max()
            ? std::numeric_limits<std::uint32_t>::max()
            : static_cast<std::uint32_t>(total);
    return EngineSnapshot{
        engine_id_,
        boots_,
        engine_time,
        boot_generation_,
        resynchronization_required_,
    };
}

Octets EngineIdentity::engineId() const {
    return snapshot().engine_id;
}

std::uint32_t EngineIdentity::boots() const {
    return snapshot().boots;
}

std::uint32_t EngineIdentity::engineTime() const {
    return snapshot().engine_time;
}

std::uint64_t EngineIdentity::bootGeneration() const {
    return snapshot().boot_generation;
}

bool EngineIdentity::resynchronizationRequired() const {
    return snapshot().resynchronization_required;
}

std::uint32_t EngineIdentity::timeWindowSeconds() const {
    const std::lock_guard lock(mutex_);
    return time_window_seconds_;
}

RestoreResult EngineIdentity::restoreConfiguration(
    const EngineConfig& restored) {
    if (restored.engine_id.empty() ||
        restored.boots > kMaxEngineBoots) {
        const std::lock_guard lock(mutex_);
        resynchronization_required_ = true;
        return RestoreResult::RequiresResynchronization;
    }

    const std::lock_guard lock(mutex_);
    if (restored.engine_id != engine_id_) {
        resynchronization_required_ = true;
        return RestoreResult::EngineIdMismatch;
    }
    if (restored.boots < boots_) {
        resynchronization_required_ = true;
        return RestoreResult::RequiresResynchronization;
    }
    if (restored.boots == boots_) {
        resynchronization_required_ = false;
        return RestoreResult::Unchanged;
    }

    const auto new_origin = clock_();
    try {
        saveOrThrow(restored.boots, restored.engine_time);
    } catch (...) {
        return RestoreResult::PersistenceFailure;
    }

    boots_ = restored.boots;
    base_engine_time_ = restored.engine_time;
    clock_origin_ = new_origin;
    ++boot_generation_;
    resynchronization_required_ = false;
    return RestoreResult::Advanced;
}

std::uint32_t EngineIdentity::resynchronize(std::uint32_t minimum_boots) {
    const std::lock_guard lock(mutex_);
    if (minimum_boots > kMaxEngineBoots || boots_ >= kMaxEngineBoots) {
        throw std::overflow_error("engine boots exhausted");
    }

    const std::uint32_t target =
        std::max<std::uint32_t>(boots_ + 1u, minimum_boots);
    const auto new_origin = clock_();
    saveOrThrow(target, 0u);
    boots_ = target;
    base_engine_time_ = 0u;
    clock_origin_ = new_origin;
    ++boot_generation_;
    resynchronization_required_ = false;
    return boots_;
}

std::uint32_t EngineIdentity::engineBooted() {
    return resynchronize();
}

bool EngineIdentity::persistCheckpoint() noexcept {
    try {
        const std::lock_guard lock(mutex_);
        const auto now = clock_();
        auto elapsed =
            std::chrono::duration_cast<std::chrono::seconds>(
                now - clock_origin_)
                .count();
        if (elapsed < 0) {
            elapsed = 0;
        }
        const std::uint64_t total =
            static_cast<std::uint64_t>(base_engine_time_) +
            static_cast<std::uint64_t>(elapsed);
        const std::uint32_t engine_time =
            total > std::numeric_limits<std::uint32_t>::max()
                ? std::numeric_limits<std::uint32_t>::max()
                : static_cast<std::uint32_t>(total);
        saveOrThrow(boots_, engine_time);
        return true;
    } catch (...) {
        return false;
    }
}

SlidingReplayWindow::SlidingReplayWindow(
    std::uint32_t window_seconds,
    std::size_t capacity)
    : window_seconds_(window_seconds), capacity_(capacity) {}

void SlidingReplayWindow::reset() {
    seen_.clear();
    order_.clear();
    boots_ = 0;
    have_boots_ = false;
}

void SlidingReplayWindow::reconfigure(
    std::uint32_t window_seconds,
    std::size_t capacity) {
    window_seconds_ = window_seconds;
    capacity_ = capacity;
}

void SlidingReplayWindow::prune(std::uint32_t local_engine_time) {
    while (!order_.empty()) {
        const auto oldest = order_.begin();
        const std::uint64_t latest =
            static_cast<std::uint64_t>(oldest->first) +
            static_cast<std::uint64_t>(window_seconds_);
        if (latest >= local_engine_time) {
            break;
        }
        const auto found = seen_.find(oldest->second);
        if (found != seen_.end() &&
            found->second.engine_time == oldest->first) {
            seen_.erase(found);
        }
        order_.erase(oldest);
    }
}

WindowResult SlidingReplayWindow::accept(
    std::uint32_t boots,
    std::uint32_t request_engine_time,
    std::uint32_t local_engine_time,
    const Octets& fingerprint) {
    if (!have_boots_ || boots_ != boots) {
        reset();
        boots_ = boots;
        have_boots_ = true;
    }

    const std::uint64_t request_time = request_engine_time;
    const std::uint64_t local_time = local_engine_time;
    const std::uint64_t window = window_seconds_;
    if (request_time + window < local_time ||
        request_time > local_time + window) {
        return WindowResult::OutsideTimeWindow;
    }

    prune(local_engine_time);
    const std::string key =
        fingerprint.empty()
            ? std::string{}
            : std::string(
                  reinterpret_cast<const char*>(fingerprint.data()),
                  fingerprint.size());
    if (seen_.find(key) != seen_.end()) {
        return WindowResult::Duplicate;
    }
    if (seen_.size() >= capacity_) {
        return WindowResult::Full;
    }

    const Entry entry{boots, request_engine_time};
    seen_.emplace(key, entry);
    order_.emplace(entry.engine_time, key);
    return WindowResult::Accepted;
}

std::size_t SlidingReplayWindow::size() const noexcept {
    return seen_.size();
}

std::size_t SlidingReplayWindow::capacity() const noexcept {
    return capacity_;
}

std::uint32_t SlidingReplayWindow::windowSeconds() const noexcept {
    return window_seconds_;
}

ReplayEngine::ReplayEngine(
    EngineConfig config,
    std::shared_ptr<EngineStateStore> store,
    MonotonicClock clock)
    : identity_(config, std::move(store), std::move(clock)),
      time_window_seconds_(config.time_window_seconds),
      replay_capacity_(config.replay_capacity),
      observed_boot_generation_(
          identity_.snapshot().boot_generation) {}

void ReplayEngine::clearWindowsLocked() {
    windows_.clear();
}

void ReplayEngine::clearReplayWindows() {
    const std::lock_guard lock(mutex_);
    clearWindowsLocked();
    observed_boot_generation_ = identity_.snapshot().boot_generation;
}

Octets ReplayEngine::replayFingerprint(const UsmRequest& request) {
    Octets material;
    appendBytes(material, request.authoritative_engine_id);
    appendU32(material, request.boots);
    appendU32(material, request.engine_time);
    appendString(material, request.user_name);
    appendU32(material, request.key_generation);
    appendU32(
        material, static_cast<std::uint32_t>(request.auth_protocol));
    appendU32(
        material, static_cast<std::uint32_t>(request.priv_protocol));
    appendBytes(material, request.authentication_parameters);
    appendBytes(material, request.message);
    return sha256(material);
}

ReplayResult ReplayEngine::verify(const UsmRequest& request) {
    const std::lock_guard lock(mutex_);
    ++statistics_.total_requests;

    const EngineSnapshot engine = identity_.snapshot();
    if (observed_boot_generation_ != engine.boot_generation) {
        clearWindowsLocked();
        observed_boot_generation_ = engine.boot_generation;
    }

    const bool valid_request_protocols =
        validAuthProtocol(request.auth_protocol) &&
        validPrivProtocol(request.priv_protocol);
    const auto user_iterator = users_.find(request.user_name);
    const bool known_user = user_iterator != users_.end();
    const UsmUser* user = known_user ? &user_iterator->second : nullptr;

    const AuthProtocol expected_protocol =
        user != nullptr ? user->auth_protocol : request.auth_protocol;
    static const Octets dummy_key = {
        0x51u, 0x7au, 0x35u, 0xc4u, 0x91u, 0x0eu, 0xb8u, 0x42u,
        0x2du, 0xe3u, 0x77u, 0x19u, 0x6fu, 0xa8u, 0x05u, 0xd6u,
        0xc1u, 0x38u, 0x9eu, 0x54u, 0x0bu, 0xfbu, 0x63u, 0x87u,
        0x2au, 0x4cu, 0xe9u, 0x16u, 0x79u, 0xa0u, 0x85u, 0x3fu,
    };
    const Octets& expected_key =
        user != nullptr ? user->auth_key : dummy_key;

    Octets expected_authenticator;
    bool authenticator_ok = false;
    if (valid_request_protocols) {
        expected_authenticator = makeAuthenticator(
            expected_protocol, expected_key, request.message);
        authenticator_ok = constantTimeEqual(
            expected_authenticator, request.authentication_parameters);
    }

    const bool metadata_ok =
        known_user && valid_request_protocols &&
        request.key_generation == user->key_generation &&
        request.auth_protocol == user->auth_protocol &&
        request.priv_protocol == user->priv_protocol;
    const bool engine_id_ok =
        constantTimeEqual(engine.engine_id, request.authoritative_engine_id);

    if (!engine_id_ok) {
        ++statistics_.engine_id_rejections;
        ++statistics_.dropped_requests;
        return ReplayResult{
            ReplayStatus::Rejected,
            ReplayReason::InvalidSecurityParameters,
        };
    }
    if (!known_user || !metadata_ok || !authenticator_ok) {
        if (known_user && request.key_generation != user->key_generation) {
            ++statistics_.key_generation_rejections;
        } else {
            ++statistics_.authentication_rejections;
        }
        ++statistics_.dropped_requests;
        return ReplayResult{
            ReplayStatus::Rejected,
            ReplayReason::InvalidSecurityParameters,
        };
    }
    if (engine.resynchronization_required) {
        ++statistics_.time_window_rejections;
        ++statistics_.dropped_requests;
        return ReplayResult{
            ReplayStatus::Rejected,
            ReplayReason::OutsideTimeWindow,
        };
    }
    if (request.boots != engine.boots) {
        ++statistics_.time_window_rejections;
        ++statistics_.dropped_requests;
        return ReplayResult{
            ReplayStatus::Rejected,
            ReplayReason::OutsideTimeWindow,
        };
    }

    const std::uint64_t request_time = request.engine_time;
    const std::uint64_t local_time = engine.engine_time;
    const std::uint64_t window = time_window_seconds_;
    if (request_time + window < local_time ||
        request_time > local_time + window) {
        ++statistics_.time_window_rejections;
        ++statistics_.dropped_requests;
        return ReplayResult{
            ReplayStatus::Rejected,
            ReplayReason::OutsideTimeWindow,
        };
    }

    auto window_iterator = windows_.find(request.user_name);
    if (window_iterator == windows_.end()) {
        window_iterator =
            windows_
                .emplace(
                    request.user_name,
                    SlidingReplayWindow(
                        time_window_seconds_, replay_capacity_))
                .first;
    }

    const Octets fingerprint = replayFingerprint(request);
    switch (window_iterator->second.accept(
        request.boots,
        request.engine_time,
        engine.engine_time,
        fingerprint)) {
        case WindowResult::Accepted:
            break;
        case WindowResult::Duplicate:
            ++statistics_.replay_rejections;
            ++statistics_.dropped_requests;
            return ReplayResult{
                ReplayStatus::Rejected, ReplayReason::ReplayedPdu};
        case WindowResult::OutsideTimeWindow:
            ++statistics_.time_window_rejections;
            ++statistics_.dropped_requests;
            return ReplayResult{
                ReplayStatus::Rejected,
                ReplayReason::OutsideTimeWindow,
            };
        case WindowResult::Full:
            ++statistics_.capacity_rejections;
            ++statistics_.dropped_requests;
            return ReplayResult{
                ReplayStatus::Rejected,
                ReplayReason::CapacityExceeded,
            };
    }

    ++statistics_.accepted_requests;
    return ReplayResult{ReplayStatus::Accepted, ReplayReason::None};
}

UserMutationResult ReplayEngine::addUser(UsmUser user) {
    if (!validUser(user)) {
        return UserMutationResult::Invalid;
    }
    const std::lock_guard lock(mutex_);
    if (users_.find(user.name) != users_.end()) {
        return UserMutationResult::AlreadyExists;
    }
    const std::string name = user.name;
    users_.emplace(name, std::move(user));
    windows_.erase(name);
    return UserMutationResult::Added;
}

UserMutationResult ReplayEngine::updateUser(UsmUser user) {
    if (!validUser(user)) {
        return UserMutationResult::Invalid;
    }
    const std::lock_guard lock(mutex_);
    const auto found = users_.find(user.name);
    if (found == users_.end()) {
        return UserMutationResult::NotFound;
    }
    if (user.key_generation <= found->second.key_generation) {
        return UserMutationResult::StaleKeyGeneration;
    }
    const std::string name = user.name;
    found->second = std::move(user);
    windows_.erase(name);
    return UserMutationResult::Updated;
}

UserMutationResult ReplayEngine::upsertUser(UsmUser user) {
    if (!validUser(user)) {
        return UserMutationResult::Invalid;
    }
    const std::lock_guard lock(mutex_);
    const auto found = users_.find(user.name);
    if (found == users_.end()) {
        const std::string name = user.name;
        users_.emplace(name, std::move(user));
        windows_.erase(name);
        return UserMutationResult::Added;
    }
    if (user.key_generation <= found->second.key_generation) {
        return UserMutationResult::StaleKeyGeneration;
    }
    const std::string name = user.name;
    found->second = std::move(user);
    windows_.erase(name);
    return UserMutationResult::Updated;
}

UserMutationResult ReplayEngine::rotateUserKeys(
    std::string_view user_name,
    std::uint32_t new_key_generation,
    AuthProtocol auth_protocol,
    PrivProtocol priv_protocol,
    Octets auth_key,
    Octets privacy_key) {
    UsmUser replacement{
        std::string(user_name),
        new_key_generation,
        auth_protocol,
        priv_protocol,
        std::move(auth_key),
        std::move(privacy_key),
    };
    return updateUser(std::move(replacement));
}

bool ReplayEngine::removeUser(std::string_view user_name) {
    const std::lock_guard lock(mutex_);
    const std::string key(user_name);
    windows_.erase(key);
    return users_.erase(key) != 0u;
}

void ReplayEngine::clearUsers() {
    const std::lock_guard lock(mutex_);
    users_.clear();
    windows_.clear();
}

RestoreResult ReplayEngine::restoreConfiguration(
    const EngineConfig& restored) {
    const std::lock_guard lock(mutex_);
    const RestoreResult result = identity_.restoreConfiguration(restored);
    const std::uint64_t generation =
        identity_.snapshot().boot_generation;
    if (generation != observed_boot_generation_) {
        clearWindowsLocked();
        observed_boot_generation_ = generation;
    }
    if (restored.time_window_seconds > 0u &&
        restored.time_window_seconds < time_window_seconds_) {
        time_window_seconds_ = restored.time_window_seconds;
    }
    if (restored.replay_capacity > 0u &&
        restored.replay_capacity < replay_capacity_) {
        replay_capacity_ = restored.replay_capacity;
    }
    if (restored.time_window_seconds > 0u &&
        restored.replay_capacity > 0u) {
        for (auto& [name, window] : windows_) {
            (void)name;
            window.reconfigure(time_window_seconds_, replay_capacity_);
        }
    }
    return result;
}

std::uint32_t ReplayEngine::resynchronize(std::uint32_t minimum_boots) {
    const std::lock_guard lock(mutex_);
    const std::uint32_t result = identity_.resynchronize(minimum_boots);
    clearWindowsLocked();
    observed_boot_generation_ = identity_.snapshot().boot_generation;
    return result;
}

std::uint32_t ReplayEngine::engineBooted() {
    return resynchronize();
}

bool ReplayEngine::resynchronizationRequired() const {
    return identity_.resynchronizationRequired();
}

EngineIdentity& ReplayEngine::identity() noexcept {
    return identity_;
}

const EngineIdentity& ReplayEngine::identity() const noexcept {
    return identity_;
}

EngineSnapshot ReplayEngine::snapshot() const {
    return identity_.snapshot();
}

ReplayStatistics ReplayEngine::statistics() const {
    const std::lock_guard lock(mutex_);
    return statistics_;
}

void ReplayEngine::resetStatistics() {
    const std::lock_guard lock(mutex_);
    statistics_ = {};
}

Octets hmac(
    AuthProtocol protocol,
    const Octets& key,
    const Octets& message) {
    if (!validAuthProtocol(protocol)) {
        throw std::invalid_argument("unsupported authentication protocol");
    }
    return hmacDigest(protocol, key, message);
}

Octets makeAuthenticator(
    AuthProtocol protocol,
    const Octets& key,
    const Octets& authenticated_message) {
    Octets value = hmac(protocol, key, authenticated_message);
    switch (protocol) {
        case AuthProtocol::None:
            return {};
        case AuthProtocol::HmacMd5:
        case AuthProtocol::HmacSha1:
            value.resize(12);
            return value;
        case AuthProtocol::HmacSha256:
            value.resize(24);
            return value;
    }
    throw std::invalid_argument("unsupported authentication protocol");
}

bool verifyAuthenticator(
    AuthProtocol protocol,
    const Octets& key,
    const Octets& authenticated_message,
    const Octets& authentication_parameters) {
    const Octets expected =
        makeAuthenticator(protocol, key, authenticated_message);
    return constantTimeEqual(expected, authentication_parameters);
}

Octets localizeKey(
    AuthProtocol protocol,
    std::string_view password,
    const Octets& engine_id) {
    if (protocol == AuthProtocol::None) {
        return {};
    }
    if (!validAuthProtocol(protocol) || engine_id.empty()) {
        throw std::invalid_argument("invalid key localization input");
    }

    constexpr std::size_t password_block_size = 1'048'576;
    Octets repeated(password_block_size, 0u);
    if (!password.empty()) {
        for (std::size_t index = 0; index < repeated.size(); ++index) {
            repeated[index] = static_cast<std::uint8_t>(
                password[index % password.size()]);
        }
    }

    const Octets intermediate = digest(protocol, repeated);
    Octets localized_input = intermediate;
    localized_input.insert(
        localized_input.end(), engine_id.begin(), engine_id.end());
    localized_input.insert(
        localized_input.end(), intermediate.begin(), intermediate.end());
    return digest(protocol, localized_input);
}

}  // namespace netsec
