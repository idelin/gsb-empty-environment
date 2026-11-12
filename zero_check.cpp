// Standalone translation unit used to prove at the assembly level that the
// secure clear is not eliminated as a dead store. Built with -O3 -S and
// inspected by the Makefile for the platform guaranteed-clear primitive
// (memset_s on Apple/Darwin).

#include "netsec.hpp"

void erase_secret(void* secret, unsigned long bytes) {
    netsec::detail::secure_zero_memory(secret, static_cast<std::size_t>(bytes));
}
