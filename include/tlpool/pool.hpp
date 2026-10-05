#pragma once

// tlpool: object pool with thread-local return caches (C++17).
//
// Ownership summary (see README.md for the full graph):
//   - SharedState is co-owned (std::shared_ptr) by the Pool object, every
//     outstanding Ptr handle, and every non-empty thread-local cache entry.
//     The pool object therefore does NOT need to outlive worker threads.
//   - Every T* is in exactly one place at any time: borrowed (inside a Ptr),
//     cached (inside one thread-local cache entry), idle (SharedState::freeList),
//     or destroyed. All hand-offs are moves, never copies, so each object is
//     destroyed exactly once.

#include <atomic>
#include <cstddef>
#include <functional>
#include <memory>
#include <mutex>
#include <stdexcept>
#include <unordered_map>
#include <vector>

namespace tlpool {

template <typename T>
class Pool;

template <typename T>
class Ptr;

namespace detail {

template <typename T>
struct SharedState {
  using Factory = std::function<T*()>;
  using Deleter = std::function<void(T*)>;

  SharedState(Factory factory, Deleter deleter, std::size_t flushThreshold)
      : factory(std::move(factory)),
        deleter(std::move(deleter)),
        flushThreshold(flushThreshold == 0 ? 1 : flushThreshold) {}

  // Runs only after the Pool, all Ptr handles and all TLS cache entries are
  // gone (they each hold a shared_ptr). Whatever is still idle in freeList is
  // destroyed here -- this is destruction site #1.
  ~SharedState() {
    for (T* p : freeList) {
      deleter(p);
      destroyed.fetch_add(1, std::memory_order_relaxed);
    }
  }

  SharedState(const SharedState&) = delete;
  SharedState& operator=(const SharedState&) = delete;

  T* acquireRaw() {
    {
      std::lock_guard<std::mutex> lk(mu);
      if (closed) {
        throw std::logic_error("tlpool: acquire on a closed pool");
      }
      if (!freeList.empty()) {
        T* p = freeList.back();
        freeList.pop_back();
        return p;
      }
    }
    // Construct outside the lock; a racing close() does not matter because a
    // freshly created object is handed straight to the caller.
    T* p = factory();
    created.fetch_add(1, std::memory_order_relaxed);
    return p;
  }

  // Batch return: takes ownership of every pointer in `batch` (clears it).
  // Either appends all of them to freeList in one critical section, or -- if
  // the pool is closed -- destroys them immediately (destruction site #2).
  void returnBatch(std::vector<T*>& batch) {
    if (batch.empty()) return;
    const std::size_t n = batch.size();
    bool destroyNow;
    {
      std::lock_guard<std::mutex> lk(mu);
      destroyNow = closed;
      if (!destroyNow) {
        freeList.insert(freeList.end(), batch.begin(), batch.end());
      }
    }
    if (destroyNow) {
      for (T* p : batch) {
        deleter(p);
        destroyed.fetch_add(1, std::memory_order_relaxed);
      }
    } else {
      batchesFlushed.fetch_add(1, std::memory_order_relaxed);
      objectsFlushed.fetch_add(n, std::memory_order_relaxed);
    }
    batch.clear();
  }

  // Pool close protocol: flip `closed`, drain the idle list and destroy it
  // outside the lock (destruction site #3). Every future return -- threshold
  // flush, thread-exit flush, or a late-returned borrowed object -- lands in
  // returnBatch() and is destroyed immediately instead of being cached.
  void close() {
    std::vector<T*> doomed;
    {
      std::lock_guard<std::mutex> lk(mu);
      if (closed) return;
      closed = true;
      doomed.swap(freeList);
    }
    for (T* p : doomed) {
      deleter(p);
      destroyed.fetch_add(1, std::memory_order_relaxed);
    }
  }

  bool isClosed() const {
    std::lock_guard<std::mutex> lk(mu);
    return closed;
  }

  std::size_t freeCount() const {
    std::lock_guard<std::mutex> lk(mu);
    return freeList.size();
  }

  mutable std::mutex mu;
  std::vector<T*> freeList;  // guarded by mu
  bool closed = false;       // guarded by mu
  Factory factory;
  Deleter deleter;
  const std::size_t flushThreshold;

  // Observability counters (relaxed; used by tests and monitoring).
  std::atomic<std::size_t> created{0};
  std::atomic<std::size_t> destroyed{0};
  std::atomic<std::size_t> batchesFlushed{0};
  std::atomic<std::size_t> objectsFlushed{0};
};

}  // namespace detail

// Borrowed-object handle. Move-only. Destruction returns the object through
// the *current* thread's return cache (which may differ from the acquiring
// thread -- that is fine, caches are per-returning-thread).
template <typename T>
class Ptr {
 public:
  Ptr() = default;

  ~Ptr() { reset(); }

  Ptr(Ptr&& other) noexcept : ptr_(other.ptr_), state_(std::move(other.state_)) {
    other.ptr_ = nullptr;
  }

  Ptr& operator=(Ptr&& other) noexcept {
    if (this != &other) {
      reset();
      ptr_ = other.ptr_;
      state_ = std::move(other.state_);
      other.ptr_ = nullptr;
    }
    return *this;
  }

  Ptr(const Ptr&) = delete;
  Ptr& operator=(const Ptr&) = delete;

  T* get() const { return ptr_; }
  T& operator*() const { return *ptr_; }
  T* operator->() const { return ptr_; }
  explicit operator bool() const { return ptr_ != nullptr; }

  // Returns the object to the pool (via the TLS return cache) and empties
  // the handle. Safe to call after Pool::close() and after the Pool object
  // itself has been destroyed -- the handle keeps SharedState alive.
  void reset() {
    if (!ptr_) return;
    T* p = ptr_;
    ptr_ = nullptr;
    Pool<T>::returnViaTls(std::move(state_), p);
  }

 private:
  friend class Pool<T>;
  Ptr(T* p, std::shared_ptr<detail::SharedState<T>> state)
      : ptr_(p), state_(std::move(state)) {}

  T* ptr_ = nullptr;
  std::shared_ptr<detail::SharedState<T>> state_;
};

template <typename T>
class Pool {
 public:
  using State = detail::SharedState<T>;
  using Factory = typename State::Factory;
  using Deleter = typename State::Deleter;

  // flushThreshold: number of returned objects a thread accumulates locally
  // before handing the whole batch to the shared pool in one locked section.
  explicit Pool(Factory factory,
                Deleter deleter = nullptr,
                std::size_t flushThreshold = 32)
      : state_(std::make_shared<State>(
            std::move(factory),
            deleter ? std::move(deleter) : Deleter([](T* p) { delete p; }),
            flushThreshold)) {}

  Ptr<T> acquire() { return Ptr<T>(state_->acquireRaw(), state_); }

  // Close protocol: idle objects are destroyed now; borrowed objects are
  // destroyed on return; acquire() throws. Idempotent.
  void close() { state_->close(); }

  bool closed() const { return state_->isClosed(); }
  std::size_t freeCount() const { return state_->freeCount(); }

  // Observability.
  std::size_t created() const { return state_->created.load(); }
  std::size_t destroyed() const { return state_->destroyed.load(); }
  std::size_t batchesFlushed() const { return state_->batchesFlushed.load(); }
  std::size_t objectsFlushed() const { return state_->objectsFlushed.load(); }

 private:
  // Per-thread return cache: one entry per pool (keyed by SharedState*).
  // Each entry holds a shared_ptr to its SharedState, which is what allows
  // worker threads to outlive the Pool object safely.
  struct TlsEntry {
    std::shared_ptr<State> state;
    std::vector<T*> objs;
    // Thread-exit protocol: when the thread dies, its cache map is destroyed
    // and every entry flushes its leftover batch to the (still alive) shared
    // state. No batch is ever lost on thread exit.
    ~TlsEntry() {
      if (state) state->returnBatch(objs);
    }
  };

  struct TlsCache {
    std::unordered_map<State*, TlsEntry> entries;
  };

  static void returnViaTls(std::shared_ptr<State> state, T* p) {
    thread_local TlsCache cache;
    TlsEntry& entry = cache.entries[state.get()];
    if (!entry.state) entry.state = std::move(state);
    entry.objs.push_back(p);
    if (entry.objs.size() >= entry.state->flushThreshold) {
      entry.state->returnBatch(entry.objs);
    }
  }

  friend class Ptr<T>;

  std::shared_ptr<State> state_;
};

}  // namespace tlpool
