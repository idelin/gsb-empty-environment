#include "netsec.hpp"

// Kept out of line and fed through a volatile sink so the barrier instructions
// survive optimization and are visible in the generated assembly.
extern "C" NETSEC_NOINLINE void netsec_barrier_probe(volatile int* sink) {
    *sink = 1;
    netsec::compiler_barrier();

    *sink = 2;
    netsec::speculation_barrier();

    *sink = 3;
    netsec::memory_barrier();

    *sink = 4;
    netsec::enter_trust_boundary();

    *sink = 5;
    netsec::leave_trust_boundary();

    *sink = 6;
}
