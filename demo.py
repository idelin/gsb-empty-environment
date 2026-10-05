"""Runnable tour of strided_tensor: views, overlap analysis, safe in-place ops."""

from strided_tensor import (
    BorrowError,
    BoundsError,
    NonContiguousError,
    OverlapError,
    Storage,
    Tensor,
)

show = lambda name, t: print(f"  {name:<26} {t}")

print("== 1. strided views: transpose / negative stride / slices ==")
m = Tensor.from_flat(list(range(6)), (2, 3))
show("m", m)
show("m.T (shared storage)", m.T)
show("m[::-1, ::-1]", m[::-1, ::-1])
show("arange(10)[1:9:3]", Tensor.arange(10)[1:9:3])
m.T.set((2, 1), 99)
print(f"  write through m.T -> m[1,2] = {m.get(1, 2)}")

print("\n== 2. zero-stride broadcast / empty dims ==")
show("broadcast (1,3)->(2,3)", Tensor.from_flat([7, 8, 9], (1, 3)).broadcast_to((2, 3)))
b = Tensor.arange(3)[0:1].broadcast_to((4,))
print(f"  zero-stride view internal overlap: {b.internal_overlap().value}")
e = Tensor.from_flat([], (0, 3))
print(f"  empty (0,3): numel={e.numel}, internal overlap={e.internal_overlap().value}")

print("\n== 3. overlap analysis (not just bounding byte ranges) ==")
t = Tensor.arange(8)
evens, odds = t[0:8:2], t[1:8:2]
print(f"  evens {evens.absolute_bounds()} vs odds {odds.absolute_bounds()}: "
      f"intervals intersect, overlap = {t[0:8:2].overlaps(odds).value}")
print(f"  t[0:5] vs t[3:8]: overlap = {t[0:5].overlaps(t[3:8]).value}")
alias = Tensor(Storage(range(8)), (2, 2), (2, 2))
print(f"  strides (2,2) shape (2,2): internal overlap = {alias.internal_overlap().value}")
tricky = Tensor(Storage(range(8)), (2, 3), (3, 2))
print(f"  strides (3,2) shape (2,3): internal overlap = {tricky.internal_overlap().value} (exact solver)")

print("\n== 4. snapshot copy vs forbidden in-place add ==")
t = Tensor.arange(6)
t[1:5].copy_(t[0:4])  # overlapping copy -> snapshot semantics
print(f"  t[1:5].copy_(t[0:4])  -> {t.to_list()}  (no smearing)")
t4 = Tensor.arange(4)
try:
    t4.add_(t4[::-1])
except OverlapError as exc:
    print(f"  add_ with alias rejected: {exc}")
ok = Tensor.arange(4)
ok[0:2].add_(ok[2:4])
print(f"  disjoint add_ allowed   -> {ok.to_list()}")

print("\n== 5. borrow-time write protection ==")
t = Tensor.arange(4)
with t.borrowed() as ro:
    print(f"  read during borrow: ro[2] = {ro.get(2)}")
    try:
        t.set(0, 9)
    except BorrowError as exc:
        print(f"  write during borrow rejected: {exc}")
t.set(0, 9)
print(f"  after borrow ends: t = {t.to_list()}")

print("\n== 6. reshape rules and bounds beyond numel ==")
try:
    Tensor.from_flat(list(range(6)), (2, 3)).T.reshape(6)
except NonContiguousError as exc:
    print(f"  transposed reshape rejected: {exc}")
print(f"  contiguous copy reshapes: "
      f"{Tensor.from_flat(list(range(6)), (2, 3)).T.contiguous().reshape(6).to_list()}")
try:
    Tensor(Storage(range(4)), (2,), (100,))  # numel 2 < 4, yet out of bounds
except BoundsError as exc:
    print(f"  stride-100 view rejected: {exc}")
