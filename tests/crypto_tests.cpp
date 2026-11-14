// Compiled as a standalone translation unit so it can inspect the internal
// primitives without exporting them from the public header.
#include "../netsec.cpp"

#include <array>
#include <cstdint>
#include <iostream>
#include <string_view>

namespace {

int failures = 0;

template <std::size_t Size>
std::array<std::uint8_t, Size> from_hex(std::string_view text) {
    std::array<std::uint8_t, Size> result{};
    for (std::size_t index = 0; index < Size; ++index) {
        const auto nibble = [](char value) -> std::uint8_t {
            if (value >= '0' && value <= '9') {
                return static_cast<std::uint8_t>(value - '0');
            }
            if (value >= 'a' && value <= 'f') {
                return static_cast<std::uint8_t>(value - 'a' + 10);
            }
            return static_cast<std::uint8_t>(value - 'A' + 10);
        };
        result[index] = static_cast<std::uint8_t>(
            (nibble(text[index * 2U]) << 4U) | nibble(text[index * 2U + 1U]));
    }
    return result;
}

template <std::size_t Size>
void check_array(const char* name, const std::array<std::uint8_t, Size>& actual,
                 std::string_view expected_hex) {
    const auto expected = from_hex<Size>(expected_hex);
    if (actual != expected) {
        std::cerr << name << " vector mismatch\n";
        ++failures;
    }
}

void test_sha256_vectors() {
    {
        netsec::detail::Sha256 hash;
        constexpr std::string_view input = "abc";
        hash.update(netsec::ByteSpan(
            reinterpret_cast<const std::uint8_t*>(input.data()), input.size()));
        check_array(
            "sha256(abc)", hash.finish(),
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
    }
    {
        netsec::detail::Sha256 hash;
        constexpr std::string_view input =
            "abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq";
        hash.update(netsec::ByteSpan(
            reinterpret_cast<const std::uint8_t*>(input.data()), input.size()));
        check_array(
            "sha256(two-block)",
            hash.finish(),
            "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1");
    }
}

void test_hmac_vectors() {
    {
        constexpr std::string_view key_text =
            "0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b";
        constexpr std::string_view data_text = "Hi There";
        const auto key = from_hex<20>(key_text);
        const auto tag = netsec::detail::hmac_sha256(
            netsec::ByteSpan(key.data(), key.size()),
            netsec::ByteSpan(
                reinterpret_cast<const std::uint8_t*>(data_text.data()),
                data_text.size()));
        check_array(
            "hmac-sha256(rfc4231-1)", tag,
            "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7");
    }
    {
        constexpr std::string_view key_text = "4a656665";  // "Jefe"
        constexpr std::string_view data_text = "what do ya want for nothing?";
        const auto key = from_hex<4>(key_text);
        const auto tag = netsec::detail::hmac_sha256(
            netsec::ByteSpan(key.data(), key.size()),
            netsec::ByteSpan(
                reinterpret_cast<const std::uint8_t*>(data_text.data()),
                data_text.size()));
        check_array(
            "hmac-sha256(rfc4231-2)", tag,
            "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843");
    }
}

}  // namespace

int main() {
    test_sha256_vectors();
    test_hmac_vectors();
    if (failures == 0) {
        std::cout << "crypto vectors passed\n";
    }
    return failures == 0 ? 0 : 1;
}
