#pragma once

#include <string>
#include <utility>
#include <vector>

namespace isel {

// "Infinity" for costs: anything >= kInfCost is treated as infeasible.
inline constexpr long long kInfCost = 1LL << 60;

// Saturation cap for the number of tied optimal solutions.
inline constexpr unsigned long long kCountCap = 4000000000000000000ULL; // 4e18

// One DAG node. Operands are indices of earlier nodes (topological order).
struct Node {
    std::string op;
    std::vector<int> operands;
};

// A pattern tree node: either a placeholder leaf (matches any DAG node whose
// value is delivered in regClass) or an internal op node.
struct PatternNode {
    bool placeholder = false;
    int regClass = -1;       // valid when placeholder
    std::string op;          // valid when !placeholder
    std::vector<int> children; // indices into Pattern::nodes
};

struct Pattern {
    int id = -1;
    long long cost = 0;
    int resultClass = 0;         // register class the pattern root produces
    std::vector<PatternNode> nodes; // nodes[0] is the root; root must not be a placeholder
};

struct Problem {
    int numClasses = 0;
    // copyCost[from][to]: cost of moving a value between register classes.
    // Diagonal is 0; kInfCost means the copy is impossible.
    std::vector<std::vector<long long>> copyCost;
    std::vector<Node> nodes;                  // topological order (operands first)
    std::vector<Pattern> patterns;
    std::vector<std::pair<int, int>> outputs; // (node, required result class)
};

// How a single DAG node is covered by the selected tiling.
struct NodeCover {
    bool used = false;       // reachable from an output and covered
    bool isTileRoot = false; // this node is the root of a tile instance
    int patternId = -1;      // pattern of the tile covering this node
    int resultClass = -1;    // tile roots only: class the value is delivered in
    int tileRoot = -1;       // node id of the root of the tile covering this node
};

struct Solution {
    bool feasible = false;
    long long totalCost = 0;
    unsigned long long numOptimal = 0; // number of tied optimal tilings (saturated at kCountCap)
    std::vector<NodeCover> cover;      // per-node assignment
};

// Cost model (see README.md):
//  - Every reachable node is covered by exactly one tile instance.
//  - Cost = sum of tile costs + sum over placeholder edges of
//    copyCost[class of producer][class required by the placeholder].
//  - A shared node (used more than once, counting output uses) is computed
//    once, in its cheapest production class; every use pays an explicit copy
//    from that class. Shared nodes are therefore always tile roots and are
//    never absorbed into a larger pattern.
Solution select(const Problem& prob);

} // namespace isel
