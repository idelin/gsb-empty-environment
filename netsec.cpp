#include "netsec.hpp"

#if __cplusplus < 202002L
#error "netsec surveillance component requires C++20"
#endif

static_assert(netsec::MessageType::AdsbPosition == netsec::MessageType::ADS_B_POSITION);
static_assert(netsec::MessageFlag::Replay == netsec::MessageFlag::REPLAY);

extern "C" int netsec_component_abi() noexcept { return 1; }
