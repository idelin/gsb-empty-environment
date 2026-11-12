// Negative control: a plain memset on a buffer that is never read again.
// Under -O3 the compiler is permitted to remove this dead store. The Makefile
// asserts that no call remains, contrasting it with the guaranteed clear in
// zero_check.cpp.

#include <cstdlib>
#include <cstring>

__attribute__((noinline))
void erase_secret_plain(void* secret, unsigned long bytes) {
    std::memset(secret, 0, static_cast<std::size_t>(bytes));
}

int main() {
    void* block = std::malloc(32);
    erase_secret_plain(block, 32);
    std::free(block);
}
