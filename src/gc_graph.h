#pragma once

#include <cstddef>
#include <stdexcept>
#include <string>
#include <unordered_map>
#include <unordered_set>
#include <vector>

namespace gcgraph {

// An object in the managed graph. Its reference count tracks
//   (# external roots naming it) + (# incoming internal edges).
// Cycles keep counts above zero, so reclamation is done explicitly
// by Graph::collect() via mark & sweep from the external roots.
class Object {
public:
  using Id = std::size_t;

  Id id() const { return id_; }
  std::size_t refCount() const { return refCount_; }
  const std::vector<Object*>& edges() const { return out_; }

  Object(const Object&) = delete;
  Object& operator=(const Object&) = delete;

private:
  friend class Graph;
  explicit Object(Id id) : id_(id) {}

  Id id_;
  std::size_t refCount_ = 0;    // external roots + incoming internal edges
  std::vector<Object*> out_;    // internal outgoing edges (raw, non-owning)
  bool marked_ = false;
};

class Graph {
public:
  Graph() = default;
  ~Graph();  // iterative teardown, never recursive

  Graph(const Graph&) = delete;
  Graph& operator=(const Graph&) = delete;

  // Creates an object with refcount 0 (unrooted until addRoot/addEdge).
  Object* create();

  // External roots, registered separately from internal edges.
  void addRoot(const std::string& name, Object* obj);
  void removeRoot(const std::string& name);
  bool hasRoot(const std::string& name) const;

  // Internal edges between objects.
  void addEdge(Object* from, Object* to);
  void removeEdge(Object* from, Object* to);

  // Reclaims every object not reachable from the external roots,
  // including mutually referencing cycles. Returns objects reclaimed.
  // Destruction is iterative (explicit worklist), so deep or cyclic
  // structures cannot overflow the call stack.
  std::size_t collect();

  bool isAlive(const Object* obj) const;
  std::size_t size() const { return objects_.size(); }
  std::vector<Object*> liveObjects() const;

private:
  Object::Id nextId_ = 0;
  std::unordered_set<Object*> objects_;            // all live objects
  std::unordered_map<std::string, Object*> roots_; // external roots
};

} // namespace gcgraph
