// A concrete ABI used by the simulator and tests.  It exercises every
// supported shape: scalars, an inline byte array, a nested pointer, a pointer
// array with an attacker-controlled count and a variable-length byte tail.
#ifndef CCM_DEMO_ABI_HPP
#define CCM_DEMO_ABI_HPP

#include <cstdlib>

#include "ccm/schema.hpp"

namespace ccm {
namespace demo {

inline constexpr TypeId kTypeRecord = 1;
inline constexpr TypeId kTypeEnvelope = 2;

inline constexpr len_t kRecordSize = 32;
inline constexpr len_t kEnvelopeSize = 48;

// Record layout (32 bytes):
//   0: u32 id
//   4: u32 payload_len
//   8: u8[16] tag
//  24: u64 next (nested pointer, may be null)
inline constexpr len_t kRecordIdOff = 0;
inline constexpr len_t kRecordLenOff = 4;
inline constexpr len_t kRecordTagOff = 8;
inline constexpr len_t kRecordTagSize = 16;
inline constexpr len_t kRecordNextOff = 24;

// Envelope layout (48 byte fixed header + u32 tail_len driven byte tail):
//   0: u32 magic
//   4: u32 version
//   8: u32 record_count
//  12: u32 tail_len
//  16: u64 records (pointer to record_count Record entries)
//  24: u64 secret_out (host-provided output pointer)
//  32: u64 record_single (nested pointer to one Record)
//  40: u64 limit (scalar business bound)
inline constexpr len_t kEnvMagicOff = 0;
inline constexpr len_t kEnvVersionOff = 4;
inline constexpr len_t kEnvCountOff = 8;
inline constexpr len_t kEnvTailLenOff = 12;
inline constexpr len_t kEnvRecordsOff = 16;
inline constexpr len_t kEnvOutOff = 24;
inline constexpr len_t kEnvSingleOff = 32;
inline constexpr len_t kEnvLimitOff = 40;

inline constexpr u32_t kExpectedMagic = 0x43434D31U;  // "CCM1"
inline constexpr u32_t kExpectedVersion = 1U;
inline constexpr len_t kMaxRecordCount = 16;
inline constexpr len_t kMaxTailLen = 128;
inline constexpr u64_t kMaxLimit = 4096;

inline Schema build_schema() {
    TypeDesc record;
    record.id = kTypeRecord;
    record.fixed_size = kRecordSize;
    record.fields = {
        FieldDesc::make_scalar("id", kRecordIdOff, ScalarWidth::U32),
        FieldDesc::make_scalar("payload_len", kRecordLenOff,
                               ScalarWidth::U32),
        FieldDesc::make_inline_bytes("tag", kRecordTagOff, kRecordTagSize),
        FieldDesc::make_nested_ptr("next", kRecordNextOff, kTypeRecord),
    };

    TypeDesc envelope;
    envelope.id = kTypeEnvelope;
    envelope.fixed_size = kEnvelopeSize;
    envelope.fields = {
        FieldDesc::make_scalar("magic", kEnvMagicOff, ScalarWidth::U32),
        FieldDesc::make_scalar("version", kEnvVersionOff, ScalarWidth::U32),
        FieldDesc::make_scalar("record_count", kEnvCountOff,
                               ScalarWidth::U32),
        FieldDesc::make_scalar("tail_len", kEnvTailLenOff,
                               ScalarWidth::U32),
        FieldDesc::make_ptr_array("records", kEnvRecordsOff, kTypeRecord,
                                  "record_count"),
        FieldDesc::make_scalar("secret_out", kEnvOutOff, ScalarWidth::U64),
        FieldDesc::make_nested_ptr("record_single", kEnvSingleOff,
                                   kTypeRecord),
        FieldDesc::make_scalar("limit", kEnvLimitOff, ScalarWidth::U64),
        FieldDesc::make_tail_bytes("tail", kEnvelopeSize, "tail_len"),
    };

    Result<Schema> result = Schema::create({record, envelope});
    if (!result.ok()) {
        // The built-in demo schema is itself a compile-time-ish invariant.
        std::abort();
    }
    return result.value();
}

}  // namespace demo
}  // namespace ccm

#endif  // CCM_DEMO_ABI_HPP
