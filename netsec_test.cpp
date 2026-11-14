#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <cassert>
#include <cstdio>
#include <span>

namespace {

using netsec::BootStatus;
using netsec::SanitizeReason;
using netsec::SecureKeyRegion;

constexpr std::size_t kKeySize = SecureKeyRegion::key_size;

std::array<std::byte, kKeySize> make_key(std::byte seed) {
  std::array<std::byte, kKeySize> key{};
  for (std::size_t i = 0; i < key.size(); ++i)
    key[i] = seed ^ static_cast<std::byte>(i * 7 + 1);
  return key;
}

// 在原始内存映像中搜索密钥字节序列（模拟冷启动残留读取）。
bool image_contains(std::span<const std::byte> image,
                    const std::array<std::byte, kKeySize>& key) {
  return std::search(image.begin(), image.end(), key.begin(), key.end()) !=
         image.end();
}

// 场景 1：冷启动读取——启动校验后注入密钥并可读回。
void test_cold_boot_read() {
  SecureKeyRegion region;
  assert(region.boot_verify() == BootStatus::fresh);

  const auto key = make_key(std::byte{0x11});
  assert(region.store_key(0, key));

  std::array<std::byte, kKeySize> out{};
  assert(region.read_key(0, out));
  assert(out == key);
  std::puts("ok: cold boot read");
}

// 场景 2：休眠恢复——密钥不跨休眠保留，恢复后槽位为空且无残留。
void test_hibernate_resume() {
  bool self_refresh = false;
  SecureKeyRegion region([&] { self_refresh = true; });
  assert(region.boot_verify() == BootStatus::fresh);

  const auto key = make_key(std::byte{0x22});
  assert(region.store_key(1, key));

  region.sanitize(SanitizeReason::hibernate);
  assert(self_refresh && region.self_refresh_entered());
  assert(!image_contains(region.raw_image(), key));

  // 休眠恢复：启动组件重新校验。
  assert(region.boot_verify() == BootStatus::sanitized);
  std::array<std::byte, kKeySize> out{};
  assert(!region.read_key(1, out));
  assert(!image_contains(region.raw_image(), key));
  std::puts("ok: hibernate resume");
}

// 场景 3：合法重启——重启路径清零密钥区并触发自刷新，重启后无残留。
void test_legitimate_reboot() {
  bool self_refresh = false;
  SecureKeyRegion region([&] { self_refresh = true; });
  assert(region.boot_verify() == BootStatus::fresh);

  const auto key = make_key(std::byte{0x33});
  assert(region.store_key(2, key));

  region.sanitize(SanitizeReason::reboot);
  assert(self_refresh);
  assert(!image_contains(region.raw_image(), key));

  // 重启后启动校验：识别为已清理状态，密钥不可读。
  assert(region.boot_verify() == BootStatus::sanitized);
  std::array<std::byte, kKeySize> out{};
  assert(!region.read_key(2, out));

  // 重启后可重新注入密钥正常使用。
  const auto new_key = make_key(std::byte{0x44});
  assert(region.store_key(0, new_key));
  assert(region.read_key(0, out));
  assert(out == new_key);
  std::puts("ok: legitimate reboot");
}

// 场景 4：关机路径——与重启一致，清零并触发自刷新。
void test_shutdown() {
  bool self_refresh = false;
  SecureKeyRegion region([&] { self_refresh = true; });
  assert(region.boot_verify() == BootStatus::fresh);

  const auto key = make_key(std::byte{0x55});
  assert(region.store_key(3, key));

  region.sanitize(SanitizeReason::shutdown);
  assert(self_refresh);
  assert(!image_contains(region.raw_image(), key));
  assert(region.boot_verify() == BootStatus::sanitized);
  std::puts("ok: shutdown");
}

// 场景 5：完整性验证——篡改内存映像后启动校验检出并整体清零，密钥不残留。
void test_integrity_verification() {
  SecureKeyRegion region;
  assert(region.boot_verify() == BootStatus::fresh);

  const auto key = make_key(std::byte{0x66});
  assert(region.store_key(0, key));

  // 模拟攻击者篡改/移植内存：翻转映像中间一个字节。
  auto image = region.raw_image();
  image[image.size() / 2] ^= std::byte{0xFF};

  assert(region.boot_verify() == BootStatus::integrity_violation);
  assert(!image_contains(region.raw_image(), key));

  // 校验失败后区域已重新初始化，可安全使用。
  std::array<std::byte, kKeySize> out{};
  assert(!region.read_key(0, out));
  std::puts("ok: integrity verification");
}

// 场景 6：未经启动校验禁止使用密钥区。
void test_no_use_before_boot_verify() {
  SecureKeyRegion region;
  const auto key = make_key(std::byte{0x77});
  assert(!region.store_key(0, key));
  std::array<std::byte, kKeySize> out{};
  assert(!region.read_key(0, out));
  std::puts("ok: no use before boot verify");
}

}  // namespace

int main() {
  test_cold_boot_read();
  test_hibernate_resume();
  test_legitimate_reboot();
  test_shutdown();
  test_integrity_verification();
  test_no_use_before_boot_verify();
  std::puts("all tests passed");
  return 0;
}
