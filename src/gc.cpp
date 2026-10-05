#include "gc/gc.hpp"

#include <algorithm>
#include <unordered_set>

namespace gc {

retired_access_error::retired_access_error()
    : std::logic_error("gc: access to a retired node") {}

// ---------------------------------------------------------------------------
// Node
// ---------------------------------------------------------------------------

Node::Node(std::shared_ptr<destruction_log> log, std::string name)
    : log_(std::move(log)), name_(std::move(name)) {}

Node::~Node() {
    if (log_) {
        log_->push_back(name_);
    }
}

void Node::link(const std::shared_ptr<Node>& child) {
    if (retired_) {
        throw retired_access_error();
    }
    if (!child) {
        throw std::invalid_argument("gc: cannot link a null node");
    }
    if (child->retired_) {
        throw retired_access_error();
    }
    edges_.push_back(child);
}

int Node::read() const {
    if (retired_) {
        throw retired_access_error();
    }
    return value_;
}

void Node::write(int value) {
    if (retired_) {
        throw retired_access_error();
    }
    value_ = value;
}

// ---------------------------------------------------------------------------
// Collector
// ---------------------------------------------------------------------------

Collector::Collector() : log_(std::make_shared<destruction_log>()) {}

std::shared_ptr<Node> Collector::make_node(std::string name) {
    // Node's constructor is private; use a deleter-enabled new instead of
    // make_shared so only Collector can create nodes.
    std::shared_ptr<Node> node(new Node(log_, std::move(name)));
    track(node);
    return node;
}

void Collector::track(const std::shared_ptr<Node>& node) {
    tracked_.push_back(node);
}

void Collector::purge_expired() {
    tracked_.erase(
        std::remove_if(tracked_.begin(), tracked_.end(),
                       [](const std::weak_ptr<Node>& w) { return w.expired(); }),
        tracked_.end());
}

void Collector::add_root(const std::shared_ptr<Node>& node) {
    if (!node) {
        throw std::invalid_argument("gc: cannot root a null node");
    }
    if (node->retired_) {
        // A retired node is already scheduled for reclamation; resurrecting
        // it through a root is not allowed.
        throw retired_access_error();
    }
    if (phase_ == Phase::Idle) {
        roots_.insert(node);
    } else {
        // Deferred: takes effect at the next phase boundary.
        pending_roots_.push_back(node);
    }
}

void Collector::remove_root(const std::shared_ptr<Node>& node) {
    roots_.erase(node);
    pending_roots_.erase(
        std::remove(pending_roots_.begin(), pending_roots_.end(), node),
        pending_roots_.end());
}

bool Collector::is_root(const std::shared_ptr<Node>& node) const {
    return roots_.count(node) != 0;
}

std::vector<std::shared_ptr<Node>> Collector::live_set() const {
    std::vector<std::shared_ptr<Node>> live;
    for (const auto& weak : tracked_) {
        if (auto node = weak.lock()) {
            if (!node->retired()) {
                live.push_back(std::move(node));
            }
        }
    }
    return live;
}

std::size_t Collector::collect(const std::function<void(Collector&)>& mark_hook) {
    if (phase_ != Phase::Idle) {
        throw std::logic_error("gc: reentrant collect() is not allowed");
    }

    // --- Phase boundary: Idle -> Mark -------------------------------------
    phase_ = Phase::Mark;
    purge_expired();

    // Root snapshot: the reachable set is computed against the roots as they
    // were when the epoch began. Roots registered during the hook are
    // deferred and do not rescue nodes in this epoch.
    std::vector<std::shared_ptr<Node>> root_snapshot(roots_.begin(), roots_.end());

    if (mark_hook) {
        mark_hook(*this);
    }

    std::unordered_set<Node*> reachable;
    std::vector<Node*> stack;
    for (const auto& root : root_snapshot) {
        if (root && reachable.insert(root.get()).second) {
            stack.push_back(root.get());
        }
    }
    while (!stack.empty()) {
        Node* current = stack.back();
        stack.pop_back();
        for (const auto& edge : current->edges_) {
            if (edge && reachable.insert(edge.get()).second) {
                stack.push_back(edge.get());
            }
        }
    }

    // --- Phase boundary: Mark -> Sweep ------------------------------------
    phase_ = Phase::Sweep;

    std::vector<std::shared_ptr<Node>> garbage;
    for (const auto& weak : tracked_) {
        if (auto node = weak.lock()) {
            if (reachable.count(node.get()) == 0) {
                garbage.push_back(std::move(node));
            }
        }
    }

    // Retirement is atomic across the whole unreachable set: every doomed
    // node starts rejecting reads before any edge is cut, so no thread of
    // control can observe a partially unlinked graph.
    for (const auto& node : garbage) {
        node->mark_retired();
    }
    // Cut all outgoing strong edges. This breaks every cycle inside the
    // garbage set; nodes whose last strong reference came from the graph are
    // destroyed here and recorded in the destruction log.
    for (const auto& node : garbage) {
        node->unlink_all();
    }
    const std::size_t reclaimed = garbage.size();
    garbage.clear(); // drop the collector's own references

    // --- Phase boundary: Sweep -> Idle ------------------------------------
    phase_ = Phase::Idle;
    for (const auto& pending : pending_roots_) {
        if (!pending->retired()) {
            roots_.insert(pending);
        }
    }
    pending_roots_.clear();
    purge_expired();

    return reclaimed;
}

} // namespace gc
