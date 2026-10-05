#include <cstdlib>
#include <iostream>
#include <map>
#include <string>
#include <vector>

#include "dense_reference.hpp"
#include "fem/assembler.hpp"

using fem::Assembler;
using fem::AssemblyError;
using fem::ElementContribution;
using fem::Rational;

static int failures = 0;

#define CHECK(cond)                                                                              \
    do {                                                                                         \
        if (!(cond)) {                                                                           \
            ++failures;                                                                          \
            std::cerr << "FAIL " << __FILE__ << ':' << __LINE__ << ": " #cond << std::endl;      \
        }                                                                                        \
    } while (0)

static void checkAgainstDense(const Assembler& a, const ref::DenseSystem& d,
                              const std::string& label) {
    const auto dense = a.matrix().toDense();
    for (std::size_t i = 0; i < d.n; ++i) {
        for (std::size_t j = 0; j < d.n; ++j) {
            if (dense[i][j] != d.k[i][j]) {
                ++failures;
                std::cerr << "FAIL " << label << " K(" << i << "," << j << "): sparse "
                          << dense[i][j] << " != dense " << d.k[i][j] << std::endl;
            }
        }
        if (a.rhs()[i] != d.f[i]) {
            ++failures;
            std::cerr << "FAIL " << label << " b(" << i << "): sparse " << a.rhs()[i]
                      << " != dense " << d.f[i] << std::endl;
        }
    }
}

// Shared DOFs across elements; fractional contributions must accumulate
// exactly (1/3 + 1/6 = 1/2, no floating point drift).
static void sharedDofsExactAccumulation() {
    Assembler a(3);
    ref::DenseSystem d(3);

    ElementContribution e1{{{{Rational(1, 3), Rational(-1, 3)},
                             {Rational(-1, 3), Rational(1, 3)}}},
                           {Rational(1, 2), Rational(0)},
                           {0, 1}};
    ElementContribution e2{{{{Rational(1, 6), Rational(-1, 6)},
                             {Rational(-1, 6), Rational(1, 6)}}},
                           {Rational(0), Rational(1, 4)},
                           {1, 2}};
    a.addBatch({e1, e2});
    ref::assemble(d, e1);
    ref::assemble(d, e2);

    CHECK(a.matrix().get(1, 1) == Rational(1, 2)); // 1/3 + 1/6 exactly
    CHECK(a.matrix().get(0, 1) == Rational(-1, 3));
    CHECK(a.matrix().get(1, 2) == Rational(-1, 6));
    CHECK(a.rhs()[0] == Rational(1, 2));
    CHECK(a.rhs()[2] == Rational(1, 4));
    checkAgainstDense(a, d, "shared-dofs");
}

// A batch containing any illegal mapping must be rejected as a whole:
// nothing from the batch may be applied.
static void invalidMappingRejectsBatch() {
    Assembler a(3);
    ElementContribution good{{{{Rational(1), Rational(1)}, {Rational(1), Rational(1)}}},
                             {},
                             {0, 1}};
    a.addContribution(good);
    const auto before = a.matrix().toDense();

    const std::vector<ElementContribution> badVariants = {
        {{{Rational(1)}}, {}, {3}},                         // index out of range
        {{{{Rational(1), Rational(1)}, {Rational(1), Rational(1)}}}, {}, {0, 0}}, // dup
        {{{{Rational(1), Rational(1)}}, {Rational(1)}}, {}, {0, 1}}, // not square
        {{{Rational(1)}}, {Rational(1), Rational(1)}, {0}},          // fe mismatch
        {{{Rational(1)}}, {}, {0, 1}},                               // map mismatch
    };
    for (const auto& bad : badVariants) {
        bool threw = false;
        try {
            a.addBatch({good, bad}); // valid entry first, invalid second
        } catch (const AssemblyError&) {
            threw = true;
        }
        CHECK(threw);
    }
    // State must equal the single pre-batch contribution, exactly.
    CHECK(a.matrix().toDense() == before);
    CHECK(a.matrix().get(0, 0) == Rational(1));
    CHECK(a.matrix().get(2, 2).isZero());
}

// Prescribed displacement must correct the RHS via the eliminated column,
// not just drop the row/column. Hand-computed 1D Poisson, 3 DOFs.
static void dirichletRhsCorrection() {
    Assembler a(3);
    ref::DenseSystem d(3);
    // K = [1 -1 0; -1 2 -1; 0 -1 1] from two unit bar elements.
    ElementContribution e01{{{{Rational(1), Rational(-1)}, {Rational(-1), Rational(1)}}},
                            {},
                            {0, 1}};
    ElementContribution e12{{{{Rational(1), Rational(-1)}, {Rational(-1), Rational(1)}}},
                            {},
                            {1, 2}};
    ElementContribution load{{{{Rational(0)}}}, {Rational(1)}, {1}}; // unit load at dof 1
    a.addBatch({e01, e12, load});
    ref::assemble(d, e01);
    ref::assemble(d, e12);
    ref::assemble(d, load);

    a.prescribe(0, Rational(1, 2));
    a.eliminateConstraints();
    ref::eliminate(d, {{0, Rational(1, 2)}});

    // b_1 = 1 - K_10 * g = 1 - (-1)(1/2) = 3/2
    CHECK(a.rhs()[1] == Rational(3, 2));
    CHECK(a.rhs()[0] == Rational(1, 2));
    CHECK(a.rhs()[2] == Rational(0));
    CHECK(a.matrix().get(0, 0) == Rational(1));
    CHECK(a.matrix().get(0, 1).isZero());
    CHECK(a.matrix().get(1, 0).isZero());
    CHECK(a.matrix().get(1, 1) == Rational(2));
    checkAgainstDense(a, d, "dirichlet");
}

// Two constrained DOFs coupled by stiffness: corrections must use the
// pre-elimination matrix and the result must not depend on prescription
// order.
static void constraintCoupling() {
    Assembler a(4);
    ref::DenseSystem d(4);
    ElementContribution e{
        {{{Rational(2), Rational(-1), Rational(0), Rational(-1)},
          {Rational(-1), Rational(3), Rational(-1), Rational(0)},
          {Rational(0), Rational(-1), Rational(2), Rational(-1)},
          {Rational(-1), Rational(0), Rational(-1), Rational(2)}}},
        {Rational(1), Rational(2), Rational(3), Rational(4)},
        {0, 1, 2, 3}};
    a.addContribution(e);
    ref::assemble(d, e);

    // DOFs 0 and 1 are coupled (K_01 = -1) and both constrained.
    a.prescribe(0, Rational(3, 2));
    a.prescribe(1, Rational(-1, 2));
    a.eliminateConstraints();
    ref::eliminate(d, {{0, Rational(3, 2)}, {1, Rational(-1, 2)}});

    // b_2 = 3 - K_20*(3/2) - K_21*(-1/2) = 3 - 0 - (-1)(-1/2) = 5/2
    CHECK(a.rhs()[2] == Rational(5, 2));
    // b_3 = 4 - K_30*(3/2) - K_31*(-1/2) = 4 - (-1)(3/2) - 0 = 11/2
    CHECK(a.rhs()[3] == Rational(11, 2));
    CHECK(a.rhs()[0] == Rational(3, 2));
    CHECK(a.rhs()[1] == Rational(-1, 2));
    CHECK(a.matrix().get(0, 1).isZero());
    CHECK(a.matrix().get(1, 0).isZero());
    CHECK(a.matrix().get(0, 0) == Rational(1));
    CHECK(a.matrix().get(1, 1) == Rational(1));
    checkAgainstDense(a, d, "constraint-coupling");
}

// A DOF untouched by any element keeps an exactly zero row/column before
// and after constraint elimination elsewhere.
static void zeroRows() {
    Assembler a(4);
    ref::DenseSystem d(4);
    ElementContribution e{{{{Rational(1), Rational(-1)}, {Rational(-1), Rational(1)}}},
                          {},
                          {0, 1}};
    a.addContribution(e);
    ref::assemble(d, e);
    a.prescribe(0, Rational(2));
    a.eliminateConstraints();
    ref::eliminate(d, {{0, Rational(2)}});

    for (std::size_t j = 0; j < 4; ++j) {
        CHECK(a.matrix().get(3, j).isZero());
        CHECK(a.matrix().get(j, 3).isZero());
    }
    CHECK(a.rhs()[3].isZero());
    // Structural zero rows must not appear as stored entries.
    for (const auto& kv : a.matrix().entries()) CHECK(kv.first.first != 3 && kv.first.second != 3);
    checkAgainstDense(a, d, "zero-rows");
}

// The local-to-global map may reorder local DOFs arbitrarily.
static void localDofPermutation() {
    Assembler a(3);
    ref::DenseSystem d(3);
    ElementContribution e{
        {{{Rational(1), Rational(2), Rational(3)},
          {Rational(4), Rational(5), Rational(6)},
          {Rational(7), Rational(8), Rational(9)}}},
        {Rational(1, 2), Rational(1, 3), Rational(1, 6)},
        {2, 0, 1}}; // local 0->global 2, local 1->global 0, local 2->global 1
    a.addContribution(e);
    ref::assemble(d, e);

    CHECK(a.matrix().get(2, 0) == Rational(2)); // local (0,1)
    CHECK(a.matrix().get(0, 1) == Rational(6)); // local (1,2)
    CHECK(a.matrix().get(1, 0) == Rational(8)); // local (2,1)
    CHECK(a.matrix().get(1, 2) == Rational(7)); // local (2,0)
    CHECK(a.matrix().get(0, 0) == Rational(5)); // local (1,1)
    CHECK(a.rhs()[2] == Rational(1, 2));
    CHECK(a.rhs()[0] == Rational(1, 3));
    CHECK(a.rhs()[1] == Rational(1, 6));
    checkAgainstDense(a, d, "permutation");
}

// End-to-end: assemble, constrain, solve exactly, verify the solution
// honors the prescribed values and satisfies the eliminated system.
static void solveWithConstraints() {
    Assembler a(3);
    ref::DenseSystem d(3);
    ElementContribution e01{{{{Rational(1), Rational(-1)}, {Rational(-1), Rational(1)}}},
                            {},
                            {0, 1}};
    ElementContribution e12{{{{Rational(1), Rational(-1)}, {Rational(-1), Rational(1)}}},
                            {},
                            {1, 2}};
    a.addBatch({e01, e12});
    ref::assemble(d, e01);
    ref::assemble(d, e12);
    // Load f_2 = 1 via a rank-1 contribution.
    ElementContribution load{{{{Rational(0)}}}, {Rational(1)}, {2}};
    a.addContribution(load);
    ref::assemble(d, load);

    a.prescribe(0, Rational(0));
    a.prescribe(2, Rational(1, 2));
    a.eliminateConstraints();
    ref::eliminate(d, {{0, Rational(0)}, {2, Rational(1, 2)}});
    checkAgainstDense(a, d, "solve");

    const auto x = ref::solve(d);
    CHECK(x[0] == Rational(0));
    CHECK(x[2] == Rational(1, 2));
    // Row 1 of the original system: -u0 + 2*u1 - u2 = 0 -> u1 = 1/4.
    CHECK(x[1] == Rational(1, 4));
}

int main() {
    sharedDofsExactAccumulation();
    invalidMappingRejectsBatch();
    dirichletRhsCorrection();
    constraintCoupling();
    zeroRows();
    localDofPermutation();
    solveWithConstraints();

    if (failures == 0) {
        std::cout << "all tests passed" << std::endl;
        return EXIT_SUCCESS;
    }
    std::cerr << failures << " check(s) failed" << std::endl;
    return EXIT_FAILURE;
}
