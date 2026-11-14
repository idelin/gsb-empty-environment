#include "netsec.hpp"

#include <atomic>
#include <cstring>
#include <utility>

namespace netsec {

SecureKeyRegion::SecureKeyRegion(SelfRefreshHook hook)
    : self_refresh_hook_(std::move(hook)) {
  // 构造时不假设内存内容可信（模拟冷启动后 DRAM 残留），
  // 由 boot_verify() 判定并初始化。
}

void SecureKeyRegion::secure_zero(void* ptr, std::size_t size) noexcept {
  // 使用 volatile 写防止编译器优化掉清零操作，并加编译屏障。
  auto* p = static_cast<volatile std::byte*>(ptr);
  for (std::size_t i = 0; i < size; ++i) p[i] = std::byte{0};
  std::atomic_signal_fence(std::memory_order_seq_cst);
}

std::uint64_t SecureKeyRegion::fnv1a64(std::span<const std::byte> data) noexcept {
  std::uint64_t hash = 14695981039346656037ULL;
  for (std::byte b : data) {
    hash ^= static_cast<std::uint8_t>(b);
    hash *= 1099511628211ULL;
  }
  return hash;
}

void SecureKeyRegion::update_checksum() {
  // 校验和覆盖：状态、代数、密钥区与占用位图、尾部金丝雀。
  std::uint64_t hash = 14695981039346656037ULL;
  auto mix = [&hash](std::span<const std::byte> d) {
    for (std::byte b : d) {
      hash ^= static_cast<std::uint8_t>(b);
      hash *= 1099511628211ULL;
    }
  };
  const std::uint8_t state = static_cast<std::uint8_t>(header_.state);
  const std::uint32_t gen = header_.generation;
  mix(std::span<const std::byte>(reinterpret_cast<const std::byte*>(&state), 1));
  mix(std::span<const std::byte>(reinterpret_cast<const std::byte*>(&gen), sizeof gen));
  mix(std::span<const std::byte>(canary_head_.data(), canary_head_.size()));
  mix(std::span<const std::byte>(keys_.data()->data(), keys_.size() * key_size));
  mix(std::span<const std::byte>(
      reinterpret_cast<const std::byte*>(occupied_.data()), occupied_.size()));
  mix(std::span<const std::byte>(canary_tail_.data(), canary_tail_.size()));
  header_.checksum = hash;
}

bool SecureKeyRegion::checksum_valid() const {
  auto copy = *this;
  copy.update_checksum();
  return copy.header_.checksum == header_.checksum;
}

bool SecureKeyRegion::canaries_valid() const {
  return canary_head_ == kCanary && canary_tail_ == kCanary;
}

void SecureKeyRegion::initialize_empty(RegionState state) {
  secure_zero(keys_.data()->data(), keys_.size() * key_size);
  secure_zero(occupied_.data(), occupied_.size());
  canary_head_ = kCanary;
  canary_tail_ = kCanary;
  header_.magic = kMagic;
  header_.state = state;
  header_.generation += 1;
  header_.reserved.fill(std::byte{0});
  update_checksum();
}

BootStatus SecureKeyRegion::boot_verify() {
  boot_checked_ = true;

  // 冷启动：无有效魔数，整体清零后初始化。
  if (header_.magic != kMagic) {
    secure_zero(&header_, sizeof header_);
    initialize_empty(RegionState::active);
    return BootStatus::fresh;
  }

  // 先前电源路径已清理：密钥区已清零，直接重新初始化为空区域。
  if (header_.state == RegionState::sanitized && checksum_valid() &&
      canaries_valid()) {
    initialize_empty(RegionState::active);
    return BootStatus::sanitized;
  }

  // 完整性校验失败（篡改/损坏/残留）：整体清零，拒绝使用任何残留数据。
  if (header_.state != RegionState::active || !checksum_valid() ||
      !canaries_valid()) {
    secure_zero(&header_, sizeof header_);
    initialize_empty(RegionState::active);
    return BootStatus::integrity_violation;
  }

  return BootStatus::verified;
}

bool SecureKeyRegion::store_key(std::uint8_t slot,
                                std::span<const std::byte, key_size> key) {
  if (!boot_checked_ || slot >= max_keys) return false;
  std::memcpy(keys_[slot].data(), key.data(), key_size);
  occupied_[slot] = 1;
  update_checksum();
  return true;
}

bool SecureKeyRegion::read_key(std::uint8_t slot,
                               std::span<std::byte, key_size> out) const {
  if (!boot_checked_ || slot >= max_keys || occupied_[slot] != 1) return false;
  std::memcpy(out.data(), keys_[slot].data(), key_size);
  return true;
}

void SecureKeyRegion::sanitize(SanitizeReason /*reason*/) {
  // 三种电源路径（关机/休眠/重启）执行相同的清理：
  // 密钥不跨休眠保留，也不跨关机/重启保留。
  initialize_empty(RegionState::sanitized);

  // 触发内存控制器自刷新，加速 DRAM 电荷衰减，压缩冷启动攻击窗口。
  if (self_refresh_hook_) self_refresh_hook_();
  self_refresh_entered_ = true;
}

std::span<const std::byte> SecureKeyRegion::raw_image() const {
  const auto* base = reinterpret_cast<const std::byte*>(&header_);
  const auto* end =
      reinterpret_cast<const std::byte*>(&canary_tail_) + sizeof(canary_tail_);
  return {base, static_cast<std::size_t>(end - base)};
}

std::span<std::byte> SecureKeyRegion::raw_image() {
  auto* base = reinterpret_cast<std::byte*>(&header_);
  auto* end = reinterpret_cast<std::byte*>(&canary_tail_) + sizeof(canary_tail_);
  return {base, static_cast<std::size_t>(end - base)};
}

}  // namespace netsec
