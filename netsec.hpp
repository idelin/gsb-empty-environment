#pragma once

#include <array>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <span>

namespace netsec {

// 触发清理的电源路径。
enum class SanitizeReason { shutdown, hibernate, reboot };

// 启动时内存完整性校验结果。
enum class BootStatus {
  fresh,               // 冷启动：无先前状态，区域已初始化
  verified,            // 校验通过，密钥区可信
  sanitized,           // 先前电源路径已清理（关机/休眠/重启），无密钥残留
  integrity_violation  // 校验失败：区域已整体清零并重新初始化
};

// 供设备启动组件调用的内存清理组件。
//
// 用法：
//   1. 启动路径：构造后先调用 boot_verify()，校验通过方可使用密钥区。
//   2. 关机/休眠/重启路径：调用 sanitize(reason)，清零密钥区并触发内存自刷新。
//      密钥不跨休眠保留——休眠恢复后所有密钥槽均为空，需重新注入。
class SecureKeyRegion {
public:
  static constexpr std::size_t key_size = 32;   // 单个密钥字节数
  static constexpr std::size_t max_keys = 8;    // 密钥槽数量

  // 内存控制器自刷新回调（硬件抽象，由启动组件注入）。
  using SelfRefreshHook = std::function<void()>;

  explicit SecureKeyRegion(SelfRefreshHook hook = {});

  // 启动时调用：校验内存完整性，然后才允许读写密钥。
  BootStatus boot_verify();

  // 密钥注入与读取。仅在 boot_verify() 之后、且槽位已占用时读取成功。
  bool store_key(std::uint8_t slot, std::span<const std::byte, key_size> key);
  bool read_key(std::uint8_t slot, std::span<std::byte, key_size> out) const;

  // 关机/休眠/重启路径：安全清零密钥区并触发内存自刷新。
  void sanitize(SanitizeReason reason);

  // 原始内存映像（模拟物理内存访问，供冷启动残留扫描与篡改测试）。
  std::span<const std::byte> raw_image() const;
  std::span<std::byte> raw_image();

  bool self_refresh_entered() const { return self_refresh_entered_; }

private:
  enum class RegionState : std::uint8_t { active = 0xA5, sanitized = 0x5A };

  struct alignas(16) Header {
    std::uint64_t magic;
    std::uint64_t checksum;
    std::uint32_t generation;
    RegionState state;
    std::array<std::byte, 7> reserved;
  };

  static constexpr std::uint64_t kMagic = 0x4E45545345434B59ULL;  // "NETSECKY"
  static constexpr std::array<std::byte, 16> kCanary = [] {
    std::array<std::byte, 16> c{};
    for (std::size_t i = 0; i < c.size(); ++i)
      c[i] = static_cast<std::byte>(0xC0 + i);
    return c;
  }();

  void initialize_empty(RegionState state);
  void update_checksum();
  bool checksum_valid() const;
  bool canaries_valid() const;
  static void secure_zero(void* ptr, std::size_t size) noexcept;
  static std::uint64_t fnv1a64(std::span<const std::byte> data) noexcept;

  SelfRefreshHook self_refresh_hook_;
  bool self_refresh_entered_ = false;
  bool boot_checked_ = false;

  Header header_{};
  std::array<std::byte, 16> canary_head_{};
  std::array<std::array<std::byte, key_size>, max_keys> keys_{};
  std::array<std::uint8_t, max_keys> occupied_{};
  std::array<std::byte, 16> canary_tail_{};
};

}  // namespace netsec
