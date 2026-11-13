#include "netsec.hpp"

#include <cstdint>
#include <cstring>
#include <iostream>
#include <limits>
#include <string>
#include <vector>

namespace {

using Bytes = std::vector<std::uint8_t>;

int failures = 0;

void check(bool condition, const std::string& message) {
  if (!condition) {
    std::cerr << "FAIL: " << message << '\n';
    ++failures;
  }
}

void put_u8(Bytes& output, std::uint8_t value) { output.push_back(value); }

void put_uleb(Bytes& output, std::uint64_t value) {
  do {
    std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7f);
    value >>= 7;
    if (value != 0) {
      byte |= 0x80;
    }
    output.push_back(byte);
  } while (value != 0);
}

void put_sleb(Bytes& output, std::int64_t value) {
  bool more = true;
  while (more) {
    const std::uint8_t byte = static_cast<std::uint8_t>(value & 0x7f);
    const bool sign_set = (byte & 0x40) != 0;
    value >>= 7;
    more = !((value == 0 && !sign_set) || (value == -1 && sign_set));
    output.push_back(static_cast<std::uint8_t>(byte | (more ? 0x80 : 0x00)));
  }
}

void put_section(Bytes& module, std::uint8_t id, const Bytes& payload) {
  module.push_back(id);
  put_uleb(module, payload.size());
  module.insert(module.end(), payload.begin(), payload.end());
}

struct SegmentSpec {
  std::int64_t offset = 0;
  Bytes bytes;
  std::uint32_t memory_index = 0;
  bool explicit_memory = false;
  bool passive = false;
};

void append_data_section(Bytes& module,
                         const std::vector<SegmentSpec>& segments,
                         bool include_data_count) {
  if (include_data_count) {
    Bytes data_count_section;
    put_uleb(data_count_section, segments.size());
    put_section(module, 12, data_count_section);
  }

  Bytes data_section;
  put_uleb(data_section, segments.size());
  for (const SegmentSpec& segment : segments) {
    if (segment.passive) {
      put_uleb(data_section, 1);
    } else if (segment.explicit_memory) {
      put_uleb(data_section, 2);
      put_uleb(data_section, segment.memory_index);
      put_u8(data_section, 0x41);
      put_sleb(data_section, segment.offset);
      put_u8(data_section, 0x0b);
    } else {
      put_uleb(data_section, 0);
      put_u8(data_section, 0x41);
      put_sleb(data_section, segment.offset);
      put_u8(data_section, 0x0b);
    }
    put_uleb(data_section, segment.bytes.size());
    data_section.insert(data_section.end(), segment.bytes.begin(),
                        segment.bytes.end());
  }
  put_section(module, 11, data_section);
}

Bytes make_module(std::uint32_t pages,
                  const std::vector<SegmentSpec>& segments,
                  bool include_data_count = false) {
  Bytes module = {0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00};

  Bytes memory_section;
  put_uleb(memory_section, 1);
  put_u8(memory_section, 0x00);
  put_uleb(memory_section, pages);
  put_section(module, 5, memory_section);

  append_data_section(module, segments, include_data_count);

  return module;
}

Bytes make_imported_memory_module(
    std::uint32_t pages, const std::vector<SegmentSpec>& segments) {
  Bytes module = {0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00};

  Bytes import_section;
  put_uleb(import_section, 1);
  put_uleb(import_section, 3);
  import_section.insert(import_section.end(), {'e', 'n', 'v'});
  put_uleb(import_section, 6);
  import_section.insert(import_section.end(),
                        {'m', 'e', 'm', 'o', 'r', 'y'});
  put_u8(import_section, 0x02);
  put_u8(import_section, 0x00);
  put_uleb(import_section, pages);
  put_section(module, 2, import_section);

  append_data_section(module, segments, false);
  return module;
}

void test_bounds_rule() {
  check(netsec::active_data_segment_in_bounds(65536, 65536, 0),
        "zero-length segment at the end of memory is in bounds");
  check(!netsec::active_data_segment_in_bounds(65536, 65536, 1),
        "segment at the end of memory with a byte is out of bounds");
  check(netsec::active_data_segment_in_bounds(65536, 65535, 1),
        "last byte of memory is writable");
  check(!netsec::active_data_segment_in_bounds(65536, 65535, 2),
        "segment crossing the end of memory is out of bounds");
  check(netsec::active_data_segment_in_bounds(0, 0, 0),
        "empty memory accepts an empty segment at offset zero");
  check(!netsec::active_data_segment_in_bounds(0, 1, 0),
        "empty memory rejects an offset past its end");
  check(!netsec::active_data_segment_in_bounds(
            std::numeric_limits<std::uint64_t>::max(),
            std::numeric_limits<std::uint64_t>::max(), 1),
        "offset plus length overflow is out of bounds");
}

void test_segment_at_end_of_last_page_fails() {
  const Bytes wasm = make_module(1, {{65536, {0xaa}}});
  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(!result.ok(), "non-empty segment at end of last page must not instantiate");
  check(result.error.find("out of bounds") != std::string::npos,
        "failure should identify the out-of-bounds data segment");
}

void test_segment_inside_memory_succeeds() {
  const Bytes payload = {0x10, 0x20, 0x30, 0x40, 0x50, 0x60};
  const Bytes wasm = make_module(1, {{65530, payload}});
  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(result.ok(), "segment fully inside memory must instantiate");
  if (!result.ok()) {
    return;
  }

  const netsec::Memory& memory = result.instance->memory(0);
  check(memory.size() == 65536, "memory should be one wasm page");
  check(std::memcmp(memory.data() + 65530, payload.data(), payload.size()) == 0,
        "data segment bytes should be written at the encoded offset");
}

void test_full_page_segment_succeeds() {
  const Bytes payload(65536, 0x5a);
  const Bytes wasm = make_module(1, {{0, payload}});
  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(result.ok(), "a segment covering all of memory must instantiate");
  if (!result.ok()) {
    return;
  }

  const netsec::Memory& memory = result.instance->memory(0);
  check(std::memcmp(memory.data(), payload.data(), payload.size()) == 0,
        "a full-page segment should be written completely");
}

void test_module_with_standard_sections() {
  Bytes wasm = {0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00};
  put_section(wasm, 1, {0x01, 0x60, 0x00, 0x00});
  put_section(wasm, 3, {0x01, 0x00});
  put_section(wasm, 5, {0x01, 0x00, 0x01});
  put_section(wasm, 10, {0x01, 0x02, 0x00, 0x0b});
  put_section(wasm, 11, {0x01, 0x00, 0x41, 0x04, 0x0b, 0x01, 0x99});

  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(result.ok(), "module with type/function/code sections should instantiate");
  if (result.ok()) {
    check(result.instance->memory(0)[4] == 0x99,
          "data segment should be applied after standard sections");
  }
}

void test_zero_length_at_end_of_last_page_succeeds() {
  const Bytes wasm = make_module(1, {{65536, {}}});
  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(result.ok(), "empty segment at end of last page is valid");
}

void test_out_of_bounds_segment_writes_nothing() {
  const Bytes wasm = make_module(
      1, {{0, {'G', 'O', 'O', 'D'}}, {65534, {'B', 'A', 'D', '!'}}});
  netsec::DecodeResult decoded = netsec::decode(wasm);
  check(decoded.ok(), "atomicity test module should decode");
  if (!decoded.ok()) {
    return;
  }

  std::vector<netsec::Memory> memories;
  memories.emplace_back(1, 0xcc);
  const std::vector<netsec::GlobalValue> globals;
  netsec::DataApplicationResult result =
      netsec::apply_active_data_segments(*decoded.module, globals, memories);

  check(!result.ok(), "partially out-of-bounds segment must be rejected");
  bool untouched = true;
  for (std::size_t i = 0; i < memories[0].size(); ++i) {
    if (memories[0][i] != 0xcc) {
      untouched = false;
      break;
    }
  }
  check(untouched,
        "no segment prefix may be written when one active segment is invalid");
}

void test_segment_at_end_of_multiple_pages_succeeds() {
  const Bytes wasm = make_module(2, {{65536, {0x11, 0x22}}});
  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(result.ok(), "offset at the start of the final page is in bounds");
  if (!result.ok()) {
    return;
  }
  check(result.instance->memory(0)[65536] == 0x11 &&
            result.instance->memory(0)[65537] == 0x22,
        "data should be written at the start of the second page");
}

void test_explicit_memory_index_and_data_count() {
  SegmentSpec segment;
  segment.offset = 4;
  segment.bytes = {0xde, 0xad};
  segment.memory_index = 0;
  segment.explicit_memory = true;
  const Bytes wasm = make_module(1, {segment}, true);
  netsec::InstantiateResult result = netsec::instantiate(wasm);
  check(result.ok(), "explicit memory index with data count section should work");
  if (result.ok()) {
    check(result.instance->memory(0)[4] == 0xde &&
              result.instance->memory(0)[5] == 0xad,
          "explicitly indexed active segment should be written");
  }
}

void test_imported_memory_write_is_transactional() {
  netsec::Memory host_memory(1, 0xcc);
  netsec::Imports imports;
  imports.memories.push_back(host_memory);

  const Bytes good = make_imported_memory_module(1, {{0, {0x33, 0x44}}});
  netsec::InstantiateResult success = netsec::instantiate(good, imports);
  check(success.ok(), "valid segment should instantiate in imported memory");
  check(host_memory[0] == 0x33 && host_memory[1] == 0x44,
        "successful writes should be visible through the imported memory");

  netsec::Memory untouched(1, 0xcc);
  netsec::Imports failing_imports;
  failing_imports.memories.push_back(untouched);
  const Bytes bad = make_imported_memory_module(
      1, {{0, {'G', 'O', 'O', 'D'}}, {65534, {'B', 'A', 'D', '!'}}});
  netsec::InstantiateResult failure = netsec::instantiate(bad, failing_imports);
  check(!failure.ok(), "out-of-bounds imported-memory segment must fail");

  bool untouched_after_failure = true;
  for (std::size_t i = 0; i < untouched.size(); ++i) {
    if (untouched[i] != 0xcc) {
      untouched_after_failure = false;
      break;
    }
  }
  check(untouched_after_failure,
        "failed instantiation must not leave a written prefix in imported memory");
}

void test_c_api() {
  const Bytes good = make_module(1, {{0, {0x42}}});
  netsec_instance* instance = nullptr;
  char error[256] = {};
  netsec_result_code code =
      netsec_instantiate(good.data(), good.size(), &instance, error, sizeof(error));
  check(code == NETSEC_OK && instance != nullptr,
        "C API should instantiate a valid module");
  if (instance != nullptr) {
    std::size_t size = 0;
    const std::uint8_t* data =
        netsec_instance_memory_data(instance, 0, &size);
    check(size == 65536 && data != nullptr && data[0] == 0x42,
          "C API should expose the initialized memory");
    netsec_instance_destroy(instance);
  }

  const Bytes bad = make_module(1, {{65536, {0x42}}});
  instance = nullptr;
  code = netsec_instantiate(bad.data(), bad.size(), &instance, error,
                            sizeof(error));
  check(code == NETSEC_INSTANTIATION_ERROR && instance == nullptr,
        "C API should reject an out-of-bounds active segment");
  check(std::strstr(error, "out of bounds") != nullptr,
        "C API should report the bounds failure");
}

}  // namespace

int main() {
  test_bounds_rule();
  test_segment_at_end_of_last_page_fails();
  test_segment_inside_memory_succeeds();
  test_full_page_segment_succeeds();
  test_module_with_standard_sections();
  test_zero_length_at_end_of_last_page_succeeds();
  test_out_of_bounds_segment_writes_nothing();
  test_segment_at_end_of_multiple_pages_succeeds();
  test_explicit_memory_index_and_data_count();
  test_imported_memory_write_is_transactional();
  test_c_api();

  if (failures != 0) {
    std::cerr << failures << " test(s) failed\n";
    return 1;
  }
  std::cout << "all netsec tests passed\n";
  return 0;
}
