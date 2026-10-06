# Thread-affine resources on steppable executors

- `src/manual_executor.hpp` — single-threaded, manually stepped executor.
  `post()` rejects tasks after shutdown; `shutdown()` seals the queue and
  drains every remaining task before returning.
- `src/thread_affine.hpp` — `ThreadAffine<T>` factory + move-only `Handle`.
  Resources may only be destroyed on the creating thread: releasing a handle
  (from any thread) posts the cleanup back to the owning executor. Cleanup
  runs exactly once. `create()` throws after the executor shuts down; a
  handle released after shutdown falls back to inline destruction.
- `src/tests.cpp` — checks with two steppable executors:
  1. destruction happens on the owning (creating) thread;
  2. cleanup runs exactly once across cross-thread handle moves;
  3. shutdown drains pending cleanups, seals creation, and races safely
     with concurrent releases (200 iterations, TSan-clean).

## Build & run

```sh
clang++ -std=c++17 -Wall -Wextra -pthread src/tests.cpp -o tests && ./tests
# or
cmake -B build && cmake --build build && ctest --test-dir build
# race check
clang++ -std=c++17 -pthread -fsanitize=thread -g src/tests.cpp -o tests_tsan && ./tests_tsan
```
