#include "netsec.hpp"

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;

int failures = 0;

#define CHECK(condition)                                                     \
  do {                                                                       \
    if (!(condition)) {                                                      \
      std::fprintf(stderr, "FAIL %s:%d: %s\n", __FILE__, __LINE__,          \
                   #condition);                                              \
      ++failures;                                                            \
    }                                                                        \
  } while (false)

void append_u32(Bytes& output, std::uint32_t value) {
  do {
    std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7fu);
    value >>= 7;
    if (value != 0) {
      byte |= 0x80u;
    }
    output.push_back(byte);
  } while (value != 0);
}

void append_bytes(Bytes& output, const Bytes& bytes) {
  output.insert(output.end(), bytes.begin(), bytes.end());
}

void append_section(Bytes& module, std::uint8_t id, const Bytes& payload) {
  module.push_back(id);
  append_u32(module, static_cast<std::uint32_t>(payload.size()));
  append_bytes(module, payload);
}

void append_name(Bytes& payload, const char* name) {
  append_u32(payload, static_cast<std::uint32_t>(std::strlen(name)));
  for (const char* cursor = name; *cursor != '\0'; ++cursor) {
    payload.push_back(static_cast<std::uint8_t>(*cursor));
  }
}

void append_offset_expr(Bytes& payload, std::uint32_t offset) {
  payload.push_back(0x41);  // i32.const
  payload.push_back(static_cast<std::uint8_t>(offset & 0x7fu));
  if (offset > 0x7f) {
    payload.back() |= 0x80u;
    // Test offsets stay small, so one continuation byte is sufficient.
    payload.push_back(static_cast<std::uint8_t>((offset >> 7) & 0x7fu));
  }
  payload.push_back(0x0b);  // end
}

Bytes module_with_elements(std::uint32_t defined_functions,
                           std::uint32_t imported_functions,
                           std::uint32_t table_min,
                           const std::vector<std::uint32_t>& element_indices,
                           std::uint32_t offset = 0,
                           bool expression_form = false) {
  Bytes module = {0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00};

  Bytes types;
  append_u32(types, 1);
  types.push_back(0x60);  // () -> ()
  append_u32(types, 0);
  append_u32(types, 0);
  append_section(module, 1, types);

  if (imported_functions != 0) {
    Bytes imports;
    append_u32(imports, imported_functions);
    for (std::uint32_t index = 0; index < imported_functions; ++index) {
      append_name(imports, "env");
      append_name(imports, "f");
      imports.push_back(0x00);  // function
      append_u32(imports, 0);   // type index
    }
    append_section(module, 2, imports);
  }

  if (defined_functions != 0) {
    Bytes functions;
    append_u32(functions, defined_functions);
    for (std::uint32_t index = 0; index < defined_functions; ++index) {
      append_u32(functions, 0);
    }
    append_section(module, 3, functions);
  }

  Bytes tables;
  append_u32(tables, 1);
  tables.push_back(0x70);  // funcref
  append_u32(tables, 0);   // limits flags
  append_u32(tables, table_min);
  append_section(module, 4, tables);

  Bytes elements;
  append_u32(elements, 1);  // one segment
  append_u32(elements, expression_form ? 4 : 0);
  append_offset_expr(elements, offset);
  append_u32(elements,
             static_cast<std::uint32_t>(element_indices.size()));
  if (expression_form) {
    for (std::uint32_t function_index : element_indices) {
      elements.push_back(0xd2);  // ref.func
      append_u32(elements, function_index);
      elements.push_back(0x0b);  // end
    }
  } else {
    for (std::uint32_t function_index : element_indices) {
      append_u32(elements, function_index);
    }
  }
  append_section(module, 9, elements);

  Bytes code;
  append_u32(code, defined_functions);
  for (std::uint32_t index = 0; index < defined_functions; ++index) {
    append_u32(code, 2);  // body size
    code.push_back(0x00);  // no locals
    code.push_back(0x0b);  // end
  }
  append_section(module, 10, code);

  return module;
}

Bytes raw_element_segment(const std::vector<std::uint32_t>& indices,
                          std::uint32_t flags = 0) {
  Bytes segment;
  append_u32(segment, flags);
  if (flags == 0) {
    append_offset_expr(segment, 0);
    append_u32(segment, static_cast<std::uint32_t>(indices.size()));
    for (std::uint32_t index : indices) {
      append_u32(segment, index);
    }
  } else if (flags == 1) {
    segment.push_back(0x00);  // elemkind
    append_u32(segment, static_cast<std::uint32_t>(indices.size()));
    for (std::uint32_t index : indices) {
      append_u32(segment, index);
    }
  }
  return segment;
}

void test_valid_index_initializes_table() {
  Bytes module = module_with_elements(1, 0, 1, {0});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(module, instance, error) ==
        netsec::ErrorCode::ok);
  CHECK(instance.function_count() == 1);
  CHECK(instance.table_count() == 1);
  CHECK(instance.tables[0].entries.size() == 1);
  CHECK(!instance.tables[0].entries[0].is_null);
  CHECK(instance.tables[0].entries[0].function_index == 0);
}

void test_index_equal_to_function_count_fails() {
  Bytes module = module_with_elements(1, 0, 1, {1});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(module, instance, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);
  CHECK(error.message.find("function count is 1") != std::string::npos);
}

void test_larger_boundary_fails() {
  Bytes module = module_with_elements(3, 0, 4, {3});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(module, instance, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);
  CHECK(error.message.find("references function 3") != std::string::npos);
}

void test_expression_form_boundary_fails() {
  Bytes valid = module_with_elements(2, 0, 2, {1}, 1, true);
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(valid, instance, error) ==
        netsec::ErrorCode::ok);
  CHECK(instance.tables[0].entries[1].function_index == 1);

  Bytes boundary = module_with_elements(2, 0, 3, {2}, 0, true);
  CHECK(netsec::decode_and_instantiate(boundary, instance, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);
}

void test_imported_functions_extend_index_space() {
  // Function index space: imported 0, defined 1 and 2. Index 2 is valid.
  Bytes valid = module_with_elements(2, 1, 3, {0, 2});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(valid, instance, error) ==
        netsec::ErrorCode::ok);
  CHECK(instance.function_count() == 3);
  CHECK(!instance.tables[0].entries[0].is_null);
  CHECK(instance.tables[0].entries[0].function_index == 0);
  CHECK(!instance.tables[0].entries[1].is_null);
  CHECK(instance.tables[0].entries[1].function_index == 2);

  // With three functions, index 3 is the first invalid index.
  Bytes invalid = module_with_elements(2, 1, 4, {3});
  CHECK(netsec::decode_and_instantiate(invalid, instance, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);
}

void test_active_segment_table_bounds() {
  // Both function indices are valid, but the table only has one slot.
  Bytes module = module_with_elements(2, 0, 1, {0, 1});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(module, instance, error) ==
        netsec::ErrorCode::table_offset_out_of_bounds);

  Bytes shifted = module_with_elements(1, 0, 3, {0}, 2);
  CHECK(netsec::decode_and_instantiate(shifted, instance, error) ==
        netsec::ErrorCode::ok);
  CHECK(instance.tables[0].entries.size() == 3);
  CHECK(instance.tables[0].entries[2].function_index == 0);
}

void test_zero_functions_rejects_any_index() {
  Bytes module = module_with_elements(0, 0, 1, {0});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(module, instance, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);
}

void test_raw_element_segment_boundary() {
  netsec::ElementSegment segment;
  netsec::Error error;

  Bytes valid = raw_element_segment({0});
  CHECK(netsec::decode_element_segment(valid.data(), valid.size(), 1, segment,
                                       error) == netsec::ErrorCode::ok);
  CHECK(segment.entries.size() == 1);
  CHECK(segment.entries[0].function_index == 0);

  Bytes boundary = raw_element_segment({1});
  CHECK(netsec::decode_element_segment(boundary.data(), boundary.size(), 1,
                                       segment, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);

  Bytes zero_count = raw_element_segment({0});
  CHECK(netsec::decode_element_segment(zero_count.data(), zero_count.size(), 0,
                                       segment, error) ==
        netsec::ErrorCode::element_function_index_out_of_bounds);

  CHECK(netsec::element_function_index_is_valid(0, 1));
  CHECK(!netsec::element_function_index_is_valid(1, 1));
  CHECK(!netsec::element_function_index_is_valid(0, 0));
}

void test_passive_segment_is_not_instantiated() {
  Bytes module = module_with_elements(1, 0, 1, {});
  netsec::Instance instance;
  netsec::Error error;
  CHECK(netsec::decode_and_instantiate(module, instance, error) ==
        netsec::ErrorCode::ok);
  CHECK(instance.tables[0].entries[0].is_null);

  Bytes passive = raw_element_segment({0}, 1);
  netsec::ElementSegment segment;
  CHECK(netsec::decode_element_segment(passive.data(), passive.size(), 1,
                                       segment, error) == netsec::ErrorCode::ok);
  CHECK(segment.mode == netsec::ElementSegment::Mode::passive);
  CHECK(segment.entries.size() == 1);
}

void test_c_api() {
  Bytes valid = module_with_elements(1, 0, 1, {0});
  Bytes invalid = module_with_elements(1, 0, 1, {1});
  char error_buffer[256];

  CHECK(netsec_decode_module(valid.data(), valid.size(), error_buffer,
                             sizeof(error_buffer)) == NETSEC_OK);
  CHECK(netsec_decode_module(invalid.data(), invalid.size(), error_buffer,
                             sizeof(error_buffer)) ==
        NETSEC_ERR_ELEMENT_FUNCTION_INDEX_OUT_OF_BOUNDS);
  CHECK(netsec_parse_module(invalid.data(), invalid.size(), error_buffer,
                            sizeof(error_buffer)) == NETSEC_OK);
  CHECK(netsec_validate_module(valid.data(), valid.size(), error_buffer,
                               sizeof(error_buffer)) == NETSEC_OK);
  CHECK(netsec_validate_module(invalid.data(), invalid.size(), error_buffer,
                               sizeof(error_buffer)) ==
        NETSEC_ERR_ELEMENT_FUNCTION_INDEX_OUT_OF_BOUNDS);

  NetsecInstance* instance = netsec_instantiate_module(
      valid.data(), valid.size(), error_buffer, sizeof(error_buffer));
  CHECK(instance != nullptr);
  if (instance != nullptr) {
    CHECK(netsec_instance_function_count(instance) == 1);
    CHECK(netsec_instance_table_count(instance) == 1);
    CHECK(netsec_instance_table_length(instance, 0) == 1);
    std::uint32_t function_index = 999;
    CHECK(netsec_instance_table_entry(instance, 0, 0, &function_index) == 1);
    CHECK(function_index == 0);
    netsec_instance_free(instance);
  }

  CHECK(netsec_instantiate_module(invalid.data(), invalid.size(), error_buffer,
                                  sizeof(error_buffer)) == nullptr);
  CHECK(netsec_element_index_is_valid(1, 1) == 0);
  CHECK(netsec_element_index_is_valid(0, 1) == 1);

  Bytes boundary = raw_element_segment({1});
  NetsecElementInfo info{};
  CHECK(netsec_decode_element_segment(boundary.data(), boundary.size(), 1,
                                      &info, error_buffer,
                                      sizeof(error_buffer)) ==
        NETSEC_ERR_ELEMENT_FUNCTION_INDEX_OUT_OF_BOUNDS);
  CHECK(std::strstr(error_buffer, "function count is 1") != nullptr);
}

void test_malformed_element_flags() {
  Bytes segment;
  append_u32(segment, 8);  // Reserved / invalid flags.
  netsec::ElementSegment parsed;
  netsec::Error error;
  CHECK(netsec::decode_element_segment(segment.data(), segment.size(), 1,
                                       parsed, error) ==
        netsec::ErrorCode::malformed);
}

void test_truncated_and_mutated_inputs_are_safe() {
  Bytes module = module_with_elements(2, 0, 2, {0, 1});

  for (std::size_t size = 0; size <= module.size(); ++size) {
    netsec::Module parsed;
    netsec::Error error;
    netsec::decode_module(module.data(), size, parsed, error);
    CHECK(static_cast<int>(error.code) >= 0 &&
          static_cast<int>(error.code) <=
              static_cast<int>(netsec::ErrorCode::internal_error));
  }

  for (std::size_t index = 0; index < module.size(); ++index) {
    for (std::uint8_t mutation : {0x01u, 0x7fu, 0x80u, 0xffu}) {
      Bytes mutated = module;
      mutated[index] ^= mutation;
      netsec::Module parsed;
      netsec::Error error;
      netsec::decode_module(mutated.data(), mutated.size(), parsed, error);
      CHECK(static_cast<int>(error.code) >= 0 &&
            static_cast<int>(error.code) <=
                static_cast<int>(netsec::ErrorCode::internal_error));
    }
  }
}

}  // namespace

int main() {
  test_valid_index_initializes_table();
  test_index_equal_to_function_count_fails();
  test_larger_boundary_fails();
  test_expression_form_boundary_fails();
  test_imported_functions_extend_index_space();
  test_active_segment_table_bounds();
  test_zero_functions_rejects_any_index();
  test_raw_element_segment_boundary();
  test_passive_segment_is_not_instantiated();
  test_c_api();
  test_malformed_element_flags();
  test_truncated_and_mutated_inputs_are_safe();

  if (failures == 0) {
    std::printf("all netsec tests passed\n");
    return 0;
  }
  std::fprintf(stderr, "%d netsec test(s) failed\n", failures);
  return 1;
}
