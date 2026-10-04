// Dense-reference cross-checks for the FEM assembly library.
// All test values are integers or halves, exactly representable in double,
// so accumulation must match the reference bit-for-bit.

#include "fem/assembly.hpp"

#include <cassert>
#include <cmath>
#include <cstdlib>
#include <iostream>
#include <map>
#include <vector>

using fem::Assembler;
using fem::AssemblyError;
using fem::ElementContribution;
using fem::Index;

namespace {

struct DenseRef {
    Index n;
    std::vector<std::vector<double>> a;
    std::vector<double> b;

    explicit DenseRef(Index n_) : n(n_), a(n_, std::vector<double>(n_, 0.0)), b(n_, 0.0) {}

    void add(const ElementContribution<double>& e) {
        for (Index r = 0; r < e.rows; ++r) {
            Index gi = e.dofs[r];
            b[gi] += e.rhs[r];
            for (Index c = 0; c < e.rows; ++c) {
                a[gi][e.dofs[c]] += e.matrix[r * e.rows + c];
            }
        }
    }

    void applyDirichlet(const std::map<Index, double>& prescribed) {
        std::vector<double> corr(n, 0.0);
        for (const auto& kv : prescribed) {
            for (Index j = 0; j < n; ++j) corr[j] += a[j][kv.first] * kv.second;
        }
        for (Index j = 0; j < n; ++j) b[j] -= corr[j];
        for (const auto& kv : prescribed) {
            Index i = kv.first;
            for (Index j = 0; j < n; ++j) { a[i][j] = 0.0; a[j][i] = 0.0; }
            a[i][i] = 1.0;
            b[i] = kv.second;
        }
    }
};

ElementContribution<double> makeElement(const std::vector<Index>& dofs,
                                        const std::vector<double>& mat,
                                        const std::vector<double>& rhs) {
    ElementContribution<double> e;
    e.rows = dofs.size();
    e.matrix = mat;
    e.rhs = rhs;
    e.dofs = dofs;
    return e;
}

void expectNear(double x, double y, const char* what) {
    if (x != y) {  // exact: all test data is dyadic
        std::cerr << "FAIL: " << what << " got " << x << " expected " << y << "\n";
        std::exit(1);
    }
}

void checkAgainstRef(const Assembler<double>& asmbl, const DenseRef& ref, const char* tag) {
    auto dense = asmbl.matrix().dense();
    for (Index i = 0; i < ref.n; ++i) {
        for (Index j = 0; j < ref.n; ++j) {
            if (dense[i][j] != ref.a[i][j]) {
                std::cerr << "FAIL [" << tag << "]: A(" << i << "," << j << ") got "
                          << dense[i][j] << " expected " << ref.a[i][j] << "\n";
                std::exit(1);
            }
        }
        expectNear(asmbl.rhs()[i], ref.b[i], tag);
    }
}

// 1. Shared dofs across elements + repeated contribution accumulates exactly.
void testSharedDofsAndExactAccumulation() {
    Assembler<double> asmbl(4);
    DenseRef ref(4);
    // 2x2 element stiffness on dofs {0,1}
    auto e1 = makeElement({0, 1}, {2, -1, -1, 2}, {1, 0});
    // 2x2 element on dofs {1,2} sharing dof 1
    auto e2 = makeElement({1, 2}, {4, -2, -2, 4}, {0.5, 0.5});
    // 2x2 element on dofs {2,3}
    auto e3 = makeElement({2, 3}, {6, -3, -3, 6}, {0, 2});

    asmbl.addBatch({e1, e2, e3});
    ref.add(e1); ref.add(e2); ref.add(e3);
    checkAgainstRef(asmbl, ref, "shared-dofs");

    // Add the exact same batch again: every entry must double exactly.
    Assembler<double> asmbl2(4);
    asmbl2.addBatch({e1, e2, e3});
    asmbl2.addBatch({e1, e2, e3});
    auto d = asmbl2.matrix().dense();
    for (Index i = 0; i < 4; ++i)
        for (Index j = 0; j < 4; ++j)
            expectNear(d[i][j], 2.0 * ref.a[i][j], "exact-double");
    for (Index i = 0; i < 4; ++i)
        expectNear(asmbl2.rhs()[i], 2.0 * ref.b[i], "exact-double-rhs");

    // Spot-check shared dof 1 diagonal: 2 (from e1) + 4 (from e2) = 6.
    expectNear(asmbl.matrix().get(1, 1), 6.0, "shared-diag");
    std::cout << "ok: shared dofs + exact accumulation\n";
}

// 2. Dirichlet elimination with coupling between two constrained dofs.
void testDirichletCoupling() {
    Assembler<double> asmbl(4);
    DenseRef ref(4);
    // Chain 0-1-2-3, unit stiffness on each link.
    auto e01 = makeElement({0, 1}, {1, -1, -1, 1}, {0, 0});
    auto e12 = makeElement({1, 2}, {1, -1, -1, 1}, {0, 0});
    auto e23 = makeElement({2, 3}, {1, -1, -1, 1}, {0, 0});
    auto load = makeElement({2}, {0}, {3});  // point load on dof 2
    asmbl.addBatch({e01, e12, e23, load});
    ref.add(e01); ref.add(e12); ref.add(e23); ref.add(load);

    // Constrain dofs 0 and 1, which are directly coupled (A(0,1) = -1).
    std::map<Index, double> prescribed{{0, 2.0}, {1, 0.5}};
    asmbl.applyDirichlet(prescribed);
    ref.applyDirichlet(prescribed);
    checkAgainstRef(asmbl, ref, "dirichlet-coupling");

    // Explicit expectations:
    // rhs[2] = 3 - A(2,1)*0.5 = 3 - (-1)*0.5 = 3.5
    expectNear(asmbl.rhs()[2], 3.5, "rhs-correction");
    // rhs[3] = 0 (no coupling to constrained dofs)
    expectNear(asmbl.rhs()[3], 0.0, "rhs-uncoupled");
    // Constrained rows are identity rows with rhs = prescribed value,
    // unaffected by the mutual coupling correction.
    expectNear(asmbl.rhs()[0], 2.0, "rhs-prescribed-0");
    expectNear(asmbl.rhs()[1], 0.5, "rhs-prescribed-1");
    expectNear(asmbl.matrix().get(0, 0), 1.0, "diag-0");
    expectNear(asmbl.matrix().get(1, 1), 1.0, "diag-1");
    expectNear(asmbl.matrix().get(0, 1), 0.0, "coupling-zeroed");
    // Column entries moved to RHS: A(2,1) must be zero now.
    expectNear(asmbl.matrix().get(2, 1), 0.0, "col-zeroed");
    std::cout << "ok: dirichlet coupling + rhs correction\n";
}

// 3. Zero rows from unconnected dofs are detected and regularized.
void testZeroRows() {
    Assembler<double> asmbl(5);
    auto e = makeElement({0, 2}, {1, -1, -1, 1}, {1, 1});
    asmbl.add(e);
    auto zeros = asmbl.matrix().zeroRows();
    std::vector<Index> expected{1, 3, 4};
    assert(zeros == expected);

    auto fixed = asmbl.regularizeZeroRows();
    assert(fixed == expected);
    assert(asmbl.matrix().zeroRows().empty());
    expectNear(asmbl.matrix().get(1, 1), 1.0, "regularized-diag");
    expectNear(asmbl.rhs()[1], 0.0, "regularized-rhs");
    // Untouched entries remain.
    expectNear(asmbl.matrix().get(0, 2), -1.0, "untouched-entry");
    std::cout << "ok: zero rows\n";
}

// 4. Local dof reordering: permuted map must land entries correctly.
void testLocalDofReordering() {
    Assembler<double> asmbl(3);
    DenseRef ref(3);
    // Same physical element, local numbering deliberately scrambled.
    auto e = makeElement({2, 0, 1},
                         {9, 1, 2,
                          3, 8, 4,
                          5, 6, 7},
                         {0.5, 1.0, 1.5});
    asmbl.add(e);
    ref.add(e);
    checkAgainstRef(asmbl, ref, "reordering");
    // Local row 0 -> global 2, local col 1 -> global 0: A(2,0) = 1.
    expectNear(asmbl.matrix().get(2, 0), 1.0, "reorder-A20");
    expectNear(asmbl.matrix().get(0, 0), 8.0, "reorder-A00");
    expectNear(asmbl.rhs()[2], 0.5, "reorder-rhs2");
    std::cout << "ok: local dof reordering\n";
}

// 5. Invalid mapping rejects the whole batch; state stays untouched.
void testBatchRejection() {
    Assembler<double> asmbl(3);
    auto good = makeElement({0, 1}, {2, -1, -1, 2}, {1, 1});
    asmbl.add(good);
    auto before = asmbl.matrix().dense();
    auto beforeRhs = asmbl.rhs();

    auto alsoGood = makeElement({1, 2}, {1, 0, 0, 1}, {5, 5});
    auto badIndex = makeElement({1, 7}, {1, 0, 0, 1}, {0, 0});   // dof 7 out of range
    auto badSize = makeElement({0, 1}, {1, 0, 0}, {0, 0});        // matrix too small
    auto badDup = makeElement({2, 2}, {1, 0, 0, 1}, {0, 0});      // duplicate dof
    auto badRhs = makeElement({0}, {1}, {1, 2});                  // rhs size mismatch

    for (const auto* bad : {&badIndex, &badSize, &badDup, &badRhs}) {
        bool threw = false;
        try {
            asmbl.addBatch({alsoGood, *bad});
        } catch (const AssemblyError&) {
            threw = true;
        }
        assert(threw);
        // Nothing from the batch may have been applied.
        assert(asmbl.matrix().dense() == before);
        assert(asmbl.rhs() == beforeRhs);
    }

    // And a clean batch still applies.
    asmbl.addBatch({alsoGood});
    expectNear(asmbl.rhs()[2], 5.0, "post-rejection-apply");
    std::cout << "ok: invalid mapping rejects whole batch\n";
}

} // namespace

int main() {
    testSharedDofsAndExactAccumulation();
    testDirichletCoupling();
    testZeroRows();
    testLocalDofReordering();
    testBatchRejection();
    std::cout << "all tests passed\n";
    return 0;
}
