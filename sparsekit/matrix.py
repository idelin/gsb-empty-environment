"""Sparse matrices in CSR/CSC layouts with canonical COO conversion.

Duplicate coordinates are merged by exact accumulation. In 'eliminate' mode
entries whose accumulated value is zero are dropped; in 'keep' mode explicit
zeros are stored and remain distinguishable from missing coordinates.
"""
from __future__ import annotations

from .dense import DenseMatrix
from .pages import DEFAULT_PAGE_SIZE, PagedBuffer, STATS

CSR = "csr"
CSC = "csc"
ELIMINATE = "eliminate"
KEEP = "keep"


class SparseMatrix:
    def __init__(
        self,
        shape,
        layout,
        indptr,
        indices,
        data,
        mode=ELIMINATE,
        version=0,
        page_size=DEFAULT_PAGE_SIZE,
        is_view=False,
        bound_version=None,
    ):
        self._shape = tuple(shape)
        self._layout = layout
        self._indptr = indptr
        self._indices = indices
        self._data = data
        self._mode = mode
        self._version = version
        self._page_size = page_size
        self._is_view = is_view
        self._bound_version = version if bound_version is None else bound_version

    @property
    def shape(self):
        return self._shape

    @property
    def layout(self):
        return self._layout

    @property
    def mode(self):
        return self._mode

    @property
    def nnz(self):
        return len(self._data)

    @property
    def version(self):
        return self._version

    @property
    def page_size(self):
        return self._page_size

    @property
    def is_view(self):
        return self._is_view

    @property
    def bound_version(self):
        return self._bound_version

    def _major_dims(self):
        if self._layout == CSR:
            return self._shape[0], self._shape[1]
        return self._shape[1], self._shape[0]

    def _check_writable(self):
        if self._is_view:
            raise TypeError(
                "matrix view is read-only; call materialize() to edit a copy"
            )

    @classmethod
    def from_coo(
        cls,
        shape,
        rows,
        cols,
        data,
        mode=ELIMINATE,
        layout=CSR,
        page_size=DEFAULT_PAGE_SIZE,
    ):
        nrows, ncols = shape
        rows = list(rows)
        cols = list(cols)
        data = list(data)
        if not (len(rows) == len(cols) == len(data)):
            raise ValueError("COO arrays must have equal length")
        if mode not in (ELIMINATE, KEEP):
            raise ValueError("mode must be 'eliminate' or 'keep'")
        if layout not in (CSR, CSC):
            raise ValueError("layout must be 'csr' or 'csc'")
        merged = {}
        for r, c, value in zip(rows, cols, data):
            if not (0 <= r < nrows and 0 <= c < ncols):
                raise IndexError(f"coordinate ({r}, {c}) out of shape {shape}")
            key = (r, c)
            merged[key] = merged[key] + value if key in merged else value
        items = sorted(merged.items())
        if mode == ELIMINATE:
            items = [(key, value) for key, value in items if value != 0]
        return cls._from_sorted_items(
            shape, items, mode, layout, page_size, version=0
        )

    @classmethod
    def _from_sorted_items(
        cls, shape, items, mode, layout, page_size, version=0
    ):
        nrows, ncols = shape
        major_dim = nrows if layout == CSR else ncols
        order = (lambda kv: (kv[0][0], kv[0][1])) if layout == CSR else (
            lambda kv: (kv[0][1], kv[0][0])
        )
        items = sorted(items, key=order)
        indptr = [0] * (major_dim + 1)
        indices = []
        values = []
        for (r, c), value in items:
            if layout == CSR:
                major, minor = r, c
            else:
                major, minor = c, r
            indptr[major + 1] += 1
            indices.append(minor)
            values.append(value)
        for k in range(major_dim):
            indptr[k + 1] += indptr[k]
        return cls._from_arrays(
            shape, layout, indptr, indices, values, mode, page_size, version
        )

    @classmethod
    def _from_arrays(
        cls,
        shape,
        layout,
        indptr,
        indices,
        data,
        mode=ELIMINATE,
        page_size=DEFAULT_PAGE_SIZE,
        version=0,
    ):
        return cls(
            shape,
            layout,
            PagedBuffer.from_values(indptr, page_size),
            PagedBuffer.from_values(indices, page_size),
            PagedBuffer.from_values(data, page_size),
            mode,
            version,
            page_size,
        )

    def arrays(self):
        return self._indptr.to_list(), self._indices.to_list(), self._data.to_list()

    def to_coo(self):
        rows, cols, values = [], [], []
        for r, c, value in self.iter_entries():
            rows.append(r)
            cols.append(c)
            values.append(value)
        return rows, cols, values

    def to_layout(self, layout):
        if layout not in (CSR, CSC):
            raise ValueError("layout must be 'csr' or 'csc'")
        if layout == self._layout:
            return self
        items = [((r, c), value) for r, c, value in self.iter_entries()]
        return SparseMatrix._from_sorted_items(
            self._shape,
            items,
            self._mode,
            layout,
            self._page_size,
            version=self._version,
        )

    def iter_entries(self):
        major_dim, _ = self._major_dims()
        for major in range(major_dim):
            for p in range(self._indptr[major], self._indptr[major + 1]):
                minor = self._indices[p]
                value = self._data[p]
                if self._layout == CSR:
                    yield major, minor, value
                else:
                    yield minor, major, value

    def to_dense(self) -> DenseMatrix:
        dense = DenseMatrix.zeros(*self._shape)
        for r, c, value in self.iter_entries():
            dense.set(r, c, value)
        return dense

    def _locate(self, i, j):
        nrows, ncols = self._shape
        if i < 0:
            i += nrows
        if j < 0:
            j += ncols
        if not (0 <= i < nrows and 0 <= j < ncols):
            raise IndexError(f"coordinate ({i}, {j}) out of shape {self._shape}")
        if self._layout == CSR:
            major, minor = i, j
        else:
            major, minor = j, i
        lo = self._indptr[major]
        end = self._indptr[major + 1]
        hi = end
        while lo < hi:
            mid = (lo + hi) // 2
            if self._indices[mid] < minor:
                lo = mid + 1
            else:
                hi = mid
        found = lo < end and self._indices[lo] == minor
        return found, lo, major

    def get(self, i, j):
        found, pos, _ = self._locate(i, j)
        return self._data[pos] if found else 0

    def explicit_at(self, i, j):
        found, pos, _ = self._locate(i, j)
        return (True, self._data[pos]) if found else (False, 0)

    def __getitem__(self, key):
        if not isinstance(key, tuple) or len(key) != 2:
            raise TypeError("index as matrix[i, j] or matrix[r0:r1, c0:c1]")
        rsel, csel = key
        if isinstance(rsel, slice) or isinstance(csel, slice) or rsel is Ellipsis:
            r0, r1 = SliceView.resolve(rsel, self._shape[0])
            c0, c1 = SliceView.resolve(csel, self._shape[1])
            return SliceView(self, r0, r1, c0, c1)
        return self.get(rsel, csel)

    @property
    def T(self):
        swapped = CSC if self._layout == CSR else CSR
        STATS.views_created += 1
        return SparseMatrix(
            (self._shape[1], self._shape[0]),
            swapped,
            self._indptr,
            self._indices,
            self._data,
            self._mode,
            self._version,
            self._page_size,
            is_view=True,
            bound_version=self._version,
        )

    def _spawn(self, *, shape=None, indptr=None, indices=None, data=None):
        return SparseMatrix(
            self._shape if shape is None else shape,
            self._layout,
            self._indptr if indptr is None else indptr,
            self._indices if indices is None else indices,
            self._data if data is None else data,
            self._mode,
            self._version + 1,
            self._page_size,
        )

    def _shifted_indptr(self, major, delta):
        ptr = self._indptr.to_list()
        for k in range(major + 1, len(ptr)):
            ptr[k] += delta
        return PagedBuffer.from_values(ptr, self._page_size)

    def set(self, i, j, value):
        self._check_writable()
        found, pos, major = self._locate(i, j)
        if found:
            if value == 0 and self._mode == ELIMINATE:
                new_indices = self._indices.splice(pos, 1, [])
                new_data = self._data.splice(pos, 1, [])
                new_indptr = self._shifted_indptr(major, -1)
                return self._spawn(
                    indptr=new_indptr, indices=new_indices, data=new_data
                )
            return self._spawn(data=self._data.updated(pos, value))
        if value == 0 and self._mode == ELIMINATE:
            return self
        new_indices = self._indices.splice(pos, 0, [self._minor_of(i, j)])
        new_data = self._data.splice(pos, 0, [value])
        new_indptr = self._shifted_indptr(major, 1)
        return self._spawn(
            indptr=new_indptr, indices=new_indices, data=new_data
        )

    def _minor_of(self, i, j):
        return j if self._layout == CSR else i

    def _insert_major(self, k, n):
        boundary = self._indptr[k]
        return (
            self._indptr.splice(k, 0, [boundary] * n),
            self._indices,
            self._data,
        )

    def _delete_major(self, k, n):
        ptr = self._indptr.to_list()
        removed = ptr[k + n] - ptr[k]
        new_ptr = ptr[:k] + [p - removed for p in ptr[k + n:]]
        return (
            PagedBuffer.from_values(new_ptr, self._page_size),
            self._indices.splice(ptr[k], removed, []),
            self._data.splice(ptr[k], removed, []),
        )

    def _insert_minor(self, k, n):
        mapped = [v + n if v >= k else v for v in self._indices.to_list()]
        return (
            self._indptr,
            PagedBuffer.from_values(mapped, self._page_size),
            self._data,
        )

    def _delete_minor(self, k, n):
        major_dim, _ = self._major_dims()
        new_indptr = [0]
        new_indices = []
        new_data = []
        for major in range(major_dim):
            for p in range(self._indptr[major], self._indptr[major + 1]):
                v = self._indices[p]
                if v < k:
                    new_indices.append(v)
                    new_data.append(self._data[p])
                elif v >= k + n:
                    new_indices.append(v - n)
                    new_data.append(self._data[p])
            new_indptr.append(len(new_indices))
        return (
            PagedBuffer.from_values(new_indptr, self._page_size),
            PagedBuffer.from_values(new_indices, self._page_size),
            PagedBuffer.from_values(new_data, self._page_size),
        )

    def _check_insert(self, axis, k):
        dim = self._shape[axis]
        if not 0 <= k <= dim:
            raise IndexError(f"insert position {k} out of range 0..{dim}")

    def _check_delete(self, axis, k, n):
        dim = self._shape[axis]
        if n < 1 or not 0 <= k or k + n > dim:
            raise IndexError(f"cannot delete {n} entries at {k} on axis of {dim}")

    def insert_rows(self, k, n=1):
        self._check_writable()
        self._check_insert(0, k)
        if n < 1:
            raise ValueError("insert count must be positive")
        if self._layout == CSR:
            indptr, indices, data = self._insert_major(k, n)
        else:
            indptr, indices, data = self._insert_minor(k, n)
        return self._spawn(
            shape=(self._shape[0] + n, self._shape[1]),
            indptr=indptr,
            indices=indices,
            data=data,
        )

    def delete_rows(self, k, n=1):
        self._check_writable()
        self._check_delete(0, k, n)
        if self._layout == CSR:
            indptr, indices, data = self._delete_major(k, n)
        else:
            indptr, indices, data = self._delete_minor(k, n)
        return self._spawn(
            shape=(self._shape[0] - n, self._shape[1]),
            indptr=indptr,
            indices=indices,
            data=data,
        )

    def insert_cols(self, k, n=1):
        self._check_writable()
        self._check_insert(1, k)
        if n < 1:
            raise ValueError("insert count must be positive")
        if self._layout == CSC:
            indptr, indices, data = self._insert_major(k, n)
        else:
            indptr, indices, data = self._insert_minor(k, n)
        return self._spawn(
            shape=(self._shape[0], self._shape[1] + n),
            indptr=indptr,
            indices=indices,
            data=data,
        )

    def delete_cols(self, k, n=1):
        self._check_writable()
        self._check_delete(1, k, n)
        if self._layout == CSC:
            indptr, indices, data = self._delete_major(k, n)
        else:
            indptr, indices, data = self._delete_minor(k, n)
        return self._spawn(
            shape=(self._shape[0], self._shape[1] - n),
            indptr=indptr,
            indices=indices,
            data=data,
        )

    def transaction(self) -> "Transaction":
        self._check_writable()
        return Transaction(self)

    def __repr__(self):
        return (
            f"SparseMatrix(shape={self._shape}, layout={self._layout!r}, "
            f"mode={self._mode!r}, nnz={self.nnz}, version={self._version})"
        )


class SliceView:
    """Zero-copy read-only rectangular window over another matrix or view."""

    def __init__(self, parent, row0, row1, col0, col1):
        if not (0 <= row0 <= row1 <= parent.shape[0]):
            raise IndexError("row window out of range")
        if not (0 <= col0 <= col1 <= parent.shape[1]):
            raise IndexError("column window out of range")
        self._parent = parent
        self._row0 = row0
        self._row1 = row1
        self._col0 = col0
        self._col1 = col1
        self._bound_version = parent.version
        STATS.views_created += 1

    @staticmethod
    def resolve(sel, dim):
        if sel is None or sel is Ellipsis:
            return 0, dim
        if isinstance(sel, slice):
            start, stop, step = sel.indices(dim)
            if step != 1:
                raise ValueError("only contiguous slices are supported")
            return start, stop
        if sel < 0:
            sel += dim
        if not 0 <= sel < dim:
            raise IndexError(f"index {sel} out of range {dim}")
        return sel, sel + 1

    @property
    def shape(self):
        return (self._row1 - self._row0, self._col1 - self._col0)

    @property
    def nnz(self):
        return sum(1 for _ in self.iter_entries())

    @property
    def version(self):
        return self._bound_version

    @property
    def bound_version(self):
        return self._bound_version

    @property
    def is_view(self):
        return True

    @property
    def mode(self):
        return self._parent.mode

    def get(self, i, j):
        nrows, ncols = self.shape
        if i < 0:
            i += nrows
        if j < 0:
            j += ncols
        if not (0 <= i < nrows and 0 <= j < ncols):
            raise IndexError(f"coordinate ({i}, {j}) out of shape {self.shape}")
        return self._parent.get(self._row0 + i, self._col0 + j)

    def __getitem__(self, key):
        if not isinstance(key, tuple) or len(key) != 2:
            raise TypeError("index as view[i, j] or view[r0:r1, c0:c1]")
        rsel, csel = key
        if isinstance(rsel, slice) or isinstance(csel, slice) or rsel is Ellipsis:
            r0, r1 = self.resolve(rsel, self.shape[0])
            c0, c1 = self.resolve(csel, self.shape[1])
            return SliceView(
                self._parent,
                self._row0 + r0,
                self._row0 + r1,
                self._col0 + c0,
                self._col0 + c1,
            )
        return self.get(rsel, csel)

    def iter_entries(self):
        for r, c, value in self._parent.iter_entries():
            if self._row0 <= r < self._row1 and self._col0 <= c < self._col1:
                yield r - self._row0, c - self._col0, value

    def to_dense(self) -> DenseMatrix:
        dense = DenseMatrix.zeros(*self.shape)
        for r, c, value in self.iter_entries():
            dense.set(r, c, value)
        return dense

    def materialize(self, layout=CSR, page_size=DEFAULT_PAGE_SIZE) -> SparseMatrix:
        rows, cols, values = [], [], []
        for r, c, value in self.iter_entries():
            rows.append(r)
            cols.append(c)
            values.append(value)
        return SparseMatrix.from_coo(
            self.shape,
            rows,
            cols,
            values,
            mode=self._parent.mode,
            layout=layout,
            page_size=page_size,
        )

    @property
    def T(self):
        return SliceView(
            self._parent.T,
            self._col0,
            self._col1,
            self._row0,
            self._row1,
        )

    def _readonly(self, *args, **kwargs):
        raise TypeError(
            "slice view is read-only; call materialize() to edit a copy"
        )

    set = _readonly
    insert_rows = _readonly
    delete_rows = _readonly
    insert_cols = _readonly
    delete_cols = _readonly
    transaction = _readonly

    def __repr__(self):
        return (
            f"SliceView(rows={self._row0}:{self._row1}, "
            f"cols={self._col0}:{self._col1}, shape={self.shape}, "
            f"bound_version={self._bound_version})"
        )


class Transaction:
    """Batches structural and point updates; commits atomically or not at all."""

    _OPERATIONS = (
        "set",
        "insert_rows",
        "delete_rows",
        "insert_cols",
        "delete_cols",
    )

    def __init__(self, matrix):
        self._base = matrix
        self._ops = []
        self._closed = False
        self.result = None

    def _record(self, name, *args):
        if self._closed:
            raise RuntimeError("transaction already committed")
        self._ops.append((name, args))
        return self

    def set(self, i, j, value):
        return self._record("set", i, j, value)

    def insert_rows(self, k, n=1):
        return self._record("insert_rows", k, n)

    def delete_rows(self, k, n=1):
        return self._record("delete_rows", k, n)

    def insert_cols(self, k, n=1):
        return self._record("insert_cols", k, n)

    def delete_cols(self, k, n=1):
        return self._record("delete_cols", k, n)

    def commit(self) -> SparseMatrix:
        if self._closed:
            raise RuntimeError("transaction already committed")
        current = self._base
        for name, args in self._ops:
            current = getattr(current, name)(*args)
        self._closed = True
        self.result = current
        return current

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc, tb):
        if exc_type is None and not self._closed:
            self.commit()
        return False
