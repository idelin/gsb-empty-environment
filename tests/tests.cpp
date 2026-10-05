#include "isel/selector.h"

#include <cassert>
#include <iostream>

using namespace isel;

namespace {

Pattern leaf(int id, long long cost, int rc, const char* op) {
    Pattern p;
    p.id = id;
    p.cost = cost;
    p.resultClass = rc;
    PatternNode pn;
    pn.op = op;
    p.nodes.push_back(std::move(pn));
    return p;
}

Pattern unary(int id, long long cost, int rc, const char* op, int childClass) {
    Pattern p;
    p.id = id;
    p.cost = cost;
    p.resultClass = rc;
    PatternNode root;
    root.op = op;
    root.children = {1};
    PatternNode ph;
    ph.placeholder = true;
    ph.regClass = childClass;
    p.nodes = {root, ph};
    return p;
}

Pattern binary(int id, long long cost, int rc, const char* op, int lhsClass, int rhsClass) {
    Pattern p;
    p.id = id;
    p.cost = cost;
    p.resultClass = rc;
    PatternNode root;
    root.op = op;
    root.children = {1, 2};
    PatternNode l, r;
    l.placeholder = r.placeholder = true;
    l.regClass = lhsClass;
    r.regClass = rhsClass;
    p.nodes = {root, l, r};
    return p;
}

Problem base(int k) {
    Problem prob;
    prob.numClasses = k;
    prob.copyCost.assign(k, std::vector<long long>(k, kInfCost));
    for (int i = 0; i < k; ++i) prob.copyCost[i][i] = 0;
    return prob;
}

// add(mul(a,b),c): fused madd ties with separate mul+add -> 2 optima, cost 5.
void testFusionTie() {
    Problem prob = base(1);
    prob.nodes = {{"const", {}}, {"const", {}}, {"mul", {0, 1}}, {"const", {}}, {"add", {2, 3}}};
    prob.outputs = {{4, 0}};
    prob.patterns = {leaf(0, 1, 0, "const"), binary(1, 1, 0, "mul", 0, 0),
                     binary(2, 1, 0, "add", 0, 0)};
    {
        Pattern madd;
        madd.id = 3;
        madd.cost = 2;
        madd.resultClass = 0;
        // (add (mul ?0 ?0) ?0)
        PatternNode root, mul, a, b, c;
        root.op = "add";
        root.children = {1, 4};
        mul.op = "mul";
        mul.children = {2, 3};
        a.placeholder = b.placeholder = c.placeholder = true;
        a.regClass = b.regClass = c.regClass = 0;
        madd.nodes = {root, mul, a, b, c};
        prob.patterns.push_back(madd);
    }
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 5);
    assert(s.numOptimal == 2);
    assert(s.cover[4].used && s.cover[4].isTileRoot);
    std::cout << "testFusionTie ok\n";
}

// Producer cheapest in class 0, consumer needs class 1: explicit copy wins
// over producing directly in class 1.
void testRegClassCopy() {
    Problem prob = base(2);
    prob.copyCost[0][1] = 2;
    prob.copyCost[1][0] = 2;
    prob.nodes = {{"ld", {}}, {"fneg", {0}}};
    prob.outputs = {{1, 0}};
    prob.patterns = {leaf(0, 1, 0, "ld"), leaf(1, 5, 1, "ld"), unary(2, 1, 0, "fneg", 1)};
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 4); // ld(1) + copy 0->1(2) + fneg(1)
    assert(s.cover[0].isTileRoot && s.cover[0].patternId == 0 && s.cover[0].resultClass == 0);
    assert(s.numOptimal == 1);
    std::cout << "testRegClassCopy ok\n";
}

// x = a*b used twice: computed once (7) + zero-cost copies + add(1) = 8.
// A plain tree treatment would charge the mul subtree twice (15).
void testSharedSubexpression() {
    Problem prob = base(1);
    prob.nodes = {{"ld", {}}, {"ld", {}}, {"mul", {0, 1}}, {"add", {2, 2}}};
    prob.outputs = {{3, 0}};
    prob.patterns = {leaf(0, 2, 0, "ld"), binary(1, 3, 0, "mul", 0, 0),
                     binary(2, 1, 0, "add", 0, 0)};
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 8);
    assert(s.cover[2].isTileRoot); // shared node must be a tile root
    assert(s.numOptimal == 1);
    std::cout << "testSharedSubexpression ok\n";
}

// Same DAG, but add consumes class 1: each of the two uses pays copy 0->1 = 2.
void testSharedWithCopies() {
    Problem prob = base(2);
    prob.copyCost[0][1] = 2;
    prob.copyCost[1][0] = 2;
    prob.nodes = {{"ld", {}}, {"ld", {}}, {"mul", {0, 1}}, {"add", {2, 2}}};
    prob.outputs = {{3, 0}};
    prob.patterns = {leaf(0, 2, 0, "ld"), binary(1, 3, 0, "mul", 0, 0),
                     binary(2, 1, 0, "add", 1, 1)};
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 12); // 7 (mul subtree) + 2+2 (copies) + 1 (add)
    std::cout << "testSharedWithCopies ok\n";
}

// A fusion pattern must NOT absorb a shared node: madd stays inapplicable.
void testSharedNotAbsorbed() {
    Problem prob = base(1);
    prob.nodes = {{"ld", {}}, {"ld", {}}, {"mul", {0, 1}}, {"add", {2, 2}}};
    prob.outputs = {{3, 0}};
    prob.patterns = {leaf(0, 2, 0, "ld"), binary(1, 3, 0, "mul", 0, 0),
                     binary(2, 1, 0, "add", 0, 0)};
    {
        Pattern madd; // (add (mul ?0 ?0) ?0), cheaper, but mul is shared
        madd.id = 3;
        madd.cost = 1;
        madd.resultClass = 0;
        PatternNode root, mul, a, b, c;
        root.op = "add";
        root.children = {1, 4};
        mul.op = "mul";
        mul.children = {2, 3};
        a.placeholder = b.placeholder = c.placeholder = true;
        a.regClass = b.regClass = c.regClass = 0;
        madd.nodes = {root, mul, a, b, c};
        prob.patterns.push_back(madd);
    }
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 8); // not 1+2+2=5: fusion would duplicate the shared mul
    assert(s.cover[2].isTileRoot);
    std::cout << "testSharedNotAbsorbed ok\n";
}

// Shared node with two equally priced patterns: 2 optima, not 4
// (the shared subtree is covered once, not once per use).
void testSharedTieCountedOnce() {
    Problem prob = base(1);
    prob.nodes = {{"ld", {}}, {"ld", {}}, {"mul", {0, 1}}, {"add", {2, 2}}};
    prob.outputs = {{3, 0}};
    prob.patterns = {leaf(0, 2, 0, "ld"), binary(1, 3, 0, "mul", 0, 0),
                     binary(2, 3, 0, "mul", 0, 0), // duplicate mul pattern, same cost
                     binary(3, 1, 0, "add", 0, 0)};
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 8);
    assert(s.numOptimal == 2);
    std::cout << "testSharedTieCountedOnce ok\n";
}

// Tie in the delivery class of a tree child -> 2 optima.
void testChildClassTie() {
    Problem prob = base(2);
    prob.copyCost[0][1] = 2;
    prob.copyCost[1][0] = 2;
    prob.nodes = {{"ld", {}}, {"fneg", {0}}};
    prob.outputs = {{1, 0}};
    prob.patterns = {leaf(0, 1, 0, "ld"), leaf(1, 3, 1, "ld"), // 1+2 == 3+0: tie
                     unary(2, 1, 0, "fneg", 1)};
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 4);
    assert(s.numOptimal == 2);
    std::cout << "testChildClassTie ok\n";
}

// Output requires a class nobody can produce and no copy reaches it.
void testInfeasible() {
    Problem prob = base(2);
    prob.nodes = {{"ld", {}}};
    prob.outputs = {{0, 1}};
    prob.patterns = {leaf(0, 1, 0, "ld")};
    Solution s = select(prob);
    assert(!s.feasible);
    std::cout << "testInfeasible ok\n";
}

// Two outputs, one of them also used as an operand (shared via output use).
void testMultiOutput() {
    Problem prob = base(1);
    prob.nodes = {{"ld", {}}, {"ld", {}}, {"add", {0, 1}}};
    prob.outputs = {{2, 0}, {0, 0}};
    prob.patterns = {leaf(0, 2, 0, "ld"), binary(1, 1, 0, "add", 0, 0)};
    Solution s = select(prob);
    assert(s.feasible);
    assert(s.totalCost == 5); // ld(2) + ld(2) + add(1); node 0 computed once
    assert(s.cover[0].isTileRoot);
    std::cout << "testMultiOutput ok\n";
}

} // namespace

int main() {
    testFusionTie();
    testRegClassCopy();
    testSharedSubexpression();
    testSharedWithCopies();
    testSharedNotAbsorbed();
    testSharedTieCountedOnce();
    testChildClassTie();
    testInfeasible();
    testMultiOutput();
    std::cout << "all tests passed\n";
    return 0;
}
