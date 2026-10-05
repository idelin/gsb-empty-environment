#pragma once

// gc: explicit batch cycle collector for strong-reference object graphs.
//
// Nodes are connected by strong edges (std::shared_ptr). Cycles therefore
// never die on their own; an explicit Collector::collect() pass reclaims the
// subgraphs that are no longer reachable from the registered root set.
// Edges stay strong -- nothing is silently downgraded to weak references.
//
// Collection runs in two phases with explicit boundaries:
//   Phase::Idle  -> Phase::Mark   : snapshot roots, compute reachable set.
//                                   New roots may be registered (deferred to
//                                   the next epoch).
//   Phase::Mark  -> Phase::Sweep  : the unreachable set is retired atomically,
//                                   then unlinked. Retired nodes reject reads.
//   Phase::Sweep -> Phase::Idle   : deferred roots take effect.
//
// Every node destruction is appended to a shared destruction log, which tests
// use to verify the surviving (live) set.

#include <cstddef>
#include <functional>
#include <memory>
#include <stdexcept>
#include <string>
#include <unordered_set>
#include <vector>

namespace gc {

class Node;
class Collector;

enum class Phase { Idle, Mark, Sweep };

class retired_access_error : public std::logic_error {
public:
    retired_access_error();
};

using destruction_log = std::vector<std::string>;

class Node : public std::enable_shared_from_this<Node> {
public:
    ~Node();

    Node(const Node&) = delete;
    Node& operator=(const Node&) = delete;

    const std::string& name() const noexcept { return name_; }

    // Strong edge: this node keeps `child` alive.
    void link(const std::shared_ptr<Node>& child);

    // Guarded accessors: once the node is retired by the collector, any read
    // or write throws retired_access_error instead of touching a half
    // torn-down object.
    int read() const;
    void write(int value);

    bool retired() const noexcept { return retired_; }

private:
    friend class Collector;

    Node(std::shared_ptr<destruction_log> log, std::string name);

    void mark_retired() noexcept { retired_ = true; }
    void unlink_all() noexcept { edges_.clear(); }

    std::shared_ptr<destruction_log> log_;
    std::string name_;
    int value_ = 0;
    bool retired_ = false;
    std::vector<std::shared_ptr<Node>> edges_;
};

class Collector {
public:
    Collector();
    ~Collector() = default;

    Collector(const Collector&) = delete;
    Collector& operator=(const Collector&) = delete;

    std::shared_ptr<Node> make_node(std::string name);

    // Root set management. Roots registered while a collection is in flight
    // are deferred and only take effect at the next phase boundary (i.e. the
    // following epoch). Registering a retired node throws.
    void add_root(const std::shared_ptr<Node>& node);
    void remove_root(const std::shared_ptr<Node>& node);
    bool is_root(const std::shared_ptr<Node>& node) const;

    Phase phase() const noexcept { return phase_; }

    const destruction_log& log() const { return *log_; }
    void clear_log() { log_->clear(); }

    // Currently tracked, non-retired nodes.
    std::vector<std::shared_ptr<Node>> live_set() const;

    // Runs one full Mark -> Sweep epoch. `mark_hook`, if given, runs inside
    // the Mark phase and may register new roots (deferred to next epoch).
    // Returns the number of nodes reclaimed.
    std::size_t collect(const std::function<void(Collector&)>& mark_hook = {});

private:
    void track(const std::shared_ptr<Node>& node);
    void purge_expired();

    std::vector<std::weak_ptr<Node>> tracked_;
    std::unordered_set<std::shared_ptr<Node>> roots_;
    std::vector<std::shared_ptr<Node>> pending_roots_;
    std::shared_ptr<destruction_log> log_;
    Phase phase_ = Phase::Idle;
};

} // namespace gc
