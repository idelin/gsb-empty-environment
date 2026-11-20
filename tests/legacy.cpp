#include "measurebank/v1.hpp"
int main() {
    const std::vector<std::uint8_t> persisted{0x4d,1,1,0xff,0xff,0,1,1,2,3,4};
    auto a=measurebank::decode_v1(persisted);
    if (!a || a->size()!=1 || a->front().first!=65535 || a->front().value!=0x01020304u) return 1;
    auto bad=persisted; bad[6]=2; if(measurebank::decode_v1(bad)) return 2;
    auto trailer=persisted; trailer.push_back(0); return measurebank::decode_v1(trailer).has_value();
}
