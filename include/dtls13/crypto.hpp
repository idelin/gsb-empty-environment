// SPDX-License-Identifier: MIT
// Abstract cryptographic primitives plus deterministic, dependency-free
// reference implementations.
//
// The reference AEAD / MAC are intentionally simple and exist so the
// record-layer and reassembly logic can be exercised deterministically
// without an external TLS library. They are NOT a substitute for a
// validated AES-GCM / ChaCha20-Poly1305 / HMAC implementation.

#ifndef DTLS13_CRYPTO_HPP
#define DTLS13_CRYPTO_HPP

#include <array>
#include <cstdint>
#include <memory>
#include <vector>

#include "dtls13/types.hpp"

namespace dtls13 {

// ---------------------------------------------------------------------------
// SHA-256 (FIPS 180-4), used by the reference primitives and cookie HMAC.
// ---------------------------------------------------------------------------

class Sha256 {
public:
    static constexpr std::size_t kDigestSize = 32;
    static constexpr std::size_t kBlockSize = 64;

    Sha256() { reset(); }

    void reset() noexcept;
    void update(ByteView data) noexcept;
    void finish(std::uint8_t digest[kDigestSize]) noexcept;

    static std::array<std::uint8_t, kDigestSize> hash(ByteView data);

private:
    void processBlock(const std::uint8_t* block) noexcept;

    std::array<std::uint32_t, 8> state_{};
    std::array<std::uint8_t, kBlockSize> buffer_{};
    std::uint64_t total_length_ = 0;
    std::size_t buffered_ = 0;
};

// HMAC-SHA256 (RFC 2104).
std::array<std::uint8_t, Sha256::kDigestSize> hmacSha256(ByteView key,
                                                         ByteView message);

// Pseudo-random function used by the reference AEAD: HMAC-SHA256 expanded
// into an arbitrary-length key stream.
Bytes hkdfLikeExpand(ByteView secret, ByteView info, std::size_t output_len);

// ---------------------------------------------------------------------------
// Abstract injected primitives.
// ---------------------------------------------------------------------------

// Builds the per-record AEAD nonce from the fixed IV and the 48-bit record
// sequence number. TLS 1.3 / DTLS 1.3 XOR the IV with the sequence number
// in the trailing bytes (RFC 8446 section 5.3).
class INonceMasker {
public:
    virtual ~INonceMasker() = default;
    // `nonce` is zero-initialized by the caller with capacity for
    // nonceLength() bytes.
    virtual void mask(std::uint64_t seq48,
                      std::uint8_t* nonce) const noexcept = 0;
    virtual std::size_t nonceLength() const noexcept = 0;
};

// Standard TLS 1.3 IV XOR masker.
class XorNonceMasker final : public INonceMasker {
public:
    static constexpr std::size_t kNonceLength = 12;

    explicit XorNonceMasker(
        std::array<std::uint8_t, kNonceLength> fixed_iv) noexcept
        : fixed_iv_(fixed_iv) {}

    void mask(std::uint64_t seq48,
              std::uint8_t* nonce) const noexcept override;
    std::size_t nonceLength() const noexcept override {
        return kNonceLength;
    }

private:
    std::array<std::uint8_t, kNonceLength> fixed_iv_;
};

// Authenticated encryption primitive. The AAD is the serialized DTLS
// record header (content type + unified header), which is exactly what
// RFC 9147 section 4.5.2 binds into the AEAD input.
class IAead {
public:
    virtual ~IAead() = default;

    virtual std::size_t tagLength() const noexcept = 0;

    // Produces ciphertext || tag. Returns an empty vector only on an
    // internal allocation failure.
    virtual Bytes seal(ByteView nonce,
                       ByteView plaintext,
                       ByteView aad) = 0;

    // Verifies the tag and returns the plaintext. Any authentication
    // failure yields ErrorCode::AeadDecryptFailed and no plaintext.
    virtual Result<Bytes> open(ByteView nonce,
                               ByteView ciphertext_and_tag,
                               ByteView aad) = 0;
};

// Deterministic, zero-dependency reference AEAD.
//
// Construction: encrypt-then-MAC.
//   key_stream = expand(HMAC(key, "dtls13-ref-key" | nonce))
//   ciphertext = plaintext XOR key_stream
//   tag        = HMAC(key, "dtls13-ref-tag" | nonce | aad | ciphertext)
//                truncated to 16 bytes
//
// The same (key, nonce) always yields the same output, which keeps tests
// deterministic. Reusing a nonce in real TLS destroys confidentiality;
// the replay window is the control that prevents nonce reuse on receive.
class ReferenceAead final : public IAead {
public:
    static constexpr std::size_t kTagLength = 16;
    static constexpr std::size_t kKeyLength = 32;

    explicit ReferenceAead(
        std::array<std::uint8_t, kKeyLength> key) noexcept
        : key_(key.begin(), key.end()) {}
    explicit ReferenceAead(ByteView key) : key_(key.data(), key.data() + key.size()) {}

    std::size_t tagLength() const noexcept override { return kTagLength; }
    Bytes seal(ByteView nonce, ByteView plaintext, ByteView aad) override;
    Result<Bytes> open(ByteView nonce,
                       ByteView ciphertext_and_tag,
                       ByteView aad) override;

private:
    Bytes deriveStream(ByteView nonce, std::size_t length) const;
    std::array<std::uint8_t, kTagLength> computeTag(
        ByteView nonce, ByteView aad, ByteView ciphertext) const;

    std::vector<std::uint8_t> key_;
};

// ---------------------------------------------------------------------------
// Cookie integrity primitive for the stateless HelloRetryRequest defense.
// ---------------------------------------------------------------------------

class ICookieSigner {
public:
    virtual ~ICookieSigner() = default;

    // Sign `context` (client identity hash + server parameters) into a
    // self-contained token.
    virtual Bytes sign(ByteView context) = 0;
    // Constant-time verification of a token previously produced by sign().
    virtual bool verify(ByteView context, ByteView token) noexcept = 0;
};

// HMAC-SHA256 based cookie signer. The token layout is:
//   version(1) | issue_time_be(8) | context_hash(32) | hmac(32)
// so verification needs no server-side state beyond the secret key.
class HmacCookieSigner final : public ICookieSigner {
public:
    static constexpr std::size_t kTokenLength = 1 + 8 + 32 + 32;
    static constexpr std::uint8_t kVersion = 1;

    HmacCookieSigner(ByteView secret,
                     std::uint64_t now_seconds,
                     std::uint64_t lifetime_seconds = 60)
        : secret_(secret.data(), secret.data() + secret.size()),
          now_seconds_(now_seconds),
          lifetime_seconds_(lifetime_seconds) {}

    Bytes sign(ByteView context) override;
    bool verify(ByteView context, ByteView token) noexcept override;

private:
    std::array<std::uint8_t, 32> macOver(ByteView token_prefix) const;

    std::vector<std::uint8_t> secret_;
    std::uint64_t now_seconds_;
    std::uint64_t lifetime_seconds_;
};

}  // namespace dtls13

#endif  // DTLS13_CRYPTO_HPP
