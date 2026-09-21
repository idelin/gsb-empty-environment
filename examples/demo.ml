// minilang feature tour
let total = 0;
let i = 1;
while (i <= 10) {
  if (i % 2 == 0) {
    i = i + 1;
    continue;            // skip even numbers
  }
  total = total + i;
  i = i + 1;
}
print("sum of odd 1..10 =", total);   // 25

fn fib(n) {
  if (n < 2) { return n; }
  return fib(n - 1) + fib(n - 2);
}
print("fib(10) =", fib(10));          // 55

// try / catch / finally
fn parse_flag(s) {
  try {
    if (s == "") { throw "empty flag"; }
    return s;
  } catch (e) {
    print("caught:", e);
    return "default";
  } finally {
    print("cleanup: parse_flag done");
  }
}
print(parse_flag(""));                // caught + cleanup + "default"
print(parse_flag("on"));              // cleanup + "on"
