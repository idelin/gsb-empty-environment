// Control-transfer priority: throw > return > break > continue > normal.
// On a tie, the newer transfer (from finally) wins.

// 1. return in finally overrides a pending break.
fn f1() {
  while (true) {
    try { break; } finally { return "return beats break"; }
  }
  return "unreachable";
}
print(f1());

// 2. On equal priority the finally transfer wins.
fn f2() {
  try { return "from try"; } finally { return "from finally"; }
}
print(f2());

// 3. A lower-priority transfer in finally does NOT override a pending throw.
fn f3() {
  while (true) {
    try { throw "boom"; } finally { break; }   // break loses to throw
  }
  return "unreachable";
}
try { f3(); } catch (e) { print("f3 escaped with:", e); }

// 4. A throw in finally overrides the original error but keeps it as cause.
try {
  try { throw "original"; } finally { throw "cleanup failed"; }
} catch (e) {
  print("outer catch:", e);   // cleanup failed (cause chain on stderr if uncaught)
}

// 5. return snapshots its value before finally mutates outer state.
let x = 1;
fn f5() {
  try { return x; } finally { x = 99; }
}
print("f5() =", f5(), "but x =", x);  // f5() = 1 but x = 99
