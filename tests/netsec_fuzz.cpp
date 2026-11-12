#include "netsec.hpp"

#include <random>
#include <vector>

using namespace netsec;

int main() {
    std::mt19937 rng(12345);
    SubmissionValidator v;
    v.addAllowedRegister(0x10);
    v.addAddressRange(0x10000000ULL, 0x100000);

    for (int iter = 0; iter < 20000; ++iter) {
        std::vector<uint8_t> buf(rng() % 130);
        for (auto& byte : buf) {
            byte = static_cast<uint8_t>(rng());
        }
        CommandBufferView cb{buf.data(), 0x10000000ULL, buf.size()};
        v.validate(cb);  // 只要求不崩溃、不越界读
    }
    return 0;
}
