#include "gc_graph.h"

#include <algorithm>

namespace gcgraph {

Graph::~Graph() {
  // Flat loop: Object's destructor frees no other objects, so teardown
  // of even a million-node chain cannot recurse.
  for (Object* obj : objects_) delete obj;
}

Object* Graph::create() {
  Object* obj = new Object(nextId_++);
  objects_.insert(obj);
  return obj;
}

void Graph::addRoot(const std::string& name, Object* obj) {
  if (objects_.count(obj) == 0)
    throw std::invalid_argument("addRoot: object not owned by this graph");
  if (!roots_.emplace(name, obj).second)
    throw std::invalid_argument("addRoot: duplicate root name");
  obj->refCount_++;
}

void Graph::removeRoot(const std::string& name) {
  auto it = roots_.find(name);
  if (it == roots_.end())
    throw std::invalid_argument("removeRoot: unknown root name");
  it->second->refCount_--;
  roots_.erase(it);
}

bool Graph::hasRoot(const std::string& name) const {
  return roots_.count(name) != 0;
}

void Graph::addEdge(Object* from, Object* to) {
  if (objects_.count(from) == 0 || objects_.count(to) == 0)
    throw std::invalid_argument("addEdge: object not owned by this graph");
  from->out_.push_back(to);
  to->refCount_++;
}

void Graph::removeEdge(Object* from, Object* to) {
  if (objects_.count(from) == 0 || objects_.count(to) == 0)
    throw std::invalid_argument("removeEdge: object not owned by this graph");
  auto it = std::find(from->out_.begin(), from->out_.end(), to);
  if (it == from->out_.end())
    throw std::invalid_argument("removeEdge: edge does not exist");
  from->out_.erase(it);
  to->refCount_--;
}

std::size_t Graph::collect() {
  // Mark: iterative DFS from the external roots.
  std::vector<Object*> stack;
  for (const auto& kv : roots_) {
    Object* root = kv.second;
    if (!root->marked_) {
      root->marked_ = true;
      stack.push_back(root);
    }
  }
  while (!stack.empty()) {
    Object* cur = stack.back();
    stack.pop_back();
    for (Object* next : cur->out_) {
      if (!next->marked_) {
        next->marked_ = true;
        stack.push_back(next);
      }
    }
  }

  // Sweep: gather the unreachable (cycles included), unmark the rest.
  std::vector<Object*> dead;
  for (Object* obj : objects_) {
    if (obj->marked_) {
      obj->marked_ = false;
    } else {
      dead.push_back(obj);
    }
  }

  // Pass 1: every dying object removes its outgoing edges, decrementing
  // the targets' refcounts. All dead objects are still allocated here,
  // so decrementing through dead->dead edges is safe.
  for (Object* d : dead) {
    objects_.erase(d);
    for (Object* tgt : d->out_) tgt->refCount_--;
    d->out_.clear();
  }
  // Pass 2: flat deletion loop -- no destructor recursion possible.
  for (Object* d : dead) delete d;

  return dead.size();
}

bool Graph::isAlive(const Object* obj) const {
  return objects_.count(const_cast<Object*>(obj)) != 0;
}

std::vector<Object*> Graph::liveObjects() const {
  return std::vector<Object*>(objects_.begin(), objects_.end());
}

} // namespace gcgraph
