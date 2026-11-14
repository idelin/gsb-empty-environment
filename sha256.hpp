#pragma once

// 自包含 SHA-256 / HMAC-SHA-256（无外部依赖，便于固件环境集成）。
// 生产环境建议替换为平台加密库或硬件加速实现，接口保持不变。

#include <array>
#include <cstddef>
#include <cstdint>

namespace netsec {

class Sha256 {
public:
    Sha256();
    void update(const std::uint8_t* data, std::size_t len);
    std::array<std::uint8_t, 32> finish();

private:
    void compress(const std::uint8_t* block);

    std::array<std::uint32_t, 8> state_;
    std::array<std::uint8_t, 64> buffer_{};
    std::size_t buffer_len_ = 0;
    std::uint64_t total_len_ = 0;
};

std::array<std::uint8_t, 32> sha256(const std::uint8_t* data, std::size_t len);

std::array<std::uint8_t, 32> hmac_sha256(const std::uint8_t* key, std::size_t key_len,
                                         const std::uint8_t* data, std::size_t data_len);

} // namespace netsec
