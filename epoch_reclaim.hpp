#pragma once

// 无后台线程的代次(epoch)回收器。
//
// 设计要点:
//  - 资源句柄(Resource)先被标记为退休, 负载对象的析构被推迟到
//    所有"已开始的读取"(存活的 Guard)结束之后, 由调用现场同步完成,
//    不创建任何后台线程。
//  - 读取守卫(Guard)是 RAII 的代次固定器: 允许移动, 禁止复制。
//  - 句柄一旦退休, guard() 永远返回 nullopt, 退休期间不能取得新守卫。
//  - EpochDomain 的生命周期必须覆盖所有 Resource 与 Guard。

#include <array>
#include <atomic>
#include <cstdint>
#include <mutex>
#include <optional>
#include <utility>
#include <vector>

namespace ebr {

template <typename T>
class Guard;
template <typename T>
class Resource;

class EpochDomain {
public:
    EpochDomain() = default;
    EpochDomain(const EpochDomain&) = delete;
    EpochDomain& operator=(const EpochDomain&) = delete;

    ~EpochDomain() { reclaim_all(); }

    std::uint64_t epoch() const { return epoch_.load(std::memory_order_relaxed); }

    // 仍在退休队列中、等待回收的对象数(观测用)。
    std::size_t pending() const {
        std::lock_guard<std::mutex> lk(mu_);
        return limbo_[0].size() + limbo_[1].size() + limbo_[2].size();
    }

private:
    template <typename T>
    friend class Guard;
    template <typename T>
    friend class Resource;

    struct Retired {
        void* ptr;
        void (*deleter)(void*);
    };

    static constexpr std::size_t kEpochCount = 3;

    // 在当前代次上登记一个读者。与 collect() 同一把锁,
    // 保证登记到的代次不会被并发推进"跨过"。
    std::uint64_t pin() {
        std::lock_guard<std::mutex> lk(mu_);
        const std::uint64_t e = epoch_.load(std::memory_order_relaxed);
        active_.push_back(e);
        return e;
    }

    void unpin(std::uint64_t e) {
        {
            std::lock_guard<std::mutex> lk(mu_);
            for (auto it = active_.begin(); it != active_.end(); ++it) {
                if (*it == e) {
                    active_.erase(it);
                    break;
                }
            }
        }
        collect();
    }

    template <typename T>
    void retire(T* p) {
        {
            std::lock_guard<std::mutex> lk(mu_);
            limbo_[epoch_.load(std::memory_order_relaxed) % kEpochCount].push_back(
                {p, [](void* q) { delete static_cast<T*>(q); }});
        }
        collect();
    }

    // 代次推进条件: 没有任何读者停留在当前代次。
    // 推进到第 N 代时回收第 N-3 代退休的对象(它们不可能再被任何
    // 存活读者看到); 单次最多推进 kEpochCount 代, 保证有界。
    void collect() {
        std::vector<Retired> trash;
        {
            std::lock_guard<std::mutex> lk(mu_);
            for (std::size_t step = 0; step < kEpochCount; ++step) {
                const std::uint64_t e = epoch_.load(std::memory_order_relaxed);
                bool blocked = false;
                for (const std::uint64_t a : active_) {
                    if (a == e) {
                        blocked = true;
                        break;
                    }
                }
                if (blocked) break;
                epoch_.store(e + 1, std::memory_order_relaxed);
                auto& list = limbo_[(e + 1) % kEpochCount];
                trash.insert(trash.end(), list.begin(), list.end());
                list.clear();
            }
        }
        // 析构在锁外执行, 避免析构函数回调本域时死锁。
        for (const Retired& r : trash) r.deleter(r.ptr);
    }

    void reclaim_all() {
        std::vector<Retired> trash;
        {
            std::lock_guard<std::mutex> lk(mu_);
            for (auto& list : limbo_) {
                trash.insert(trash.end(), list.begin(), list.end());
                list.clear();
            }
        }
        for (const Retired& r : trash) r.deleter(r.ptr);
    }

    std::atomic<std::uint64_t> epoch_{0};
    mutable std::mutex mu_;
    std::vector<std::uint64_t> active_;  // 存活读者各自固定的代次
    std::array<std::vector<Retired>, kEpochCount> limbo_;
};

// 读取守卫: 存活期间固定一个代次, 保证所读对象不被回收。
// 允许移动(移动后源守卫不再持有代次), 禁止复制。
template <typename T>
class Guard {
public:
    Guard(const Guard&) = delete;
    Guard& operator=(const Guard&) = delete;

    Guard(Guard&& other) noexcept
        : domain_(std::exchange(other.domain_, nullptr)),
          payload_(std::exchange(other.payload_, nullptr)),
          pin_(other.pin_) {}

    Guard& operator=(Guard&& other) noexcept {
        if (this != &other) {
            release();
            domain_ = std::exchange(other.domain_, nullptr);
            payload_ = std::exchange(other.payload_, nullptr);
            pin_ = other.pin_;
        }
        return *this;
    }

    ~Guard() { release(); }

    const T& get() const { return *payload_; }
    const T* operator->() const { return payload_; }
    const T& operator*() const { return *payload_; }
    explicit operator bool() const { return payload_ != nullptr; }

private:
    friend class Resource<T>;

    Guard(EpochDomain* domain, const T* payload, std::uint64_t pin)
        : domain_(domain), payload_(payload), pin_(pin) {}

    void release() {
        if (domain_) {
            domain_->unpin(pin_);
            domain_ = nullptr;
            payload_ = nullptr;
        }
    }

    EpochDomain* domain_;
    const T* payload_;
    std::uint64_t pin_;
};

// 资源句柄: 拥有负载对象。retire() 之后不再发放新守卫;
// 负载的释放被推迟到所有已开始的读取结束之后。
template <typename T>
class Resource {
public:
    Resource(EpochDomain& domain, T* payload) : domain_(&domain), payload_(payload) {}
    ~Resource() { retire(); }

    Resource(const Resource&) = delete;
    Resource& operator=(const Resource&) = delete;

    // 退休期间(以及退休之后)返回 nullopt。
    std::optional<Guard<T>> guard() {
        if (retired_.load(std::memory_order_acquire)) return std::nullopt;
        const std::uint64_t pin = domain_->pin();
        // 固定代次后复查, 闭合与 retire() 的竞争窗口。
        if (retired_.load(std::memory_order_acquire)) {
            domain_->unpin(pin);
            return std::nullopt;
        }
        const T* p = payload_.load(std::memory_order_acquire);
        if (!p) {
            domain_->unpin(pin);
            return std::nullopt;
        }
        return Guard<T>(domain_, p, pin);
    }

    bool retired() const { return retired_.load(std::memory_order_acquire); }

    // 幂等: 只第一次调用生效。
    void retire() {
        bool expected = false;
        if (!retired_.compare_exchange_strong(expected, true, std::memory_order_acq_rel)) return;
        T* p = payload_.exchange(nullptr, std::memory_order_acq_rel);
        if (p) domain_->retire(p);
    }

private:
    EpochDomain* domain_;
    std::atomic<T*> payload_;
    std::atomic<bool> retired_{false};
};

}  // namespace ebr
