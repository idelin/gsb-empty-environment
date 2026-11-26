// SPDX-License-Identifier: MIT

#include "dtls13/crypto.hpp"
#include "test_framework.hpp"

#include <array>
#include <string>

using namespace dtls13;

namespace {

std::array<std::uint8_t, 32> digestOf(const std::string& input) {
    return Sha256::hash(ByteView(
        reinterpret_cast<const std::uint8_t*>(input.data()), input.size()));
}

std::string hex(const std::array<std::uint8_t, 32>& bytes) {
    static const char* kDigits = "0123456789abcdef";
    std::string out;
    for (std::uint8_t b : bytes) {
        out.push_back(kDigits[b >> 4]);
        out.push_back(kDigits[b & 0x0F]);
    }
    return out;
}

std::vector<std::uint8_t> bytesOf(const std::string& s) {
    return std::vector<std::uint8_t>(s.begin(), s.end());
}

}  // namespace

TEST_CASE(sha256_empty_known_answer) {
    CHECK_EQ(hex(digestOf("")),
             "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855");
}

TEST_CASE(sha256_abc_known_answer) {
    CHECK_EQ(hex(digestOf("abc")),
             "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad");
}

TEST_CASE(sha256_streaming_matches_one_shot) {
    Sha256 ctx;
    const std::string part1 = "abcdefghijklmnopqrstuvwxyz";
    const std::string part2 = "0123456789";
    ctx.update(ByteView(reinterpret_cast<const std::uint8_t*>(part1.data()),
                        part1.size()));
    ctx.update(ByteView(reinterpret_cast<const std::uint8_t*>(part2.data()),
                        part2.size()));
    std::array<std::uint8_t, 32> streamed{};
    ctx.finish(streamed.data());
    CHECK_EQ(hex(streamed), hex(digestOf(part1 + part2)));
}

TEST_CASE(hmac_rfc4231_case1) {
    std::vector<std::uint8_t> key(20, 0x0B);
    const std::string data = "Hi There";
    auto mac = hmacSha256(
        ByteView(key),
        ByteView(reinterpret_cast<const std::uint8_t*>(data.data()),
                 data.size()));
    CHECK_EQ(hex(mac),
             "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7");
}

TEST_CASE(nonce_mask_xor_iv_and_sequence) {
    std::array<std::uint8_t, 12> iv{};
    for (std::size_t i = 0; i < iv.size(); ++i) {
        iv[i] = static_cast<std::uint8_t>(0xA0 + i);
    }
    XorNonceMasker masker(iv);
    std::array<std::uint8_t, 12> nonce{};
    masker.mask(0x010203040506ULL, nonce.data());
    for (std::size_t i = 0; i < 4; ++i) {
        CHECK_EQ(nonce[i], iv[i]);
    }
    // Last 8 bytes = fixed IV XOR big-endian sequence.
    const std::uint64_t seq = 0x010203040506ULL;
    for (std::size_t i = 0; i < 8; ++i) {
        const std::uint8_t seq_byte =
            static_cast<std::uint8_t>(seq >> (56 - 8 * static_cast<unsigned>(i)));
        CHECK_EQ(nonce[4 + i],
                 static_cast<std::uint8_t>(iv[4 + i] ^ seq_byte));
    }
}

TEST_CASE(reference_aead_roundtrip) {
    std::array<std::uint8_t, 32> key{};
    for (std::size_t i = 0; i < key.size(); ++i) {
        key[i] = static_cast<std::uint8_t>(i + 1);
    }
    ReferenceAead aead(key);
    const auto nonce = bytesOf("nonce-12345");
    const auto aad = bytesOf("record-header");
    const auto plaintext = bytesOf("hello dtls 1.3");

    Bytes sealed = aead.seal(ByteView(nonce), ByteView(plaintext),
                             ByteView(aad));
    CHECK_EQ(sealed.size(), plaintext.size() + ReferenceAead::kTagLength);

    auto opened = aead.open(ByteView(nonce), ByteView(sealed),
                            ByteView(aad));
    CHECK(opened.ok());
    CHECK(opened.value == plaintext);
}

TEST_CASE(reference_aead_detects_ciphertext_tamper) {
    std::array<std::uint8_t, 32> key{};
    key.fill(0x42);
    ReferenceAead aead(key);
    const auto nonce = bytesOf("nonce-12345");
    const auto aad = bytesOf("record-header");
    Bytes sealed = aead.seal(ByteView(nonce), ByteView(bytesOf("payload")),
                             ByteView(aad));
    sealed[0] = static_cast<std::uint8_t>(sealed[0] ^ 0x01);
    auto opened = aead.open(ByteView(nonce), ByteView(sealed),
                            ByteView(aad));
    CHECK(!opened.ok());
    CHECK_EQ(opened.code, ErrorCode::AeadDecryptFailed);
}

TEST_CASE(reference_aead_detects_aad_tamper) {
    std::array<std::uint8_t, 32> key{};
    key.fill(0x07);
    ReferenceAead aead(key);
    const auto nonce = bytesOf("nonce-12345");
    Bytes sealed = aead.seal(ByteView(nonce), ByteView(bytesOf("payload")),
                             ByteView(bytesOf("hdr-a")));
    auto opened = aead.open(ByteView(nonce), ByteView(sealed),
                            ByteView(bytesOf("hdr-b")));
    CHECK_EQ(opened.code, ErrorCode::AeadDecryptFailed);
}

TEST_CASE(reference_aead_is_deterministic) {
    std::array<std::uint8_t, 32> key{};
    key.fill(0x11);
    ReferenceAead aead(key);
    const auto nonce = bytesOf("nonce-12345");
    const auto aad = bytesOf("aad");
    const auto plaintext = bytesOf("same input");
    Bytes first = aead.seal(ByteView(nonce), ByteView(plaintext),
                            ByteView(aad));
    Bytes second = aead.seal(ByteView(nonce), ByteView(plaintext),
                             ByteView(aad));
    CHECK(first == second);
}
