#include "detail/crypto.hpp"

#include <cstddef>
#include <cstring>
#include <iostream>
#include <span>
#include <string>

namespace {

int failures = 0;

void check(bool condition, const char* name) {
  if (!condition) {
    std::cerr << "FAIL: " << name << '\n';
    ++failures;
  }
}

template <std::size_t N>
std::span<const netsec::Byte> span(const netsec::Byte (&value)[N]) {
  return std::span<const netsec::Byte>(value, N);
}

}  // namespace

int main() {
  using namespace netsec;

  {
    constexpr netsec::Byte input[] = {'a', 'b', 'c'};
    check(to_hex(detail::sha256(span(input))) ==
              "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
          "SHA-256 known-answer test");
  }

  {
    constexpr netsec::Byte key[] = {'k', 'e', 'y'};
    const char* message = "The quick brown fox jumps over the lazy dog";
    const auto data = std::span<const netsec::Byte>(
        reinterpret_cast<const netsec::Byte*>(message), std::strlen(message));
    check(to_hex(detail::hmac_sha256(span(key), data)) ==
              "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8",
          "HMAC-SHA256 known-answer test");
  }

  {
    const SecretKey alice_private = SecretKey::from_hex(
        "77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a");
    const SecretKey bob_private = SecretKey::from_hex(
        "5dab087e624a8a4b79e17f8b83800ee66f3bb1292618b6fd1c2f8b27ff88e0eb");
    const SecretKey basepoint = SecretKey::from_hex(
        "0900000000000000000000000000000000000000000000000000000000000000");
    const auto alice_public = detail::x25519(alice_private.bytes,
                                             basepoint.bytes);
    const auto bob_public =
        detail::x25519(bob_private.bytes, basepoint.bytes);
    check(to_hex(alice_public) ==
              "8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a",
          "X25519 Alice public key");
    check(to_hex(bob_public) ==
              "de9edb7d7b7dc1b4d35b61c2ece435373f8343c85b78674dadfc7e146f882b4f",
          "X25519 Bob public key");
    check(to_hex(detail::x25519(alice_private.bytes, bob_public)) ==
              "4a5d9d5ba4ce2de1728e3bf480350f25e07e21c947d19e3376f09b3c1e161742",
          "X25519 shared secret");
  }

  {
    const SecretKey key = SecretKey::from_hex(
        "808182838485868788898a8b8c8d8e8f909192939495969798999a9b9c9d9e9f");
    constexpr netsec::Byte nonce[] = {0x07, 0x00, 0x00, 0x00, 0x40, 0x41,
                                      0x42, 0x43, 0x44, 0x45, 0x46, 0x47};
    constexpr netsec::Byte aad[] = {0x50, 0x51, 0x52, 0x53, 0xc0, 0xc1,
                                    0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7};
    const std::string plaintext =
        "Ladies and Gentlemen of the class of '99: If I could offer you only "
        "one tip for the future, sunscreen would be it.";
    const auto input = std::span<const netsec::Byte>(
        reinterpret_cast<const netsec::Byte*>(plaintext.data()),
        plaintext.size());
    const Bytes sealed = detail::aead_seal(key.bytes, span(nonce), span(aad),
                                           input);
    const std::string sealed_hex = to_hex(sealed);
    const std::string expected =
        "d31a8d34648e60db7b86afbc53ef7ec2a4aded51296e08fea9e2b5a736ee62d6"
        "3dbea45e8ca9671282fafb69da92728b1a71de0a9e060b2905d6a5b67ecd3b36"
        "92ddbd7f2d778b8c9803aee328091b58fab324e4fad675945585808b4831d7bc"
        "3ff4def08e4b7a9de576d26586cec64b6116"
        "1ae10b594f09e26a7e902ecbd0600691";
    if (sealed_hex != expected) {
      std::cerr << "actual AEAD: " << sealed_hex << '\n';
    }
    check(sealed_hex == expected, "ChaCha20-Poly1305 RFC 8439 vector");
    check(detail::aead_open(key.bytes, span(nonce), span(aad), sealed)
              .has_value(),
          "AEAD authenticated open");
    Bytes corrupted = sealed;
    corrupted[4] ^= 0x80;
    check(!detail::aead_open(key.bytes, span(nonce), span(aad), corrupted),
          "AEAD rejects ciphertext modification");
    check(!detail::aead_open(key.bytes, span(nonce), span(aad),
                             std::span<const netsec::Byte>(sealed).first(
                                 sealed.size() - 1)),
          "AEAD rejects truncation");
  }

  if (failures != 0) {
    std::cerr << failures << " crypto test(s) failed\n";
    return 1;
  }
  std::cout << "crypto tests passed\n";
  return 0;
}
