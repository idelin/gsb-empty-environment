// MiniLang feature tour.

// --- variables, arithmetic, strings ------------------------------------
let name = "世界";            // UTF-8 identifiers and strings are fine
let 问候 = "你好";
print(问候 + ", " + name + "!");

let x = 7;
let y = 3;
print("x + y =", x + y);
print("x * y =", x * y);
print("x % y =", x % y);
print("x > y && y > 0:", x > y && y > 0);

// --- functions, closures, recursion ------------------------------------
fn fib(n) {
    if (n < 2) { return n; }
    return fib(n - 1) + fib(n - 2);
}
print("fib(10) =", fib(10));

fn make_counter() {
    let count = 0;
    fn bump() {
        count = count + 1;
        return count;
    }
    return bump;
}
let counter = make_counter();
counter();
counter();
print("counter =", counter());

// --- loops and loop control --------------------------------------------
let total = 0;
for (let i = 1; i <= 10; i = i + 1) {
    if (i == 4) { continue; }   // skip 4
    if (i == 8) { break; }      // stop at 8
    total = total + i;
}
print("total =", total);        // 1+2+3+5+6+7 = 24

let n = 5;
while (n > 0) {
    n = n - 1;
}
print("n =", n);

// --- throw / try / catch / finally -------------------------------------
fn checked_div(a, b) {
    if (b == 0) {
        throw "cannot divide by zero";
    }
    return a / b;
}

try {
    print("10 / 2 =", checked_div(10, 2));
    print("10 / 0 =", checked_div(10, 0));
    print("unreachable");
} catch (err) {
    print("caught:", err);
} finally {
    print("finally: always runs");
}

// Runtime faults are catchable too.
try {
    let z = 1 / 0;
} catch (err) {
    print("caught runtime fault:", err);
}
