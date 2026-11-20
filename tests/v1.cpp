#include "packetlease/span.hpp"
int main(){
 packetlease::SpanBytes b{0,2,0,9,3,0,0,0,254,255,255,255,2,0,0,0};
 auto s=packetlease::decode_span_v1(b);
 if (!s||s->pool!=2||s->slot!=9||s->generation!=3||s->offset!=0xfffffffeU||s->size!=2) return 1;
 b[12]=3;return packetlease::decode_span_v1(b).has_value();
}
