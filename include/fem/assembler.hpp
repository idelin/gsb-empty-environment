#pragma once

#include <cstddef>
#include <map>
#include <stdexcept>
#include <string>
#include <unordered_set>
#include <vector>

#include "fem/rational.hpp"
#include "fem/sparse_matrix.hpp"

namespace fem {

class AssemblyError : public std::runtime_error {
public:
    explicit AssemblyError(const std::string& msg) : std::runtime_error(msg) {}
};

// One element's local contribution plus its local-to-global DOF map.
// dofMap[local] = global; local DOF order may be an arbitrary permutation.
struct ElementContribution {
    std::vector<std::vector<Rational>> ke; // square local stiffness
    std::vector<Rational> fe;              // local load, empty or ke.size()
    std::vector<std::size_t> dofMap;       // size == ke.size()
};

// Assembles local rational matrices into a global sparse matrix and RHS,
// then eliminates prescribed-displacement (Dirichlet) constraints with
// full RHS correction.
class Assembler {
public:
    explicit Assembler(std::size_t numDofs) : n_(numDofs), k_(numDofs), f_(numDofs) {}

    std::size_t numDofs() const { return n_; }

    // Atomically applies a batch of contributions. Every mapping is
    // validated first; if any is illegal the whole batch is rejected
    // and the assembler state is left untouched.
    void addBatch(const std::vector<ElementContribution>& batch) {
        for (std::size_t e = 0; e < batch.size(); ++e)
            validate(batch[e], e);
        for (const auto& c : batch) apply(c);
    }

    void addContribution(const ElementContribution& c) { addBatch({c}); }

    // Prescribe u[dof] = value. Repeated prescriptions on the same DOF
    // overwrite; elimination happens in eliminateConstraints().
    void prescribe(std::size_t dof, const Rational& value) {
        if (dof >= n_) throw AssemblyError("prescribed DOF out of range");
        constraints_[dof] = value;
    }

    // Eliminates prescribed DOFs. For every constrained DOF d with value g:
    //   b_j -= K_jd * g   for all unconstrained rows j   (RHS correction)
    //   row d and column d of K are zeroed
    //   K_dd = 1, b_d = g
    // Corrections are computed from the pre-elimination stiffness, so
    // couplings between mutually constrained DOFs are handled consistently
    // regardless of prescription order.
    void eliminateConstraints() {
        std::vector<bool> constrained(n_, false);
        for (const auto& kv : constraints_) constrained[kv.first] = true;

        // Snapshot entries: corrections must use unmodified K.
        const auto entries = k_.entries();
        for (const auto& kv : entries) {
            const std::size_t i = kv.first.first;
            const std::size_t j = kv.first.second;
            const bool rowC = constrained[i];
            const bool colC = constrained[j];
            if (!rowC && colC) f_[i] -= kv.second * constraints_.at(j);
            if (rowC || colC) k_.remove(i, j);
        }
        for (const auto& kv : constraints_) {
            k_.set(kv.first, kv.first, Rational(1));
            f_[kv.first] = kv.second;
        }
        constraints_.clear();
    }

    const SparseMatrix& matrix() const { return k_; }
    const std::vector<Rational>& rhs() const { return f_; }

private:
    void validate(const ElementContribution& c, std::size_t index) const {
        const std::string where = "contribution " + std::to_string(index) + ": ";
        const std::size_t m = c.ke.size();
        if (m == 0) throw AssemblyError(where + "empty local matrix");
        for (const auto& row : c.ke)
            if (row.size() != m) throw AssemblyError(where + "local matrix not square");
        if (!c.fe.empty() && c.fe.size() != m)
            throw AssemblyError(where + "local load size mismatch");
        if (c.dofMap.size() != m) throw AssemblyError(where + "DOF map size mismatch");
        std::unordered_set<std::size_t> seen;
        for (const std::size_t g : c.dofMap) {
            if (g >= n_) throw AssemblyError(where + "DOF map index out of range");
            if (!seen.insert(g).second)
                throw AssemblyError(where + "DOF map contains duplicate index");
        }
    }

    void apply(const ElementContribution& c) {
        const std::size_t m = c.ke.size();
        for (std::size_t a = 0; a < m; ++a) {
            const std::size_t gi = c.dofMap[a];
            for (std::size_t b = 0; b < m; ++b) k_.add(gi, c.dofMap[b], c.ke[a][b]);
            if (!c.fe.empty()) f_[gi] += c.fe[a];
        }
    }

    std::size_t n_;
    SparseMatrix k_;
    std::vector<Rational> f_;
    std::map<std::size_t, Rational> constraints_;
};

} // namespace fem
