// intrusive_queue.hpp
//
// 多线程侵入式队列（Michael-Scott 算法），节点由调用者所有：
//  - 调用者分配节点并以引用入队，队列自身不分配调用者节点；
//  - 出队返回的节点仍链接在队列中（作为哑元头），可能被其他线程观察，
//    因此出队结果由调用者持有的 Guard（冒险指针）显式保护；
//  - 节点被后续出队摘除后进入退役列表，仅当没有任何受保护引用时，
//    回收（reclaim）才调用调用者提供的 disposer 析构节点（延迟回收）；
//  - close() 之后新入队失败（返回 false），已入队节点可继续排空。
//
// 析构队列时不允许存在任何存活 Guard（调用者须保证读者已全部退出）。
#pragma once

#include <atomic>
#include <cstddef>
#include <cstdlib>
#include <mutex>
#include <vector>

namespace iqueue {

struct Node {
    std::atomic<Node*> next{nullptr};
    virtual ~Node() = default;
};

// 冒险指针域：读侧保护 + 退役/回收。
class HazardDomain {
public:
    static constexpr std::size_t kMaxHazards = 64;
    using Disposer = void (*)(Node*);

    HazardDomain() = default;
    HazardDomain(const HazardDomain&) = delete;
    HazardDomain& operator=(const HazardDomain&) = delete;

    struct Slot {
        std::atomic<Node*> ptr{nullptr};
        std::atomic<bool> used{false};
    };

    // RAII 读侧保护。protect() 期间目标节点不会被回收。
    // 一个 Guard 同一时刻保护一个指针；protect 新指针会解除旧保护。
    class Guard {
    public:
        explicit Guard(HazardDomain& domain) : domain_(&domain) {}
        Guard(const Guard&) = delete;
        Guard& operator=(const Guard&) = delete;
        Guard(Guard&& other) noexcept
            : domain_(other.domain_), slot_(other.slot_) {
            other.domain_ = nullptr;
            other.slot_ = nullptr;
        }
        Guard& operator=(Guard&& other) noexcept {
            if (this != &other) {
                release();
                domain_ = other.domain_;
                slot_ = other.slot_;
                other.domain_ = nullptr;
                other.slot_ = nullptr;
            }
            return *this;
        }
        ~Guard() { release(); }

        // 保护 src 当前指向的节点并返回它；返回的指针在本 Guard
        // 复位/析构前保证不被回收。
        Node* protect(const std::atomic<Node*>& src) {
            if (!slot_) acquire();
            Node* p;
            do {
                p = src.load(std::memory_order_acquire);
                slot_->ptr.store(p, std::memory_order_seq_cst);
            } while (src.load(std::memory_order_acquire) != p);
            return p;
        }

        // 解除当前保护（保留槽位）。
        void reset() {
            if (slot_) slot_->ptr.store(nullptr, std::memory_order_release);
        }

    private:
        void acquire() {
            for (auto& s : domain_->slots_) {
                bool expected = false;
                if (s.used.compare_exchange_strong(expected, true,
                                                   std::memory_order_acq_rel)) {
                    slot_ = &s;
                    return;
                }
            }
            std::abort();  // 冒险槽耗尽：读者线程数超过 kMaxHazards
        }
        void release() {
            if (slot_) {
                slot_->ptr.store(nullptr, std::memory_order_release);
                slot_->used.store(false, std::memory_order_release);
                slot_ = nullptr;
            }
        }

        HazardDomain* domain_;
        Slot* slot_ = nullptr;
    };

    // 退役节点：当没有任何 Guard 引用它时，由 reclaim() 调用 disposer。
    void retire(Node* n, Disposer disposer) {
        std::lock_guard<std::mutex> lk(mutex_);
        retired_.push_back({n, disposer});
    }

    // 回收所有未被保护的退役节点，返回回收数量。
    std::size_t reclaim() {
        Node* hazards[kMaxHazards];
        std::size_t n_hazards = 0;
        for (auto& s : slots_) {
            Node* p = s.ptr.load(std::memory_order_seq_cst);
            if (p) hazards[n_hazards++] = p;
        }
        std::vector<Retired> doomed;
        {
            std::lock_guard<std::mutex> lk(mutex_);
            std::vector<Retired> keep;
            for (auto& r : retired_) {
                bool protected_now = false;
                for (std::size_t i = 0; i < n_hazards; ++i) {
                    if (hazards[i] == r.node) {
                        protected_now = true;
                        break;
                    }
                }
                (protected_now ? keep : doomed).push_back(r);
            }
            retired_.swap(keep);
        }
        for (auto& r : doomed) r.disposer(r.node);
        return doomed.size();
    }

    // 仅析构阶段使用：无视保护，回收全部退役节点。
    void reclaim_all() {
        std::vector<Retired> doomed;
        {
            std::lock_guard<std::mutex> lk(mutex_);
            doomed.swap(retired_);
        }
        for (auto& r : doomed) r.disposer(r.node);
    }

private:
    struct Retired {
        Node* node;
        Disposer disposer;
    };

    Slot slots_[kMaxHazards];
    std::mutex mutex_;
    std::vector<Retired> retired_;
};

class IntrusiveQueue {
public:
    using Disposer = HazardDomain::Disposer;

    explicit IntrusiveQueue(Disposer disposer = default_disposer)
        : disposer_(disposer) {
        stub_ = new Node;  // 队列自有哑元节点
        head_.store(stub_, std::memory_order_relaxed);
        tail_.store(stub_, std::memory_order_relaxed);
    }
    IntrusiveQueue(const IntrusiveQueue&) = delete;
    IntrusiveQueue& operator=(const IntrusiveQueue&) = delete;

    // 调用前须保证：无并发入队/出队，无存活 Guard。
    ~IntrusiveQueue() {
        Node* h = head_.load(std::memory_order_relaxed);
        while (h) {
            Node* next = h->next.load(std::memory_order_relaxed);
            dispose(h);
            h = next;
        }
        domain_.reclaim_all();
    }

    // 入队。close() 之后开始的调用失败并返回 false；
    // 与 close() 并发进行的调用可能成功也可能失败。
    bool enqueue(Node& n) {
        if (closed_.load(std::memory_order_acquire)) return false;
        n.next.store(nullptr, std::memory_order_relaxed);
        HazardDomain::Guard guard(domain_);
        for (;;) {
            Node* tail = guard.protect(tail_);
            Node* next = tail->next.load(std::memory_order_acquire);
            if (tail != tail_.load(std::memory_order_acquire)) continue;
            if (next == nullptr) {
                if (tail->next.compare_exchange_weak(next, &n,
                                                     std::memory_order_release,
                                                     std::memory_order_relaxed)) {
                    tail_.compare_exchange_strong(tail, &n,
                                                  std::memory_order_release,
                                                  std::memory_order_relaxed);
                    return true;
                }
            } else {
                // 尾指针落后，帮忙推进。
                tail_.compare_exchange_weak(tail, next,
                                            std::memory_order_release,
                                            std::memory_order_relaxed);
            }
        }
    }

    // 出队。成功时返回调用者节点，并由 out 保护：调用者在 out 复位/
    // 析构前可安全读取。被摘除的旧头节点进入退役列表延迟回收。
    // 队列为空返回 nullptr（并解除 out 的保护）。
    Node* dequeue(HazardDomain::Guard& out) {
        HazardDomain::Guard head_guard(domain_);
        for (;;) {
            Node* head = head_guard.protect(head_);
            Node* tail = tail_.load(std::memory_order_acquire);
            Node* next = head->next.load(std::memory_order_acquire);
            if (head != head_.load(std::memory_order_acquire)) continue;
            if (head == tail) {
                if (next == nullptr) {
                    out.reset();
                    return nullptr;
                }
                tail_.compare_exchange_weak(tail, next,
                                            std::memory_order_release,
                                            std::memory_order_relaxed);
                continue;
            }
            if (next == nullptr) continue;  // 中间状态，重试
            // 在摘除前先保护 next：保护成功后再推进头指针，
            // 保证返回给调用者的节点不会被并发回收。
            Node* observed = out.protect(head->next);
            if (observed != next) continue;
            if (head_.compare_exchange_weak(head, next,
                                            std::memory_order_acq_rel,
                                            std::memory_order_acquire)) {
                retire_old_head(head);
                return next;
            }
        }
    }

    // 关闭：之后新入队失败；已入队节点仍可全部出队排空。
    void close() { closed_.store(true, std::memory_order_release); }
    bool closed() const { return closed_.load(std::memory_order_acquire); }

    HazardDomain& domain() { return domain_; }
    // 回收当前未被保护的退役节点，返回回收数量。
    std::size_t reclaim() { return domain_.reclaim(); }

private:
    static void default_disposer(Node* n) { delete n; }
    static void dispose_stub(Node* n) { delete n; }

    void retire_old_head(Node* head) {
        domain_.retire(head, head == stub_ ? dispose_stub : disposer_);
    }
    void dispose(Node* n) {
        if (n == stub_) {
            delete n;
        } else {
            disposer_(n);
        }
    }

    HazardDomain domain_;
    Disposer disposer_;
    Node* stub_;
    std::atomic<Node*> head_;
    std::atomic<Node*> tail_;
    std::atomic<bool> closed_{false};
};

}  // namespace iqueue
