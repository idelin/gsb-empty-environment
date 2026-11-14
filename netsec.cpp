#include "netsec.hpp"

// Translation unit umbrella: the component implementation is split across
// wire.cpp, certificate.cpp, trust_store.cpp and policy.cpp. Keeping this
// file lets downstreams link the original `netsec` target unchanged.

namespace netsec {

const int kVersion = 1;

}  // namespace netsec
