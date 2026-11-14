#include "netsec.hpp"

#include <algorithm>
#include <array>
#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <cstring>
#include <deque>
#include <functional>
#include <iostream>
#include <memory>
#include <mutex>
#include <optional>
#include <span>
#include <string>
#include <thread>

namespace {

using netsec::Byte;
using netsec::Bytes;

int failures = 0;

void check(bool condition, const std::string& name) {
  if (!condition) {
    std::cerr << "FAIL: " << name << '\n';
    ++failures;
  }
}

netsec::SecretKey test_key(Byte seed) {
  std::array<Byte, 32> bytes{};
  bytes.fill(seed);
  return netsec::SecretKey::from_bytes(bytes);
}

struct MemoryLink {
  enum class Side { source, target };
  using Interceptor = std::function<bool(Side, Bytes&)>;

  explicit MemoryLink(Interceptor value = {})
      : interceptor(std::move(value)) {}

  bool send(Side from, std::span<const Byte> message) {
    std::lock_guard lock(mutex);
    if (closed) {
      return false;
    }
    Bytes copy(message.begin(), message.end());
    if (interceptor && !interceptor(from, copy)) {
      return true;
    }
    auto& destination =
        from == Side::source ? source_to_target : target_to_source;
    destination.push_back(std::move(copy));
    condition.notify_all();
    return true;
  }

  std::optional<Bytes> receive(Side side) {
    std::unique_lock lock(mutex);
    const auto ready = [&] {
      return closed ||
             (side == Side::source ? !target_to_source.empty()
                                   : !source_to_target.empty());
    };
    if (!condition.wait_for(lock, std::chrono::seconds(5), ready)) {
      return std::nullopt;
    }
    auto& queue = side == Side::source ? target_to_source : source_to_target;
    if (queue.empty()) {
      return std::nullopt;
    }
    Bytes message = std::move(queue.front());
    queue.pop_front();
    return message;
  }

  void close() {
    std::lock_guard lock(mutex);
    closed = true;
    condition.notify_all();
  }

  std::mutex mutex;
  std::condition_variable condition;
  std::deque<Bytes> source_to_target;
  std::deque<Bytes> target_to_source;
  bool closed{};
  Interceptor interceptor;
};

class LinkEndpoint : public netsec::MigrationChannel {
 public:
  LinkEndpoint(std::shared_ptr<MemoryLink> value, MemoryLink::Side side)
      : link_(std::move(value)), side_(side) {}

  bool send(std::span<const Byte> message) override {
    return link_->send(side_, message);
  }

  std::optional<Bytes> receive() override { return link_->receive(side_); }

  void close() { link_->close(); }

 private:
  std::shared_ptr<MemoryLink> link_;
  MemoryLink::Side side_;
};

class NullChannel : public netsec::MigrationChannel {
 public:
  bool send(std::span<const Byte>) override { return false; }
  std::optional<Bytes> receive() override { return std::nullopt; }
};

std::uint8_t wire_type(const Bytes& frame) {
  return frame.size() >= 6 ? frame[5] : 0;
}

netsec::SourceConfig source_config(const netsec::Endpoint& endpoint,
                                   const netsec::SecretKey& source_key,
                                   const netsec::SecretKey& target_key) {
  netsec::SourceConfig config;
  config.source_id = netsec::HostId{"source.example"};
  config.target_id = netsec::HostId{"target.example"};
  config.target_endpoint = endpoint;
  config.source_auth_key = source_key;
  config.target_auth_key = target_key;
  config.max_page_bytes = 4096;
  config.max_total_bytes = 1U << 20U;
  return config;
}

netsec::TargetConfig target_config(const netsec::Endpoint& endpoint,
                                   const netsec::SecretKey& source_key,
                                   const netsec::SecretKey& target_key) {
  netsec::TargetConfig config;
  config.source_id = netsec::HostId{"source.example"};
  config.target_id = netsec::HostId{"target.example"};
  config.endpoint = endpoint;
  config.source_auth_key = source_key;
  config.target_auth_key = target_key;
  config.max_page_bytes = 4096;
  config.max_total_bytes = 1U << 20U;
  return config;
}

std::vector<netsec::MemoryPage> pages(std::size_t count, std::size_t bytes) {
  std::vector<netsec::MemoryPage> result;
  for (std::size_t index = 0; index < count; ++index) {
    netsec::MemoryPage page;
    page.index = index;
    page.guest_address = 0x1000U + 0x1000U * index;
    page.data.resize(bytes);
    for (std::size_t offset = 0; offset < bytes; ++offset) {
      page.data[offset] =
          static_cast<Byte>((index * 37U + offset * 11U) & 0xffU);
    }
    result.push_back(std::move(page));
  }
  return result;
}

struct RunResult {
  netsec::MigrationResult source;
  netsec::TargetMigrationResult target;
  std::size_t page_frames{};
  std::size_t server_hello_frames{};
};

RunResult run_migration(netsec::MigrationSource& source,
                        netsec::MigrationTarget& target,
                        const netsec::MigrationRequest& request,
                        MemoryLink::Interceptor user_interceptor = {}) {
  std::size_t page_frames = 0;
  std::size_t server_hello_frames = 0;
  auto interceptor = [&](MemoryLink::Side from, Bytes& frame) {
    if (from == MemoryLink::Side::source && wire_type(frame) == 5) {
      ++page_frames;
    }
    if (from == MemoryLink::Side::target && wire_type(frame) == 2) {
      ++server_hello_frames;
    }
    return user_interceptor ? user_interceptor(from, frame) : true;
  };

  auto link = std::make_shared<MemoryLink>(interceptor);
  LinkEndpoint source_channel(link, MemoryLink::Side::source);
  LinkEndpoint target_channel(link, MemoryLink::Side::target);

  RunResult result;
  std::thread target_thread([&] {
    result.target = target.accept(target_channel);
    target_channel.close();
  });
  result.source = source.migrate(source_channel, request);
  source_channel.close();
  target_thread.join();
  result.page_frames = page_frames;
  result.server_hello_frames = server_hello_frames;
  return result;
}

netsec::MigrationRequest make_request(
    const netsec::MigrationTicket& ticket, const netsec::VmId& vm_id,
    std::uint64_t generation, std::vector<netsec::MemoryPage> memory_pages) {
  netsec::MigrationRequest request;
  request.vm_id = vm_id;
  request.generation = generation;
  request.ticket = ticket;
  request.pages = std::move(memory_pages);
  return request;
}

void test_legitimate_migration() {
  const netsec::Endpoint endpoint{"target.internal", 9443};
  const netsec::VmId vm_id{"vm-42"};
  constexpr std::uint64_t generation = 7;
  const auto source_key = test_key(0x11);
  const auto target_key = test_key(0x22);
  const auto authority = std::make_shared<netsec::TicketAuthority>();
  const netsec::TicketBinding binding{
      netsec::HostId{"source.example"}, netsec::HostId{"target.example"},
      vm_id, generation};
  const netsec::MigrationTicket ticket = authority->issue(binding);

  netsec::MigrationSource source(
      source_config(endpoint, source_key, target_key));
  netsec::MigrationTarget target(
      target_config(endpoint, source_key, target_key), authority);
  const auto memory_pages = pages(3, 1024);
  const netsec::MigrationRequest request =
      make_request(ticket, vm_id, generation, memory_pages);

  const RunResult result = run_migration(source, target, request);
  if (!result.source.ok() || !result.target.ok()) {
    std::cerr << "legitimate source error: " << result.source.detail << '\n';
    std::cerr << "legitimate target error: " << result.target.detail << '\n';
  }
  check(result.source.ok(), "legitimate source reports success");
  check(result.target.ok(), "legitimate target reports success");
  check(result.target.committed.has_value(),
        "legitimate target commits a memory image");
  check(result.page_frames == 3, "all memory pages cross the channel");
  if (result.target.committed) {
    check(result.target.committed->vm_id == vm_id,
          "committed image is bound to the requested VM");
    check(result.target.committed->generation == generation,
          "committed image is bound to the requested generation");
    check(result.target.committed->pages.size() == memory_pages.size(),
          "committed image has every page");
    bool equal = true;
    for (std::size_t i = 0; i < memory_pages.size() &&
                           i < result.target.committed->pages.size();
         ++i) {
      equal = equal &&
              result.target.committed->pages[i].guest_address ==
                  memory_pages[i].guest_address &&
              result.target.committed->pages[i].data == memory_pages[i].data;
    }
    check(equal, "committed page contents and guest addresses match");
    check(result.target.committed->manifest_checksum ==
              result.source.manifest_checksum,
          "source and target agree on the completion manifest");
  }
}

void test_route_rewrite_to_rogue_target() {
  const netsec::Endpoint endpoint{"target.internal", 9443};
  const netsec::VmId vm_id{"vm-route"};
  constexpr std::uint64_t generation = 8;
  const auto source_key = test_key(0x11);
  const auto real_target_key = test_key(0x22);
  const auto rogue_target_key = test_key(0x33);
  const auto real_authority = std::make_shared<netsec::TicketAuthority>();
  const netsec::TicketBinding binding{
      netsec::HostId{"source.example"}, netsec::HostId{"target.example"},
      vm_id, generation};
  const netsec::MigrationTicket ticket = real_authority->issue(binding);

  netsec::MigrationSource source(
      source_config(endpoint, source_key, real_target_key));

  // The rogue has valid source-facing credentials and claims the configured
  // address, but cannot authenticate as the real target or redeem its ticket.
  const auto rogue_authority = std::make_shared<netsec::TicketAuthority>();
  netsec::MigrationTarget rogue(
      target_config(endpoint, source_key, rogue_target_key), rogue_authority);
  const netsec::MigrationRequest request =
      make_request(ticket, vm_id, generation, pages(2, 512));

  const RunResult result = run_migration(source, rogue, request);
  check(!result.source.ok(), "rewritten route fails source authentication");
  check(!result.target.ok(), "rogue target cannot accept the session");
  check(!result.target.committed.has_value(),
        "no memory is committed on the rogue host");
  check(result.page_frames == 0,
        "no memory page is sent before the configured target authenticates");
  check(result.server_hello_frames == 0,
        "rogue cannot produce an authenticated server hello");
}

void test_ticket_reuse_is_rejected() {
  const netsec::Endpoint endpoint{"target.internal", 9443};
  const netsec::VmId vm_id{"vm-ticket"};
  constexpr std::uint64_t generation = 9;
  const auto source_key = test_key(0x11);
  const auto target_key = test_key(0x22);
  const auto authority = std::make_shared<netsec::TicketAuthority>();
  const netsec::TicketBinding binding{
      netsec::HostId{"source.example"}, netsec::HostId{"target.example"},
      vm_id, generation};
  const netsec::MigrationTicket ticket = authority->issue(binding);
  const auto memory_pages = pages(1, 256);

  netsec::MigrationSource source(
      source_config(endpoint, source_key, target_key));
  netsec::MigrationTarget target(
      target_config(endpoint, source_key, target_key), authority);
  const netsec::MigrationRequest request =
      make_request(ticket, vm_id, generation, memory_pages);

  const RunResult first = run_migration(source, target, request);
  check(first.source.ok() && first.target.ok(),
        "first use of the migration ticket succeeds");

  const RunResult second = run_migration(source, target, request);
  check(!second.source.ok(), "reused ticket fails at the source");
  check(!second.target.ok(), "reused ticket is rejected by the target");
  check(second.target.error == netsec::ErrorCode::ticket_consumed,
        "target reports one-time ticket reuse");
  check(!second.target.committed.has_value(),
        "reused ticket commits no memory");
  check(second.page_frames == 0, "reused ticket sends no memory pages");
}

void test_mitm_tamper_and_rollback() {
  const netsec::Endpoint endpoint{"target.internal", 9443};
  const netsec::VmId vm_id{"vm-mitm"};
  constexpr std::uint64_t generation = 10;
  const auto source_key = test_key(0x11);
  const auto target_key = test_key(0x22);
  const auto authority = std::make_shared<netsec::TicketAuthority>();
  const netsec::TicketBinding binding{
      netsec::HostId{"source.example"}, netsec::HostId{"target.example"},
      vm_id, generation};
  const netsec::MigrationTicket ticket = authority->issue(binding);

  netsec::MigrationSource source(
      source_config(endpoint, source_key, target_key));
  netsec::MigrationTarget target(
      target_config(endpoint, source_key, target_key), authority);
  const netsec::MigrationRequest request =
      make_request(ticket, vm_id, generation, pages(3, 768));

  std::size_t pages_seen = 0;
  const RunResult result = run_migration(
      source, target, request,
      [&](MemoryLink::Side from, Bytes& frame) {
        if (from == MemoryLink::Side::source && wire_type(frame) == 5) {
          ++pages_seen;
          if (pages_seen == 2 && frame.size() > 18) {
            frame[13] ^= 0x40U;
          }
        }
        return true;
      });

  check(!result.source.ok(), "source does not receive completion confirmation");
  check(!result.target.ok(), "target rejects the modified page record");
  check(result.target.error == netsec::ErrorCode::authentication_failed,
        "AEAD rejects the MITM page modification");
  check(!result.target.committed.has_value(),
        "partially staged pages are rolled back");
}

void test_mitm_server_hello_is_not_accepted() {
  const netsec::Endpoint endpoint{"target.internal", 9443};
  const netsec::VmId vm_id{"vm-server-mitm"};
  constexpr std::uint64_t generation = 11;
  const auto source_key = test_key(0x11);
  const auto target_key = test_key(0x22);
  const auto authority = std::make_shared<netsec::TicketAuthority>();
  const netsec::TicketBinding binding{
      netsec::HostId{"source.example"}, netsec::HostId{"target.example"},
      vm_id, generation};
  const netsec::MigrationTicket ticket = authority->issue(binding);

  netsec::MigrationSource source(
      source_config(endpoint, source_key, target_key));
  netsec::MigrationTarget target(
      target_config(endpoint, source_key, target_key), authority);
  const netsec::MigrationRequest request =
      make_request(ticket, vm_id, generation, pages(2, 512));

  bool tampered = false;
  const RunResult result = run_migration(
      source, target, request,
      [&](MemoryLink::Side from, Bytes& frame) {
        if (from == MemoryLink::Side::target && wire_type(frame) == 2 &&
            !tampered) {
          tampered = true;
          frame.back() ^= 0x01U;
        }
        return true;
      });

  check(tampered, "MITM observed the server hello");
  check(!result.source.ok(), "source rejects a forged server hello");
  check(result.source.error == netsec::ErrorCode::authentication_failed,
        "server hello proof failure is authenticated");
  check(!result.target.ok(), "target cannot complete without key confirmation");
  check(!result.target.committed.has_value(),
        "no memory is committed after server hello tampering");
  check(result.page_frames == 0, "source sends no pages to the MITM");
}

void test_binding_and_checksum_logic() {
  const netsec::VmId vm_id{"vm-checksum"};
  constexpr std::uint64_t generation = 12;
  netsec::MemoryPage page{1, 0x2000, Bytes{1, 2, 3, 4}};
  const netsec::Digest original =
      netsec::compute_page_checksum(vm_id, generation, page);
  page.guest_address = 0x3000;
  const netsec::Digest rewritten =
      netsec::compute_page_checksum(vm_id, generation, page);
  check(original != rewritten,
        "page checksum binds the guest address as well as contents");

  const auto source_key = test_key(0x11);
  const auto target_key = test_key(0x22);
  const netsec::Endpoint endpoint{"target.internal", 9443};
  const auto authority = std::make_shared<netsec::TicketAuthority>();
  const netsec::VmId ticket_vm{"vm-bound"};
  const netsec::TicketBinding binding{
      netsec::HostId{"source.example"}, netsec::HostId{"target.example"},
      ticket_vm, 13};
  const netsec::MigrationTicket ticket = authority->issue(binding);
  netsec::MigrationSource source(
      source_config(endpoint, source_key, target_key));
  netsec::MigrationRequest request =
      make_request(ticket, ticket_vm, 14, pages(1, 64));
  NullChannel channel;
  const netsec::MigrationResult result = source.migrate(channel, request);
  check(result.error == netsec::ErrorCode::invalid_request,
        "generation substitution is rejected before network use");
  check(result.detail.find("bound") != std::string::npos,
        "binding failure explains the ticket mismatch");
}

}  // namespace

int main() {
  test_legitimate_migration();
  test_route_rewrite_to_rogue_target();
  test_ticket_reuse_is_rejected();
  test_mitm_tamper_and_rollback();
  test_mitm_server_hello_is_not_accepted();
  test_binding_and_checksum_logic();

  if (failures != 0) {
    std::cerr << failures << " protocol test(s) failed\n";
    return 1;
  }
  std::cout << "protocol tests passed\n";
  return 0;
}
