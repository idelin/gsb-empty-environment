// Intrusive MPMC queue with caller-owned nodes, explicit read-side
// protection (hazard pointers) and deferred reclamation.
//
// Node lifetime contract:
//   - Nodes are allocated, owned and eventually destroyed by the caller.
//   - A node returned by dequeue() is unlinked from the queue, but other
//     threads may still hold protected references to it. The caller must
//     NOT destroy it directly; instead hand it to retire(node, deleter).
//   - reclaim() invokes the deleter only once no hazard pointer in the
//     domain references the node, i.e. no reader can still observe it.
//
// Close semantics:
//   - After close(), enqueue() fails (returns false) for any enqueue that
//     starts after close() returns. Nodes already linked remain dequeueable
//     until the queue is drained.
//
// Sentinel semantics (Michael-Scott style): the queue always retains one
// node as sentinel. dequeue() unlinks and returns the *previous* sentinel;
// the logically last node stays inside as the final sentinel. After close()
// and a drained (nullptr-returning) dequeue(), take_sentinel() hands that
// last node back, so every node the caller ever linked is recoverable.
#pragma once

#include <array>
#include <atomic>
#include <cassert>
#include <cstddef>
#include <mutex>
#include <vector>

namespace iq {

struct node {
  std::atomic<node*> next{nullptr};
};

// Fixed-size hazard pointer domain. Each protected reference occupies one
// slot for the duration of a read_guard.
class hazard_domain {
public:
  static constexpr std::size_t kMaxSlots = 128;

  struct slot {
    std::atomic<const void*> ptr{nullptr};
    std::atomic<bool> used{false};
  };

  slot* acquire_slot() {
    for (auto& s : slots_) {
      bool expected = false;
      if (s.used.compare_exchange_strong(expected, true,
                                         std::memory_order_acq_rel)) {
        s.ptr.store(nullptr, std::memory_order_relaxed);
        return &s;
      }
    }
    assert(false && "hazard_domain: out of slots");
    return nullptr;
  }

  void release_slot(slot* s) {
    s->ptr.store(nullptr, std::memory_order_release);
    s->used.store(false, std::memory_order_release);
  }

  bool is_hazardous(const void* p) const {
    for (const auto& s : slots_) {
      if (s.used.load(std::memory_order_acquire) &&
          s.ptr.load(std::memory_order_acquire) == p) {
        return true;
      }
    }
    return false;
  }

private:
  std::array<slot, kMaxSlots> slots_;
};

// RAII read-side protection. While the guard holds a protected pointer,
// reclamation of that node is deferred.
class read_guard {
public:
  explicit read_guard(hazard_domain& dom) : dom_(dom), slot_(dom.acquire_slot()) {}
  ~read_guard() { dom_.release_slot(slot_); }

  read_guard(const read_guard&) = delete;
  read_guard& operator=(const read_guard&) = delete;

  // Protect the pointer currently stored in `src` and return it.
  // Re-validates after publishing so the returned pointer is guaranteed
  // to still be reachable from `src` (hence not yet reclaimed).
  template <typename T>
  T* protect(const std::atomic<T*>& src) {
    T* p;
    do {
      p = src.load(std::memory_order_acquire);
      slot_->ptr.store(p, std::memory_order_release);
    } while (src.load(std::memory_order_acquire) != p);
    return p;
  }

  void reset() { slot_->ptr.store(nullptr, std::memory_order_release); }

private:
  hazard_domain& dom_;
  hazard_domain::slot* slot_;
};

class intrusive_queue {
public:
  using deleter_fn = void (*)(node*);

  // `dummy` is a caller-owned node that acts as the initial sentinel.
  explicit intrusive_queue(node* dummy, hazard_domain& dom) : dom_(dom) {
    head_.store(dummy, std::memory_order_relaxed);
    tail_.store(dummy, std::memory_order_relaxed);
  }

  // Link a caller-owned node at the tail. Fails once the queue is closed.
  bool enqueue(node* n) {
    if (closed_.load(std::memory_order_acquire)) return false;
    n->next.store(nullptr, std::memory_order_relaxed);
    node* prev = tail_.exchange(n, std::memory_order_acq_rel);
    prev->next.store(n, std::memory_order_release);
    return true;
  }

  // Unlink and return the node at the head (the previous sentinel), or
  // nullptr if the queue is (momentarily) empty. The returned node may
  // still be observed by other threads; hand it to retire().
  node* dequeue() {
    hazard_domain::slot* s = dom_.acquire_slot();
    node* h;
    for (;;) {
      h = head_.load(std::memory_order_acquire);
      s->ptr.store(h, std::memory_order_release);
      if (head_.load(std::memory_order_acquire) != h) continue;
      node* n = h->next.load(std::memory_order_acquire);
      if (n == nullptr) {  // empty (or an enqueue is mid-link)
        h = nullptr;
        break;
      }
      if (head_.compare_exchange_weak(h, n, std::memory_order_acq_rel,
                                      std::memory_order_acquire)) {
        break;  // h unlinked; n becomes the new sentinel
      }
    }
    s->ptr.store(nullptr, std::memory_order_release);
    dom_.release_slot(s);
    return h;
  }

  // Raw snapshot of the head pointer, for use with read_guard::protect.
  const std::atomic<node*>& head_ref() const { return head_; }

  read_guard make_guard() { return read_guard(dom_); }

  void close() { closed_.store(true, std::memory_order_release); }
  bool closed() const { return closed_.load(std::memory_order_acquire); }

  // Recover the final sentinel. Precondition: the queue is closed and
  // drained (dequeue() returned nullptr). The queue must not be used
  // afterwards.
  node* take_sentinel() const {
    assert(closed_.load(std::memory_order_acquire) && "queue must be closed");
    node* h = head_.load(std::memory_order_acquire);
    assert(h->next.load(std::memory_order_acquire) == nullptr &&
           "queue not drained");
    return h;
  }

  // Defer destruction of an unlinked node until no reader references it.
  void retire(node* n, deleter_fn deleter) {
    std::lock_guard<std::mutex> lk(retire_mu_);
    retired_.push_back({n, deleter});
  }

  // Invoke deleters for retired nodes that no hazard pointer references.
  // Returns the number of reclaimed nodes.
  std::size_t reclaim() {
    std::lock_guard<std::mutex> lk(retire_mu_);
    std::size_t count = 0;
    for (auto it = retired_.begin(); it != retired_.end();) {
      if (!dom_.is_hazardous(it->n)) {
        it->deleter(it->n);
        it = retired_.erase(it);
        ++count;
      } else {
        ++it;
      }
    }
    return count;
  }

  std::size_t retired_count() const {
    std::lock_guard<std::mutex> lk(retire_mu_);
    return retired_.size();
  }

private:
  struct retired_node {
    node* n;
    deleter_fn deleter;
  };

  hazard_domain& dom_;
  std::atomic<node*> head_{};
  std::atomic<node*> tail_{};
  std::atomic<bool> closed_{false};
  mutable std::mutex retire_mu_;
  std::vector<retired_node> retired_;
};

}  // namespace iq
