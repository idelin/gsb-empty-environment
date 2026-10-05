# new wire format: tag widened to 2 bytes
struct Packet {
  field len    size=4 align=4
  field tag    size=2 align=2
  pad 2                       # explicit reserved bytes
  field flags  size=1 align=1
  struct Inner nonpod {
    field id    size=4 align=4
    field kind  size=1 align=1
  }
  union Payload {
    field small size=4 align=4
    field blob  size=1 align=1 count=12
  }
  field crc    size=4 align=4
}
