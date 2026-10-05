#pragma once

// binrw: schema-driven binary reader/writer.
//  - fixed-size little-endian header
//  - payload fields with per-field byte order and bit width (1..64),
//    fields may cross byte boundaries
//  - no unaligned access, no host-endianness dependence
//  - unknown bits are preserved and re-encoded verbatim
//  - checked length arithmetic (overflow raises binrw::Error)

#include "binrw/bitstream.hpp"
#include "binrw/header.hpp"
#include "binrw/message.hpp"
#include "binrw/schema.hpp"
