#pragma once

// tlpool::Pool<T> —— 带线程本地归还缓存的对象池（C++17）。
//
// 所有权模型（所有权图见 README.md）：
//   * 所有对象仅由 Core 控制块析构，且恰好析构一次。
//   * Core 采用 shared 所有权：Pool 句柄、每个线程缓存、每个外借 Lease
//     都持有 shared_ptr<Core>，因此不要求 Pool 比任何线程或 Lease 活得久。
//   * 任一时刻，每个 T* 恰好属于 {Lease, ThreadCache.items, Core.stash}
//     三者之一，所有权随指针移动而唯一转移。
//
// 协议：
//   * 归还：Lease 的 deleter 优先进入本线程缓存；缓存达到 high 阈值时，
//     一次性把 high-keep 个对象批量刷回 Core（单次加锁）。
//   * 线程退出：thread_local Sentry 析构时冲刷本线程全部缓存并置 retired；
//     此后（TLS 析构阶段的迟归还）deleter 直接交回 Core，批次不遗失。
//   * 池关闭：close() 后 lease() 返回空；deleter 绕过缓存直接交回 Core，
//     保证对象及时回到全局池等待最终统一析构。
//   * 池先死：Core 被缓存 / Lease 中的 shared_ptr 续命，最后一个引用
//     消失时 ~Core 统一析构 stash 中的全部对象。

#include <cstddef>
#include <functional>
#include <memory>
#include <mutex>
#include <unordered_map>
#include <utility>
#include <vector>

#if defined(__has_feature)
#if __has_feature(address_sanitizer)
#define TLPOOL_ASAN 1
#endif
#endif
#if defined(__SANITIZE_ADDRESS__)
#define TLPOOL_ASAN 1
#endif
#if !defined(TLPOOL_ASAN)
#define TLPOOL_ASAN 0
#endif
#if TLPOOL_ASAN
#include <sanitizer/lsan_interface.h>
#endif

namespace tlpool {

template <typename T>
class Pool {
  // 控制块：全局池的本体，也是所有对象的唯一析构点。
  struct Core {
    mutable std::mutex mu;
    std::vector<T*> stash;            // Core 拥有的空闲对象
    bool closed = false;              // 池关闭后不再借出
    std::size_t cache_high = 32;      // 线程缓存批量回刷阈值
    std::size_t cache_keep = 8;       // 批量回刷后保留的热对象数
    std::function<void(T*)> destroy;  // 唯一的对象销毁入口

    Core() = default;
    Core(const Core&) = delete;
    Core& operator=(const Core&) = delete;
    ~Core() {  // 每个对象恰好在这里析构一次
      for (T* p : stash) destroy(p);
    }

    T* try_pop() {
      std::lock_guard<std::mutex> lk(mu);
      if (stash.empty()) return nullptr;
      T* p = stash.back();
      stash.pop_back();
      return p;
    }
    void retire_batch(T** items, std::size_t n) {
      if (n == 0) return;
      std::lock_guard<std::mutex> lk(mu);
      stash.insert(stash.end(), items, items + n);
    }
    bool is_closed() const {
      std::lock_guard<std::mutex> lk(mu);
      return closed;
    }
  };

  // 单线程归还缓存：无锁，仅属主线程可触。
  struct Cache {
    std::shared_ptr<Core> core;  // 为 Core 续命：线程退出时 Pool 可能已销毁
    std::vector<T*> items;

    Cache() = default;
    explicit Cache(std::shared_ptr<Core> c) : core(std::move(c)) {}
    Cache(Cache&&) = default;
    Cache& operator=(Cache&&) = default;
    Cache(const Cache&) = delete;
    Cache& operator=(const Cache&) = delete;
    ~Cache() { flush_all(); }

    void return_one(T* p) {
      items.push_back(p);
      if (items.size() >= core->cache_high) flush_batch();
    }
    // 攒到阈值后批量交回全局池：一次加锁移动 high-keep 个。
    void flush_batch() {
      const std::size_t keep = core->cache_keep;
      const std::size_t n = items.size() > keep ? items.size() - keep : 0;
      if (n == 0) return;
      core->retire_batch(items.data(), n);
      items.erase(items.begin(), items.begin() + static_cast<std::ptrdiff_t>(n));
    }
    void flush_all() {
      if (!core || items.empty()) return;
      core->retire_batch(items.data(), items.size());
      items.clear();
    }
  };

  // 线程本地槽位。有意做成永不析构（每线程一次的有界泄漏）：
  // TLS 对象的析构顺序不可控，Lease 的 deleter 可能在线程退出的任意
  // 阶段执行，必须保证槽位内存始终可安全访问。槽位内容（缓存的对象）
  // 由 Sentry 在线程退出时全部交还 Core，泄漏的只是一个空壳结构体。
  struct TlsSlot {
    std::unordered_map<Core*, Cache> caches;
    bool retired = false;  // Sentry 已冲刷；此后的归还直接进 Core
  };

  // 线程退出协议：Sentry 析构时把本线程所有缓存整批刷回各自 Core。
  struct Sentry {
    ~Sentry() {
      TlsSlot& slot = tls_slot();
      for (auto& kv : slot.caches) kv.second.flush_all();
      slot.caches.clear();
      slot.retired = true;
    }
  };

  static TlsSlot& tls_slot() {
    thread_local TlsSlot* slot = [] {
      TlsSlot* s = new TlsSlot();
#if TLPOOL_ASAN
      __lsan_ignore_object(s);  // 有意的常驻结构，非泄漏
#endif
      return s;
    }();
    return *slot;
  }
  static void ensure_sentry() {
    thread_local Sentry sentry;  // 首次归还时建立，线程退出时冲刷
    (void)sentry;
  }

 public:
  // Lease 的 deleter：持有 shared_ptr<Core>，因此 Lease 可以比
  // Pool 句柄活得更久（迟归还协议的基础）。
  struct LeaseDeleter {
    std::shared_ptr<Core> core;
    void operator()(T* p) const noexcept {
      if (!p) return;
      Core* c = core.get();
      TlsSlot& slot = tls_slot();
      if (!slot.retired && !c->is_closed()) {
        ensure_sentry();
        auto it = slot.caches.find(c);
        if (it == slot.caches.end())
          it = slot.caches.emplace(c, Cache(core)).first;
        it->second.return_one(p);
      } else {
        // 池已关闭或本线程缓存已退役：绕过缓存，直接交回 Core。
        c->retire_batch(&p, 1);
      }
    }
  };

  using Lease = std::unique_ptr<T, LeaseDeleter>;

  explicit Pool(std::function<T*()> factory,
                std::function<void(T*)> destroy = [](T* p) { delete p; },
                std::size_t cache_high = 32, std::size_t cache_keep = 8)
      : factory_(std::move(factory)), core_(std::make_shared<Core>()) {
    core_->destroy = std::move(destroy);
    core_->cache_high = cache_high == 0 ? 1 : cache_high;
    core_->cache_keep =
        cache_keep < core_->cache_high ? cache_keep : core_->cache_high - 1;
  }
  Pool(const Pool&) = delete;
  Pool& operator=(const Pool&) = delete;
  ~Pool() = default;  // 不销毁对象：Core 由 shared 所有权续命

  // 借出对象；池已关闭时返回空 Lease。
  Lease lease() {
    if (core_->is_closed()) return Lease{};
    TlsSlot& slot = tls_slot();
    if (!slot.retired) {  // 1) 本线程缓存（无锁热路径）
      auto it = slot.caches.find(core_.get());
      if (it != slot.caches.end() && !it->second.items.empty()) {
        T* p = it->second.items.back();
        it->second.items.pop_back();
        return Lease(p, LeaseDeleter{core_});
      }
    }
    if (T* p = core_->try_pop())  // 2) 全局 stash（一次加锁）
      return Lease(p, LeaseDeleter{core_});
    return Lease(factory_(), LeaseDeleter{core_});  // 3) 新建
  }

  // 关闭池：不再借出；迟归还的对象绕过缓存直接回 Core。
  void close() {
    std::lock_guard<std::mutex> lk(core_->mu);
    core_->closed = true;
  }
  bool closed() const { return core_->is_closed(); }

  // 冲刷当前线程中属于本池的缓存（类似 tcmalloc 的 ReleaseToSystem）。
  void flush_thread_cache() {
    TlsSlot& slot = tls_slot();
    auto it = slot.caches.find(core_.get());
    if (it != slot.caches.end()) it->second.flush_all();
  }

  // 观测接口（测试 / 监控用）。
  std::size_t stash_size() const {
    std::lock_guard<std::mutex> lk(core_->mu);
    return core_->stash.size();
  }
  std::weak_ptr<Core> core_handle() const { return core_; }

 private:
  std::function<T*()> factory_;
  std::shared_ptr<Core> core_;
};

}  // namespace tlpool
