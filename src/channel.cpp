#include "rdpauth/channel.hpp"

#include "rdpauth/crypto.hpp"

#include <cstring>

namespace rdp::auth {
namespace {

void append_u32(std::vector<std::uint8_t>& out, std::uint32_t value) {
    for (int i = 3; i >= 0; --i) {
        out.push_back(static_cast<std::uint8_t>(value >> (i * 8)));
    }
}

void append_u64(std::vector<std::uint8_t>& out, std::uint64_t value) {
    for (int i = 7; i >= 0; --i) {
        out.push_back(static_cast<std::uint8_t>(value >> (i * 8)));
    }
}

} // namespace

SecureChannel::SecureChannel(SessionBinding binding, Hash256 session_key)
    : binding_(binding), session_key_(session_key) {}

void SecureChannel::send(std::span<const std::uint8_t> plaintext) {
    // Keystream: HMAC(key, "rdp-auth/stream" || frame || block) per 32 bytes.
    std::vector<std::uint8_t> ciphertext(plaintext.size());
    for (std::size_t offset = 0, block = 0; offset < plaintext.size(); offset += 32, ++block) {
        std::vector<std::uint8_t> seed;
        const std::string label = "rdp-auth/stream/v1";
        seed.insert(seed.end(), label.begin(), label.end());
        append_u64(seed, frame_counter_);
        append_u64(seed, block);
        const Hash256 stream = hmac_sha256(session_key_, seed);
        const std::size_t take = std::min<std::size_t>(32, plaintext.size() - offset);
        for (std::size_t i = 0; i < take; ++i) {
            ciphertext[offset + i] = plaintext[offset + i] ^ stream[i];
        }
    }

    // Frame tag: HMAC(key, "rdp-auth/frame" || frame || len || ciphertext).
    std::vector<std::uint8_t> tag_input;
    const std::string tag_label = "rdp-auth/frame/v1";
    tag_input.insert(tag_input.end(), tag_label.begin(), tag_label.end());
    append_u64(tag_input, frame_counter_);
    append_u32(tag_input, static_cast<std::uint32_t>(ciphertext.size()));
    tag_input.insert(tag_input.end(), ciphertext.begin(), ciphertext.end());
    const Hash256 tag = hmac_sha256(session_key_, tag_input);

    // Wire frame: length || ciphertext || tag. No cleartext payload bytes.
    append_u32(wire_log_, static_cast<std::uint32_t>(ciphertext.size()));
    wire_log_.insert(wire_log_.end(), ciphertext.begin(), ciphertext.end());
    wire_log_.insert(wire_log_.end(), tag.begin(), tag.end());
    ++frame_counter_;
}

void SecureChannel::send_credentials(std::string_view username, std::string_view password) {
    std::vector<std::uint8_t> payload;
    append_u32(payload, static_cast<std::uint32_t>(username.size()));
    payload.insert(payload.end(), username.begin(), username.end());
    append_u32(payload, static_cast<std::uint32_t>(password.size()));
    payload.insert(payload.end(), password.begin(), password.end());
    send(payload);
}

} // namespace rdp::auth
