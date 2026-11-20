#include "linkstate/v1.hpp"
int main() {
    const linkstate::Wire archived{'L','K',1,3,4,3,2,1,9,0,0,0,255,0,0,0};
    auto cp=linkstate::decode_v1(archived);
    if (!cp || cp->boot!=0x01020304u || cp->peer!=9 || cp->next!=255 || linkstate::encode_v1(*cp)!=archived) return 1;
    auto invalid=archived; invalid[3]=0;
    return linkstate::decode_v1(invalid).has_value();
}
