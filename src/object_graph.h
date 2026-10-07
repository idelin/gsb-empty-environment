#pragma once

#include <cstddef>
#include <unordered_set>
#include <vector>

namespace objgraph {

// A node in the object graph. Owned by Graph; user code holds raw pointers
// that dangle once the object is reclaimed.
class Object {
public:
    using Id = std::size_t;

    Id id() const { return id_; }
    // External roots + incoming internal edges.
    std::size_t refCount() const { return ref_count_; }

private:
    friend class Graph;
    explicit Object(Id id) : id_(id) {}

    Object(const Object&) = delete;
    Object& operator=(const Object&) = delete;

    Id id_;
    std::size_t ref_count_ = 0;
    std::unordered_set<Object*> out_;  // internal outgoing edges
};

// Reference-counted object graph with explicit cycle collection.
// External roots and internal edges are registered separately; both
// contribute to the reference count. Objects whose count drops to zero are
// reclaimed immediately and iteratively (no recursive destructor chains).
// collect() reclaims everything unreachable from the external roots,
// including reference cycles.
class Graph {
public:
    Graph() = default;
    ~Graph();  // iterative teardown of all remaining objects

    Graph(const Graph&) = delete;
    Graph& operator=(const Graph&) = delete;

    // Creates an unrooted object. It survives until rooted/linked or until
    // the next collect().
    Object* create();

    void addRoot(Object* o);
    void removeRoot(Object* o);
    void addEdge(Object* from, Object* to);
    void removeEdge(Object* from, Object* to);

    // Reclaims all objects unreachable from the external roots.
    // Returns the number of reclaimed objects.
    std::size_t collect();

    std::size_t liveCount() const { return objects_.size(); }
    std::size_t rootCount() const { return roots_.size(); }
    bool contains(Object* o) const { return objects_.count(o) != 0; }
    std::vector<Object::Id> liveIds() const;

private:
    void release(Object* o);
    void reclaimIterative(Object* seed);

    std::unordered_set<Object*> objects_;
    std::unordered_set<Object*> roots_;
    Object::Id next_id_ = 0;
};

}  // namespace objgraph
