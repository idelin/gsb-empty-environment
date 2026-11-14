#pragma once

// Importable SSH certificate constraint component.
//
// Include this single header from an SSH server. The implementation lives in
// netsec.cpp and builds as the `netsec` CMake target (C++20).

#include "netsec/certificate.hpp"
#include "netsec/policy.hpp"
#include "netsec/trust_store.hpp"
#include "netsec/wire.hpp"

namespace netsec {

// Component version, defined in the umbrella translation unit.
extern const int kVersion;

}  // namespace netsec
