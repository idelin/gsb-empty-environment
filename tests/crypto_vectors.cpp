#include "../netsec.cpp"

#include <iostream>
#include <string>
#include <string_view>

namespace {

int g_failures = 0;

void check(bool condition, const char* name) {
  if (!condition) {
    ++g_failures;
    std::cerr << "crypto vector failed: " << name << '\n';
  }
}

netsec::Bytes hex(std::string_view text) {
  const auto value = netsec::from_hex(text);
  return value.has_value() ? *value : netsec::Bytes{};
}

}  // namespace

int main() {
  using namespace netsec;

  check(sha256_hex(hex("")) ==
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        "SHA-256 empty");
  check(sha256_hex(hex("616263")) ==
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
        "SHA-256 abc");

  const Bytes hmac_key(20, 0x0bU);
  const Bytes hmac_data{0x48U, 0x69U, 0x20U, 0x54U,
                        0x68U, 0x65U, 0x72U, 0x65U};
  check(to_hex(hmac_sha256(hmac_key, hmac_data)) ==
            "b0344c61d8db38535ca8afceaf0bf12b881dc200c9833da726e9376c2e32cff7",
        "HMAC-SHA-256 RFC 4231 case 1");

  const Bytes ikm(22, 0x0bU);
  const auto hkdf = hkdf_sha256(
      ikm, hex("000102030405060708090a0b0c"),
      hex("f0f1f2f3f4f5f6f7f8f9"), 42);
  check(to_hex(hkdf) ==
            "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf"
            "34007208d5b887185865",
        "HKDF-SHA-256 RFC 5869 case 1");

  const Bytes chacha_key = hex(
      "000102030405060708090a0b0c0d0e0f"
      "101112131415161718191a1b1c1d1e1f");
  const Bytes chacha_nonce = hex("000000000000004a00000000");
  const std::string plaintext =
      "Ladies and Gentlemen of the class of '99: If I could offer you only "
      "one tip for the future, sunscreen would be it.";
  const Bytes input(plaintext.begin(), plaintext.end());
  const Bytes ciphertext = chacha20_xor(chacha_key, chacha_nonce, 1U, input);
  check(to_hex(std::span<const std::uint8_t>(ciphertext).first(16U)) ==
            "6e2e359a2568f98041ba0728dd0d6981",
        "ChaCha20 RFC 8439 section 2.4.2");

  return g_failures == 0 ? 0 : 1;
}
