#include "object_graph.h"

namespace objgraph {

Object* Graph::create() {
    Object* o = new Object(next_id_++);
    objects_.insert(o);
    return o;
}

void Graph::addRoot(Object* o) {
    if (roots_.insert(o).second) ++o->ref_count_;
}

void Graph::removeRoot(Object* o) {
    if (roots_.erase(o)) release(o);
}

void Graph::addEdge(Object* from, Object* to) {
    if (from->out_.insert(to).second) ++to->ref_count_;
}

void Graph::removeEdge(Object* from, Object* to) {
    if (from->out_.erase(to)) release(to);
}

void Graph::release(Object* o) {
    if (--o->ref_count_ == 0) reclaimIterative(o);
}

// Reclaims `seed` and everything that becomes unreachable through the
// resulting edge removals, using an explicit work stack instead of
// recursive destructor calls.
void Graph::reclaimIterative(Object* seed) {
    std::vector<Object*> stack{seed};
    while (!stack.empty()) {
        Object* victim = stack.back();
        stack.pop_back();
        // Destruction removes the victim's outgoing edges; children whose
        // count reaches zero are reclaimed in turn.
        for (Object* child : victim->out_) {
            if (--child->ref_count_ == 0) stack.push_back(child);
        }
        victim->out_.clear();
        objects_.erase(victim);
        delete victim;
    }
}

std::size_t Graph::collect() {
    // Plain graph reachability from the external roots.
    std::unordered_set<Object*> reachable;
    std::vector<Object*> stack(roots_.begin(), roots_.end());
    while (!stack.empty()) {
        Object* o = stack.back();
        stack.pop_back();
        if (!reachable.insert(o).second) continue;
        for (Object* child : o->out_) stack.push_back(child);
    }

    std::vector<Object*> victims;
    for (Object* o : objects_) {
        if (!reachable.count(o)) victims.push_back(o);
    }
    if (victims.empty()) return 0;

    // Detach victim edges first (iteratively, no cascading deletes), then
    // destroy. Reachable targets keep a positive count because some
    // reachable edge or root still accounts for them.
    std::unordered_set<Object*> victim_set(victims.begin(), victims.end());
    for (Object* victim : victims) {
        for (Object* child : victim->out_) {
            if (!victim_set.count(child)) --child->ref_count_;
        }
        victim->out_.clear();
    }
    for (Object* victim : victims) {
        objects_.erase(victim);
        delete victim;
    }
    return victims.size();
}

std::vector<Object::Id> Graph::liveIds() const {
    std::vector<Object::Id> ids;
    ids.reserve(objects_.size());
    for (const Object* o : objects_) ids.push_back(o->id());
    return ids;
}

Graph::~Graph() {
    // Pre-clear edges so no destructor ever follows a pointer to another
    // object; deletion itself is a plain iterative loop.
    for (Object* o : objects_) o->out_.clear();
    for (Object* o : objects_) delete o;
}

}  // namespace objgraph
