"""Counting, k-th word and ranking over a lazy product automaton.

Two strictly separate modes:

* ``exact``  -- arbitrary precision integers (required for k-th / ranking);
* ``mod``    -- all arithmetic reduced modulo ``modulus`` (counting only).

Large lengths are handled with reusable power blocks: for the transition
matrix ``M`` we cache ``M^(2^i)`` together with the cumulative sums
``I + M + ... + M^(2^i - 1)`` so both "exactly length n" and "length at
most n" queries cost O(log n) sparse matrix/vector products.  The blocks
are shared by every query of the same counter and are rebuilt automatically
whenever the product version changes (cache invalidation on mutation).
"""


class _PowerBlocks:
    """Reusable blocks ``M^(2^i)`` and ``S_(2^i) = sum_{l<2^i} M^l``."""

    def __init__(self, matrix, mod):
        self.mod = mod
        n = len(matrix)
        self.power = [matrix]                       # M^(2^0)
        self.sums = [[{i: 1} for i in range(n)]]    # S_1 = I

    def _matmul(self, left, right):
        mod = self.mod
        out = []
        for row in left:
            acc = {}
            for mid, a in row.items():
                for dst, b in right[mid].items():
                    acc[dst] = acc.get(dst, 0) + a * b
            if mod:
                acc = {k: v % mod for k, v in acc.items() if v % mod}
            else:
                acc = {k: v for k, v in acc.items() if v}
            out.append(acc)
        return out

    def _matadd(self, left, right):
        mod = self.mod
        out = []
        for lrow, rrow in zip(left, right):
            row = dict(lrow)
            for dst, b in rrow.items():
                row[dst] = row.get(dst, 0) + b
            if mod:
                row = {k: v % mod for k, v in row.items() if v % mod}
            else:
                row = {k: v for k, v in row.items() if v}
            out.append(row)
        return out

    def ensure(self, n):
        """Make sure blocks exist for every bit position of ``n``."""
        while n >> len(self.power):
            top = self.power[-1]
            self.sums.append(self._matadd(self.sums[-1], self._matmul(top, self.sums[-1])))
            self.power.append(self._matmul(top, top))

    def mat_vec(self, mat, vec):
        mod = self.mod
        out = {}
        for src, val in vec.items():
            for dst, a in mat[src].items():
                out[dst] = out.get(dst, 0) + a * val
        if mod:
            return {k: v % mod for k, v in out.items() if v % mod}
        return {k: v for k, v in out.items() if v}

    def power_vec(self, n, idx):
        """Return ``M^n @ e_idx`` as a sparse vector."""
        self.ensure(n)
        vec = {idx: 1}
        bit = 0
        while n:
            if n & 1:
                vec = self.mat_vec(self.power[bit], vec)
            n >>= 1
            bit += 1
        return vec

    def cum_vec(self, n, idx):
        """Return ``S_n @ e_idx`` where ``S_n = sum_{l<n} M^l``."""
        self.ensure(n)
        cur = {idx: 1}
        res = {}
        bit = 0
        while n:
            if n & 1:
                res = self._vecadd(res, self.mat_vec(self.sums[bit], cur))
                cur = self.mat_vec(self.power[bit], cur)
            n >>= 1
            bit += 1
        return res

    def _vecadd(self, left, right):
        mod = self.mod
        out = dict(left)
        for k, v in right.items():
            out[k] = out.get(k, 0) + v
        if mod:
            return {k: v % mod for k, v in out.items() if v % mod}
        return {k: v for k, v in out.items() if v}


class Counter:
    """Counts / selects words of a product automaton in a length interval."""

    def __init__(self, product, mode="exact", modulus=None):
        if mode not in ("exact", "mod"):
            raise ValueError("mode must be 'exact' or 'mod'")
        if mode == "mod":
            if not isinstance(modulus, int) or modulus < 2:
                raise ValueError("mod mode requires an integer modulus >= 2")
        elif modulus is not None:
            raise ValueError("modulus is only meaningful in mod mode")
        self.product = product
        self.mode = mode
        self.modulus = modulus
        self._cached_version = None
        self._blocks = None

    # -- lazy graph + cache invalidation ------------------------------------
    def _refresh(self):
        version = self.product.version
        if self._cached_version == version:
            return
        product = self.product
        index = {product.initial(): 0}
        states = [product.initial()]
        matrix = []
        accept = set()
        frontier = [product.initial()]
        while frontier:
            pstate = frontier.pop()
            src = index[pstate]
            while len(matrix) <= src:
                matrix.append({})
            row = {}
            for sym in product.alphabet:
                nxt = product.step(pstate, sym)
                if nxt is None:
                    continue
                if nxt not in index:
                    index[nxt] = len(states)
                    states.append(nxt)
                    frontier.append(nxt)
                row[sym] = index[nxt]
            matrix[src] = row
            if product.is_accept(pstate):
                accept.add(src)
        # transition matrix as count matrix (0/1 entries, DFA)
        count_matrix = [dict() for _ in states]
        for src, row in enumerate(matrix):
            for dst in row.values():
                count_matrix[src][dst] = count_matrix[src].get(dst, 0) + 1
        self._states = states
        self._trans = matrix
        self._accept = accept
        self._initial_idx = index[product.initial()]
        self._blocks = _PowerBlocks(count_matrix, self.modulus if self.mode == "mod" else None)
        self._cached_version = version

    def _require_exact(self, what):
        if self.mode != "exact":
            raise ValueError(f"{what} requires exact counting mode; "
                             "modular counts cannot drive selection")

    # -- primitive counting ---------------------------------------------------
    def _accept_dot(self, vec):
        total = sum(vec.get(j, 0) for j in self._accept)
        return total % self.modulus if self.mode == "mod" else total

    def count_length(self, n, _idx=None):
        """Number of accepted words of exactly length ``n``."""
        if n < 0:
            return 0
        self._refresh()
        idx = self._initial_idx if _idx is None else _idx
        return self._accept_dot(self._blocks.power_vec(n, idx))

    def count_upto(self, n, _idx=None):
        """Number of accepted words of length in ``[0, n]``."""
        if n < 0:
            return 0
        self._refresh()
        idx = self._initial_idx if _idx is None else _idx
        return self._accept_dot(self._blocks.cum_vec(n + 1, idx))

    def _count_range(self, idx, lo, hi):
        """Accepted continuations from ``idx`` with length in [lo, hi]."""
        if hi < lo or hi < 0:
            return 0
        lo = max(lo, 0)
        blocks = self._blocks
        high = self._accept_dot(blocks.cum_vec(hi + 1, idx))
        low = self._accept_dot(blocks.cum_vec(lo, idx)) if lo else 0
        diff = high - low
        return diff % self.modulus if self.mode == "mod" else diff

    def count(self, lo, hi):
        """Number of accepted words with length in ``[lo, hi]``."""
        self._refresh()
        return self._count_range(self._initial_idx, lo, hi)

    # -- k-th word and ranking (exact mode only) ------------------------------
    def _subtree_count(self, idx, depth, lo, hi):
        return self._count_range(idx, lo - depth, hi - depth)

    def kth(self, k, lo, hi):
        """The k-th (0-based) accepted word of length in [lo, hi],
        in lexicographic order over the sorted alphabet."""
        self._require_exact("kth")
        self._refresh()
        total = self.count(lo, hi)
        if not 0 <= k < total:
            raise IndexError(f"k={k} out of range, only {total} words")
        word = []
        idx = self._initial_idx
        while True:
            if len(word) >= lo and idx in self._accept:
                if k == 0:
                    return "".join(word)
                k -= 1
            for sym in self.product.alphabet:
                nxt = self._trans[idx].get(sym)
                if nxt is None:
                    continue
                cnt = self._subtree_count(nxt, len(word) + 1, lo, hi)
                if k < cnt:
                    word.append(sym)
                    idx = nxt
                    break
                k -= cnt
            else:  # pragma: no cover - unreachable if counts are consistent
                raise AssertionError("counting/selection mismatch")

    def rank(self, word, lo, hi):
        """0-based lexicographic rank of ``word`` among accepted words
        with length in [lo, hi].  Raises if the word is not counted."""
        self._require_exact("rank")
        self._refresh()
        idx = self._initial_idx
        result = 0
        for depth, sym in enumerate(word):
            if depth >= lo and idx in self._accept:
                result += 1  # a valid proper prefix sorts before `word`
            for alt in self.product.alphabet:
                if alt == sym:
                    break
                nxt = self._trans[idx].get(alt)
                if nxt is not None:
                    result += self._subtree_count(nxt, depth + 1, lo, hi)
            idx = self._trans[idx].get(sym)
            if idx is None:
                raise ValueError(f"{word!r} is not accepted by the constraints")
        if len(word) < lo or len(word) > hi or idx not in self._accept:
            raise ValueError(f"{word!r} is not counted in [{lo}, {hi}]")
        return result
