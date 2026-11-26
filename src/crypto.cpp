// SPDX-License-Identifier: MIT

#include "dtls13/crypto.hpp"

#include <algorithm>
#include <cstring>

namespace dtls13 {

namespace {

constexpr std::uint32_t kSha256K[64] = {
    0x428a2f98U, 0x71374491U, 0xb5c0fbcfU, 0xe9b5dba5U, 0x3956c25bU,
    0x59f111f1U, 0x923f82a4U, 0xab1c5ed5U, 0xd807aa98U, 0x12835b01U,
    0x243185beU, 0x550c7dc3U, 0x72be5d74U, 0x80deb1feU, 0x9bdc06a7U,
    0xc19bf174U, 0xe49b69c1U, 0xefbe4786U, 0x0fc19dc6U, 0x240ca1ccU,
    0x2de92c6fU, 0x4a7484aaU, 0x5cb0a9dcU, 0x76f988daU, 0x983e5152U,
    0xa831c66dU, 0xb00327c8U, 0xbf597fc7U, 0xc6e00bf3U, 0xd5a79147U,
    0x06ca6351U, 0x14292967U, 0x27b70a85U, 0x2e1b2138U, 0x4d2c6dfcU,
    0x53380d13U, 0x650a7354U, 0x766a0abbU, 0x81c2c92eU, 0x92722c85U,
    0xa2bfe8a1U, 0xa81a664bU, 0xc24b8b70U, 0xc76c51a3U, 0xd192e819U,
    0xd6990624U, 0xf40e3585U, 0x106aa070U, 0x19a4c116U, 0x1e376c08U,
    0x2748774cU, 0x34b0bcb5U, 0x391c0cb3U, 0x4ed8aa4aU, 0x5b9cca4fU,
    0x682e6ff3U, 0x748f82eeU, 0x78a5636fU, 0x84c87814U, 0x8cc70208U,
    0x90befffaU, 0xa4506cebU, 0xbef9a3f7U, 0xc67178f2U};

constexpr std::uint32_t rotr(std::uint32_t x, unsigned n) noexcept {
    return (x >> n) | (x << (32U - n));
}

}  // namespace

void Sha256::reset() noexcept {
    state_ = {0x6a09e667U, 0xbb67ae85U, 0x3c6ef372U, 0xa54ff53aU,
              0x510e527fU, 0x9b05688cU, 0x1f83d9abU, 0x5be0cd19U};
    total_length_ = 0;
    buffered_ = 0;
}

void Sha256::processBlock(const std::uint8_t* block) noexcept {
    std::uint32_t w[64];
    for (std::size_t i = 0; i < 16; ++i) {
        w[i] = readBe32(block + 4 * i);
    }
    for (std::size_t i = 16; i < 64; ++i) {
        const std::uint32_t s0 =
            rotr(w[i - 15], 7) ^ rotr(w[i - 15], 18) ^ (w[i - 15] >> 3);
        const std::uint32_t s1 =
            rotr(w[i - 2], 17) ^ rotr(w[i - 2], 19) ^ (w[i - 2] >> 10);
        w[i] = w[i - 16] + s0 + w[i - 7] + s1;
    }

    std::uint32_t a = state_[0];
    std::uint32_t b = state_[1];
    std::uint32_t c = state_[2];
    std::uint32_t d = state_[3];
    std::uint32_t e = state_[4];
    std::uint32_t f = state_[5];
    std::uint32_t g = state_[6];
    std::uint32_t h = state_[7];

    for (std::size_t i = 0; i < 64; ++i) {
        const std::uint32_t s1 = rotr(e, 6) ^ rotr(e, 11) ^ rotr(e, 25);
        const std::uint32_t ch = (e & f) ^ ((~e) & g);
        const std::uint32_t temp1 = h + s1 + ch + kSha256K[i] + w[i];
        const std::uint32_t s0 = rotr(a, 2) ^ rotr(a, 13) ^ rotr(a, 22);
        const std::uint32_t maj = (a & b) ^ (a & c) ^ (b & c);
        const std::uint32_t temp2 = s0 + maj;
        h = g;
        g = f;
        f = e;
        e = d + temp1;
        d = c;
        c = b;
        b = a;
        a = temp1 + temp2;
    }

    state_[0] += a;
    state_[1] += b;
    state_[2] += c;
    state_[3] += d;
    state_[4] += e;
    state_[5] += f;
    state_[6] += g;
    state_[7] += h;
}

void Sha256::update(ByteView data) noexcept {
    std::size_t index = 0;
    total_length_ += static_cast<std::uint64_t>(data.size());

    if (buffered_ != 0) {
        while (index < data.size() && buffered_ < kBlockSize) {
            buffer_[buffered_++] = data[index++];
        }
        if (buffered_ == kBlockSize) {
            processBlock(buffer_.data());
            buffered_ = 0;
        }
    }

    while (data.size() - index >= kBlockSize) {
        processBlock(data.data() + index);
        index += kBlockSize;
    }

    while (index < data.size()) {
        buffer_[buffered_++] = data[index++];
    }
}

void Sha256::finish(std::uint8_t digest[kDigestSize]) noexcept {
    const std::uint64_t bit_length = total_length_ * 8;
    buffer_[buffered_++] = 0x80;
    if (buffered_ > 56) {
        while (buffered_ < kBlockSize) {
            buffer_[buffered_++] = 0;
        }
        processBlock(buffer_.data());
        buffered_ = 0;
    }
    while (buffered_ < 56) {
        buffer_[buffered_++] = 0;
    }
    writeBe64(buffer_.data() + 56, bit_length);
    processBlock(buffer_.data());
    for (std::size_t i = 0; i < state_.size(); ++i) {
        writeBe32(digest + 4 * i, state_[i]);
    }
    reset();
}

std::array<std::uint8_t, Sha256::kDigestSize> Sha256::hash(ByteView data) {
    Sha256 ctx;
    std::array<std::uint8_t, kDigestSize> digest{};
    ctx.update(data);
    ctx.finish(digest.data());
    return digest;
}

std::array<std::uint8_t, Sha256::kDigestSize> hmacSha256(ByteView key,
                                                         ByteView message) {
    std::array<std::uint8_t, Sha256::kBlockSize> norm_key{};
    if (key.size() > Sha256::kBlockSize) {
        auto digest = Sha256::hash(key);
        std::memcpy(norm_key.data(), digest.data(), digest.size());
    } else {
        std::memcpy(norm_key.data(), key.data(), key.size());
    }

    std::array<std::uint8_t, Sha256::kBlockSize> ipad{};
    std::array<std::uint8_t, Sha256::kBlockSize> opad{};
    for (std::size_t i = 0; i < Sha256::kBlockSize; ++i) {
        ipad[i] = norm_key[i] ^ 0x36;
        opad[i] = norm_key[i] ^ 0x5c;
    }

    Sha256 inner;
    inner.update(ByteView(ipad.data(), ipad.size()));
    inner.update(message);
    std::array<std::uint8_t, Sha256::kDigestSize> inner_digest{};
    inner.finish(inner_digest.data());

    Sha256 outer;
    outer.update(ByteView(opad.data(), opad.size()));
    outer.update(ByteView(inner_digest.data(), inner_digest.size()));
    std::array<std::uint8_t, Sha256::kDigestSize> result{};
    outer.finish(result.data());
    return result;
}

Bytes hkdfLikeExpand(ByteView secret, ByteView info, std::size_t output_len) {
    Bytes output;
    output.reserve(output_len);
    std::uint8_t counter = 1;
    Bytes previous;

    while (output.size() < output_len) {
        Bytes material;
        material.insert(material.end(), previous.begin(), previous.end());
        material.insert(material.end(), info.data(),
                        info.data() + info.size());
        material.push_back(counter);
        auto block = hmacSha256(secret, ByteView(material));
        const std::size_t take =
            std::min<std::size_t>(block.size(), output_len - output.size());
        output.insert(output.end(), block.begin(), block.begin() + take);
        previous.assign(block.begin(), block.end());
        ++counter;
    }
    return output;
}

void XorNonceMasker::mask(std::uint64_t seq48,
                          std::uint8_t* nonce) const noexcept {
    std::memcpy(nonce, fixed_iv_.data(), kNonceLength);
    std::uint8_t seq_bytes[8];
    writeBe64(seq_bytes, seq48);
    // TLS 1.3 XORs the 64-bit record number into the last 8 bytes of the
    // 12-byte fixed IV; DTLS 1.3 only uses the low 48 bits.
    for (std::size_t i = 0; i < 8; ++i) {
        const std::size_t index = kNonceLength - 8 + i;
        nonce[index] =
            static_cast<std::uint8_t>(nonce[index] ^ seq_bytes[i]);
    }
}

Bytes ReferenceAead::deriveStream(ByteView nonce,
                                  std::size_t length) const {
    Bytes info;
    static const std::uint8_t kLabel[] = "dtls13-ref-key";
    info.insert(info.end(), kLabel, kLabel + sizeof(kLabel) - 1);
    info.insert(info.end(), nonce.data(), nonce.data() + nonce.size());
    return hkdfLikeExpand(ByteView(key_), ByteView(info), length);
}

std::array<std::uint8_t, ReferenceAead::kTagLength> ReferenceAead::computeTag(
    ByteView nonce, ByteView aad, ByteView ciphertext) const {
    Bytes mac_input;
    static const std::uint8_t kLabel[] = "dtls13-ref-tag";
    mac_input.insert(mac_input.end(), kLabel, kLabel + sizeof(kLabel) - 1);
    mac_input.insert(mac_input.end(), nonce.data(),
                     nonce.data() + nonce.size());
    mac_input.insert(mac_input.end(), aad.data(), aad.data() + aad.size());
    mac_input.insert(mac_input.end(), ciphertext.data(),
                     ciphertext.data() + ciphertext.size());
    auto full = hmacSha256(ByteView(key_), ByteView(mac_input));
    std::array<std::uint8_t, kTagLength> tag{};
    std::memcpy(tag.data(), full.data(), kTagLength);
    return tag;
}

Bytes ReferenceAead::seal(ByteView nonce,
                          ByteView plaintext,
                          ByteView aad) {
    Bytes stream = deriveStream(nonce, plaintext.size());
    Bytes ciphertext;
    ciphertext.resize(plaintext.size());
    for (std::size_t i = 0; i < plaintext.size(); ++i) {
        ciphertext[i] = static_cast<std::uint8_t>(plaintext[i] ^ stream[i]);
    }
    auto tag = computeTag(nonce, aad, ByteView(ciphertext));
    ciphertext.insert(ciphertext.end(), tag.begin(), tag.end());
    return ciphertext;
}

Result<Bytes> ReferenceAead::open(ByteView nonce,
                                  ByteView ciphertext_and_tag,
                                  ByteView aad) {
    if (ciphertext_and_tag.size() < kTagLength) {
        return ErrorCode::AeadDecryptFailed;
    }
    const std::size_t cipher_len =
        ciphertext_and_tag.size() - kTagLength;
    ByteView ciphertext(ciphertext_and_tag.data(), cipher_len);
    ByteView received_tag(ciphertext_and_tag.data() + cipher_len,
                          kTagLength);

    auto expected = computeTag(nonce, aad, ciphertext);
    if (!constantTimeEqual(
            received_tag, ByteView(expected.data(), expected.size()))) {
        return ErrorCode::AeadDecryptFailed;
    }

    Bytes stream = deriveStream(nonce, cipher_len);
    Bytes plaintext;
    plaintext.resize(cipher_len);
    for (std::size_t i = 0; i < cipher_len; ++i) {
        plaintext[i] = static_cast<std::uint8_t>(ciphertext[i] ^ stream[i]);
    }
    return plaintext;
}

std::array<std::uint8_t, 32> HmacCookieSigner::macOver(
    ByteView token_prefix) const {
    return hmacSha256(ByteView(secret_), token_prefix);
}

Bytes HmacCookieSigner::sign(ByteView context) {
    auto context_hash = Sha256::hash(context);

    Bytes token;
    token.reserve(kTokenLength);
    token.push_back(kVersion);
    std::uint8_t time_bytes[8];
    writeBe64(time_bytes, now_seconds_);
    token.insert(token.end(), time_bytes, time_bytes + sizeof(time_bytes));
    token.insert(token.end(), context_hash.begin(), context_hash.end());

    auto mac = macOver(ByteView(token));
    token.insert(token.end(), mac.begin(), mac.end());
    return token;
}

bool HmacCookieSigner::verify(ByteView context, ByteView token) noexcept {
    if (token.size() != kTokenLength || token[0] != kVersion) {
        return false;
    }
    const std::size_t prefix_len = kTokenLength - Sha256::kDigestSize;
    ByteView prefix(token.data(), prefix_len);
    ByteView received_mac(token.data() + prefix_len,
                          Sha256::kDigestSize);
    auto expected_mac = macOver(prefix);
    if (!constantTimeEqual(
            received_mac,
            ByteView(expected_mac.data(), expected_mac.size()))) {
        return false;
    }

    auto context_hash = Sha256::hash(context);
    if (!constantTimeEqual(
            ByteView(token.data() + 9, Sha256::kDigestSize),
            ByteView(context_hash.data(), context_hash.size()))) {
        return false;
    }

    std::uint64_t issued = 0;
    for (std::size_t i = 1; i <= 8; ++i) {
        issued = (issued << 8) | token[i];
    }
    // Reject expired tokens and avoid unsigned wrap on clock skew.
    if (now_seconds_ < issued) {
        return false;
    }
    return now_seconds_ - issued <= lifetime_seconds_;
}

}  // namespace dtls13
