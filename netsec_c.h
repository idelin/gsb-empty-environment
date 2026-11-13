#pragma once

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef struct netsec_instance netsec_instance;

typedef enum netsec_result_code {
  NETSEC_OK = 0,
  NETSEC_DECODE_ERROR = 1,
  NETSEC_INSTANTIATION_ERROR = 2,
  NETSEC_INVALID_ARGUMENT = 3,
} netsec_result_code;

netsec_result_code netsec_instantiate(const uint8_t* wasm, size_t size,
                                      netsec_instance** out_instance,
                                      char* error_buffer,
                                      size_t error_buffer_size);

netsec_result_code netsec_decode_and_instantiate(
    const uint8_t* wasm, size_t size, netsec_instance** out_instance,
    char* error_buffer, size_t error_buffer_size);

void netsec_instance_destroy(netsec_instance* instance);

size_t netsec_instance_memory_count(const netsec_instance* instance);

size_t netsec_instance_memory_size(const netsec_instance* instance,
                                   uint32_t memory_index);

const uint8_t* netsec_instance_memory_data(const netsec_instance* instance,
                                           uint32_t memory_index,
                                           size_t* out_size);

int netsec_active_data_segment_in_bounds(uint64_t memory_size, uint64_t offset,
                                         uint64_t length);

#ifdef __cplusplus
}
#endif
