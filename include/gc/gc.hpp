// gc.hpp -- Explicit batch cycle collector for strongly-linked node graphs.
//
// Design overview
// ---------------
// * Nodes derive from gc::Node and are created through gc::Collector::create.
//   The collector owns every node; the graph itself is built from *strong*
//   edges (gc::Ptr<T>). Edges are never weak references: reachability from
//   the external root set is what keeps a node alive.
// * The external root set (gc::Root<T>) may change at any time. Roots
//   registered while a collection cycle is open are deferred: they take
//   effect at the next begin boundary, so the in-flight cycle always works
//   on a stable snapshot.
// * Collection is explicit and batched, with two observable phase
//   boundaries:
//       auto cycle = c.startCycle();        // begin boundary
//       cycle.retireUnreachable();          // mark + retire
//       cycle.finish();                     // end boundary
//   or the convenience c.collect() which runs a full cycle.
// * Retired nodes sit in a quarantine between the two boundaries. During
//   that window they still exist but cannot be read: Handle/Root accessors
//   return nullptr. They are destroyed at the end boundary, and every
//   destruction is appended to the collector's destruction log, which tests
//   use to verify the survivor set.
//
// The library is single-threaded by design; phase boundaries are the
// synchronization points.

#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

namespace gc {

class Collector;
class Node;
template <typename T> class Ptr;
template <typename T> class Handle;
template <typename T> class Root;

enum class NodeState { kAlive, kRetired };

namespace internal {

// Shared liveness cell. Handles and roots observe the node through this
// block, so a retired or destroyed node is never dereferenced.
struct Control {
  Node* node = nullptr;
  NodeState state = NodeState::kAlive;
};

// Type-erased edge slot. Every gc::Ptr member registers itself with its
// owner node, which lets the collector trace and isolate edges generically.
struct SlotBase {
  Node* target = nullptr;
  virtual ~SlotBase() = default;
  virtual void clear() noexcept = 0;
};

}  // namespace internal

// Append-only record of node destructions, used to verify the survivor set.
class DestructionLog {
public:
  struct Entry {
    std::uint64_t sequence;
    std::uint64_t id;
    std::string name;
  };

  void record(std::uint64_t id, std::string name) {
    entries_.push_back(Entry{nextSequence_++, id, std::move(name)});
  }

  const std::vector<Entry>& entries() const { return entries_; }

  std::vector<std::string> names() const {
    std::vector<std::string> result;
    result.reserve(entries_.size());
    for (const auto& entry : entries_) result.push_back(entry.name);
    return result;
  }

  bool contains(const std::string& name) const {
    for (const auto& entry : entries_) {
      if (entry.name == name) return true;
    }
    return false;
  }

  void clear() { entries_.clear(); }

private:
  std::vector<Entry> entries_;
  std::uint64_t nextSequence_ = 0;
};

class Node {
public:
  Node(const Node&) = delete;
  Node& operator=(const Node&) = delete;
  virtual ~Node();

  std::uint64_t id() const { return id_; }
  const std::string& name() const { return name_; }
  NodeState state() const { return control_->state; }

protected:
  Node() : control_(std::make_shared<internal::Control>()) {}

private:
  friend class Collector;
  template <typename T> friend class Ptr;
  template <typename T> friend class Root;

  void addSlot(internal::SlotBase* slot) { slots_.push_back(slot); }
  void removeSlot(internal::SlotBase* slot);

  // Breaks every outgoing strong edge. Called when the node is retired so
  // that unreachable cycles are dismantled before destruction.
  void isolate() noexcept {
    for (auto* slot : slots_) slot->clear();
  }

  std::shared_ptr<internal::Control> control_;
  std::vector<internal::SlotBase*> slots_;
  Collector* collector_ = nullptr;
  std::uint64_t id_ = 0;
  std::string name_;
  bool marked_ = false;
};

// Strong graph edge. Stored as a member of a gc::Node subclass:
//     struct MyNode : gc::Node { gc::Ptr<MyNode> next{this}; };
// The edge keeps its target alive purely through collector reachability;
// it is never a weak reference.
template <typename T>
class Ptr : public internal::SlotBase {
public:
  explicit Ptr(Node* owner) : owner_(owner) { owner_->addSlot(this); }
  Ptr(const Ptr&) = delete;
  Ptr& operator=(const Ptr&) = delete;
  ~Ptr() override {
    if (owner_) owner_->removeSlot(this);
  }

  Ptr& operator=(T* target) noexcept {
    this->target = target;
    return *this;
  }
  Ptr& operator=(std::nullptr_t) noexcept {
    this->target = nullptr;
    return *this;
  }

  T* get() const noexcept { return static_cast<T*>(this->target); }
  T& operator*() const noexcept { return *get(); }
  T* operator->() const noexcept { return get(); }
  explicit operator bool() const noexcept { return this->target != nullptr; }
  void reset() noexcept { this->target = nullptr; }
  void clear() noexcept override { this->target = nullptr; }

private:
  Node* owner_;
};

// Non-owning observer. Reading a retired or destroyed node yields nullptr.
template <typename T>
class Handle {
public:
  Handle() = default;

  T* get() const noexcept {
    auto control = control_.lock();
    if (!control || control->state != NodeState::kAlive) return nullptr;
    return static_cast<T*>(control->node);
  }
  T* operator->() const noexcept { return get(); }
  explicit operator bool() const noexcept { return get() != nullptr; }

private:
  friend class Collector;
  explicit Handle(std::weak_ptr<internal::Control> control)
      : control_(std::move(control)) {}

  std::weak_ptr<internal::Control> control_;
};

// External root. While a Root is alive and active, its node (and everything
// reachable from it) survives collection. Roots registered while a cycle is
// open are deferred to the next begin boundary.
template <typename T>
class Root {
public:
  Root() = default;
  Root(Collector& collector, T* node) : collector_(&collector), node_(node) {
    if (node_) {
      control_ = node_->control_;
      registerWithCollector();
    }
  }

  Root(const Root& other)
      : collector_(other.collector_), node_(other.node_), control_(other.control_) {
    if (node_) registerWithCollector();
  }
  Root& operator=(const Root& other) {
    if (this != &other) {
      unregister();
      collector_ = other.collector_;
      node_ = other.node_;
      control_ = other.control_;
      if (node_) registerWithCollector();
    }
    return *this;
  }

  Root(Root&& other) noexcept { stealFrom(other); }
  Root& operator=(Root&& other) noexcept {
    if (this != &other) {
      unregister();
      stealFrom(other);
    }
    return *this;
  }

  ~Root() { unregister(); }

  T* get() const noexcept {
    if (!control_ || control_->state != NodeState::kAlive) return nullptr;
    return static_cast<T*>(control_->node);
  }
  T* operator->() const noexcept { return get(); }
  explicit operator bool() const noexcept { return get() != nullptr; }

  void reset() { unregister(); }

private:
  void registerWithCollector();
  void unregister();
  void stealFrom(Root& other) noexcept {
    collector_ = other.collector_;
    node_ = other.node_;
    control_ = std::move(other.control_);
    epoch_ = other.epoch_;
    pending_ = other.pending_;
    other.collector_ = nullptr;
    other.node_ = nullptr;
    other.control_.reset();
  }

  Collector* collector_ = nullptr;
  Node* node_ = nullptr;
  std::shared_ptr<internal::Control> control_;
  std::uint64_t epoch_ = 0;
  bool pending_ = false;
};

class Collector {
public:
  Collector() = default;
  Collector(const Collector&) = delete;
  Collector& operator=(const Collector&) = delete;
  ~Collector() = default;

  // Creates a node owned by the collector. Nodes created while a cycle is
  // open are treated as reachable for that cycle (allocated-live).
  template <typename T, typename... Args>
  T* create(std::string name, Args&&... args) {
    auto node = std::make_unique<T>(std::forward<Args>(args)...);
    T* raw = node.get();
    raw->collector_ = this;
    raw->id_ = nextId_++;
    raw->name_ = std::move(name);
    raw->control_->node = raw;
    raw->marked_ = cycleOpen_;
    all_.emplace(raw->id_, std::move(node));
    return raw;
  }

  // Returns an observer for `node`. The node must be alive: like any API
  // taking a raw pointer, calling this on a destroyed node is undefined.
  // Acquire handles before running a collection that may retire the node.
  template <typename T>
  Handle<T> handleOf(T* node) const {
    return Handle<T>(node->control_);
  }

  // One collection cycle, delimited by two explicit phase boundaries.
  // Must not outlive the collector.
  class Cycle {
  public:
    Cycle(Cycle&& other) noexcept
        : collector_(other.collector_), active_(std::exchange(other.active_, false)) {}
    Cycle(const Cycle&) = delete;
    Cycle& operator=(const Cycle&) = delete;
    ~Cycle() {
      if (active_) finish();
    }

    // Mark phase: floods from the root snapshot taken at the begin
    // boundary. Sweep phase: unmarked nodes are retired into quarantine
    // and their outgoing edges are cut. Retired nodes become unreadable
    // immediately but are not destroyed until the end boundary.
    std::size_t retireUnreachable() {
      std::vector<Node*> stack;
      for (const auto& entry : collector_.roots_) {
        Node* node = entry.first;
        if (node->control_->state == NodeState::kAlive && !node->marked_) {
          node->marked_ = true;
          stack.push_back(node);
        }
      }
      while (!stack.empty()) {
        Node* node = stack.back();
        stack.pop_back();
        for (auto* slot : node->slots_) {
          Node* target = slot->target;
          if (target && !target->marked_) {
            target->marked_ = true;
            stack.push_back(target);
          }
        }
      }

      std::size_t retired = 0;
      auto& all = collector_.all_;
      for (auto it = all.begin(); it != all.end();) {
        Node* node = it->second.get();
        if (node->marked_) {
          node->marked_ = false;
          ++it;
          continue;
        }
        node->control_->state = NodeState::kRetired;
        node->isolate();
        // Deferred root registrations that arrived during this cycle came
        // too late to protect the node; drop them before the pointer dies.
        auto& pending = collector_.pendingRoots_;
        pending.erase(std::remove(pending.begin(), pending.end(), node),
                      pending.end());
        collector_.quarantine_.push_back(std::move(it->second));
        it = all.erase(it);
        ++retired;
      }
      return retired;
    }

    // End boundary: quarantined nodes are destroyed (each destructor
    // appends to the destruction log) and the cycle closes.
    void finish() {
      if (!active_) return;
      active_ = false;
      collector_.quarantine_.clear();
      collector_.cycleOpen_ = false;
    }

  private:
    friend class Collector;
    explicit Cycle(Collector& collector) : collector_(collector) {}

    Collector& collector_;
    bool active_ = true;
  };

  // Begin boundary: deferred roots registered since the previous boundary
  // become active, and the root set is frozen for the coming mark phase.
  Cycle startCycle() {
    if (cycleOpen_) throw std::logic_error("gc: collection cycle already open");
    for (Node* node : pendingRoots_) {
      if (node->control_->state == NodeState::kAlive) ++roots_[node];
    }
    pendingRoots_.clear();
    ++epoch_;
    cycleOpen_ = true;
    return Cycle(*this);
  }

  // Convenience: runs a complete cycle and returns the number of retired
  // (and destroyed) nodes.
  std::size_t collect() {
    auto cycle = startCycle();
    std::size_t retired = cycle.retireUnreachable();
    cycle.finish();
    return retired;
  }

  const DestructionLog& destructorLog() const { return log_; }
  std::size_t nodeCount() const { return all_.size(); }
  std::size_t quarantineCount() const { return quarantine_.size(); }
  bool cycleOpen() const { return cycleOpen_; }

private:
  friend class Node;
  template <typename T> friend class Root;

  void noteDestroyed(const Node& node) { log_.record(node.id_, node.name_); }

  void registerRoot(Node* node, std::uint64_t& epochOut, bool& pendingOut) {
    epochOut = epoch_;
    if (cycleOpen_) {
      pendingRoots_.push_back(node);
      pendingOut = true;
    } else {
      ++roots_[node];
      pendingOut = false;
    }
  }

  void unregisterRoot(Node* node, std::uint64_t epoch, bool pending) {
    if (pending && epoch == epoch_) {
      auto it = std::find(pendingRoots_.begin(), pendingRoots_.end(), node);
      if (it != pendingRoots_.end()) pendingRoots_.erase(it);
      return;
    }
    auto it = roots_.find(node);
    if (it != roots_.end() && --it->second == 0) roots_.erase(it);
  }

  // log_ is declared first so it outlives every node and quarantine.
  DestructionLog log_;
  std::unordered_map<std::uint64_t, std::unique_ptr<Node>> all_;
  std::vector<std::unique_ptr<Node>> quarantine_;
  std::unordered_map<Node*, std::size_t> roots_;
  std::vector<Node*> pendingRoots_;
  std::uint64_t nextId_ = 1;
  std::uint64_t epoch_ = 0;
  bool cycleOpen_ = false;
};

inline Node::~Node() {
  control_->node = nullptr;
  control_->state = NodeState::kRetired;
  if (collector_) collector_->noteDestroyed(*this);
}

inline void Node::removeSlot(internal::SlotBase* slot) {
  auto it = std::find(slots_.begin(), slots_.end(), slot);
  if (it != slots_.end()) slots_.erase(it);
}

template <typename T>
void Root<T>::registerWithCollector() {
  collector_->registerRoot(node_, epoch_, pending_);
}

template <typename T>
void Root<T>::unregister() {
  if (collector_ && node_) collector_->unregisterRoot(node_, epoch_, pending_);
  collector_ = nullptr;
  node_ = nullptr;
  control_.reset();
}

}  // namespace gc
