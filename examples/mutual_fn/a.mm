// a imports a function from b while b imports a function from a.
// Function declarations are hoisted, so the cycle links fine.
import { is_odd } from "b"

export fn is_even(n) {
  if n == 0 { return true }
  return is_odd(n - 1)
}

export const check = is_even(10)
print(check)
