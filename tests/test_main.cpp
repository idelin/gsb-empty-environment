// SPDX-License-Identifier: MIT
// Deterministic boundary tests for the DTLS 1.3 record-layer library.
// Test registration happens via static registrars in the linked units.

#include "test_framework.hpp"

int main() {
    return dtls13_test::runAll();
}
