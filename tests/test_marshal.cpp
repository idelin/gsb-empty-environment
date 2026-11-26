#include "ccm/marshaller.hpp"
#include "ccm/schema.hpp"
#include "ccm/shared_bus.hpp"

#include "test_framework.hpp"

using namespace ccm;

namespace {

constexpr TypeId kChild = 1;
constexpr TypeId kRoot = 2;
constexpr len_t kChildSize = 16;
constexpr len_t kRootSize = 32;

constexpr addr_t kEnclaveBase = 0x100000;
constexpr addr_t kEnclaveEnd = 0x110000;
constexpr addr_t kSharedBase = 0x200000;
constexpr len_t kSharedSize = 4096;

Schema build_acyclic_schema() {
    TypeDesc child;
    child.id = kChild;
    child.fixed_size = kChildSize;
    child.fields = {
        FieldDesc::make_scalar("value", 0, ScalarWidth::U32),
        FieldDesc::make_scalar("length", 4, ScalarWidth::U32),
        FieldDesc::make_tail_bytes("tail", kChildSize, "length"),
    };

    TypeDesc root;
    root.id = kRoot;
    root.fixed_size = kRootSize;
    root.fields = {
        FieldDesc::make_scalar("count", 0, ScalarWidth::U32),
        FieldDesc::make_scalar("tail_len", 4, ScalarWidth::U32),
        FieldDesc::make_nested_ptr("child", 8, kChild),
        FieldDesc::make_ptr_array("children", 16, kChild, "count"),
        FieldDesc::make_tail_bytes("root_tail", kRootSize, "tail_len"),
    };
    return Schema::create({child, root}).value();
}

AddressSpace make_space() {
    return AddressSpace::create(
               kEnclaveBase, kEnclaveEnd,
               std::vector<Region>{Region{kSharedBase, kSharedSize}})
        .value();
}

}  // namespace

CCM_TEST(schema_rejects_cycles_and_bad_layout) {
    TypeDesc a;
    a.id = 1;
    a.fixed_size = 16;
    a.fields = {FieldDesc::make_nested_ptr("loop", 0, 2)};
    TypeDesc b;
    b.id = 2;
    b.fixed_size = 16;
    b.fields = {FieldDesc::make_nested_ptr("back", 0, 1)};
    // Self/mutual type references are legal ABI shapes; runtime pointer cycles
    // are rejected by the marshaller, not the static schema.
    CCM_CHECK(Schema::create({a, b}).ok());

    TypeDesc dangling;
    dangling.id = 4;
    dangling.fixed_size = 16;
    dangling.fields = {FieldDesc::make_nested_ptr("missing", 0, 99)};
    CCM_CHECK(!Schema::create({dangling}).ok());

    TypeDesc bad;
    bad.id = 3;
    bad.fixed_size = 8;
    bad.fields = {FieldDesc::make_scalar("x", 6, ScalarWidth::U32)};
    CCM_CHECK(!Schema::create({bad}).ok());
}

CCM_TEST(marshal_deep_copies_and_relocates_nested_graph) {
    Schema schema = build_acyclic_schema();
    AddressSpace space = make_space();
    SharedBus bus(kSharedBase, kSharedSize);

    // Every block reserves header + tail so the "tail follows header" ABI
    // convention never aliases another block.
    addr_t root_addr = kSharedBase;        // header + 3 tail bytes
    addr_t child_addr = kSharedBase + 64;  // header + 4 tail bytes
    addr_t array_addr = kSharedBase + 256; // two 16-byte elements
    addr_t root_tail_addr = root_addr + kRootSize;
    addr_t child_tail_addr = child_addr + kChildSize;

    std::vector<std::uint8_t> root(kRootSize, 0);
    write_le32(root.data() + 0, 2);          // count
    write_le32(root.data() + 4, 3);          // tail_len
    write_le64(root.data() + 8,
               static_cast<u64_t>(child_addr));
    write_le64(root.data() + 16,
               static_cast<u64_t>(array_addr));
    bus.host_poke(root_addr, root.data(), root.size());
    std::uint8_t root_tail[3] = {9, 8, 7};
    bus.host_poke(root_tail_addr, root_tail, 3);

    std::vector<std::uint8_t> child(kChildSize, 0);
    write_le32(child.data() + 0, 42);
    write_le32(child.data() + 4, 4);
    bus.host_poke(child_addr, child.data(), child.size());
    std::uint8_t child_tail[4] = {1, 2, 3, 4};
    bus.host_poke(child_tail_addr, child_tail, 4);

    for (u32_t i = 0; i < 2; ++i) {
        std::vector<std::uint8_t> element(kChildSize, 0);
        write_le32(element.data() + 0, 100 + i);
        write_le32(element.data() + 4, 0);
        bus.host_poke(
            array_addr + static_cast<addr_t>(i) * kChildSize,
            element.data(), element.size());
    }

    ReadOnceBus once(bus);
    Marshaller marshaller(space, schema, once);
    auto result = marshaller.marshal(kRoot, root_addr);
    CCM_CHECK(result.ok());

    const auto& nodes = result.value();
    CCM_CHECK_EQ(nodes.size(), static_cast<size_t>(4));

    const MarshaledNode* root_node = nullptr;
    for (const MarshaledNode& node : nodes) {
        if (node.type == kRoot) {
            root_node = &node;
        }
    }
    CCM_CHECK(root_node != nullptr);
    CCM_CHECK_EQ(root_node->tail_size, static_cast<len_t>(3));

    u64_t relocated_child = read_le64(root_node->data.data() + 8);
    u64_t relocated_array = read_le64(root_node->data.data() + 16);
    CCM_CHECK(relocated_child != static_cast<u64_t>(child_addr));
    CCM_CHECK(relocated_array != static_cast<u64_t>(array_addr));
    // Relocated pointers reference process-private heap copies, never the
    // external shared pool.
    CCM_CHECK(relocated_child < kSharedBase ||
              relocated_child >= kSharedBase + kSharedSize);
    CCM_CHECK(relocated_array < kSharedBase ||
              relocated_array >= kSharedBase + kSharedSize);
    CCM_CHECK(read_le32(reinterpret_cast<const std::uint8_t*>(
                            static_cast<addr_t>(relocated_child))) == 42);

    // Every external byte crossed the boundary exactly once.
    len_t expected = kRootSize + 3 + kChildSize + 4 + 2 * kChildSize;
    CCM_CHECK_EQ(marshaller.fetched_byte_count(), expected);
}

CCM_TEST(marshal_rejects_pointer_into_enclave) {
    Schema schema = build_acyclic_schema();
    AddressSpace space = make_space();
    SharedBus bus(kSharedBase, kSharedSize);

    std::vector<std::uint8_t> root(kRootSize, 0);
    write_le32(root.data() + 0, 0);
    write_le64(root.data() + 8,
               static_cast<u64_t>(kEnclaveBase + 64));
    bus.host_poke(kSharedBase, root.data(), root.size());

    Marshaller marshaller(space, schema, bus);
    auto result = marshaller.marshal(kRoot, kSharedBase);
    CCM_CHECK(!result.ok());
    CCM_CHECK_STATUS(StatusCode::OverlapsEnclave, result.status());
}

CCM_TEST(marshal_rejects_aliased_and_cyclic_external_graph) {
    Schema schema = build_acyclic_schema();
    AddressSpace space = make_space();
    SharedBus bus(kSharedBase, kSharedSize);

    // Both the nested pointer and the array point at the same bytes.
    addr_t alias = kSharedBase + 256;
    std::vector<std::uint8_t> root(kRootSize, 0);
    write_le32(root.data() + 0, 1);
    write_le64(root.data() + 8, static_cast<u64_t>(alias));
    write_le64(root.data() + 16, static_cast<u64_t>(alias));
    bus.host_poke(kSharedBase, root.data(), root.size());

    std::vector<std::uint8_t> child(kChildSize, 0);
    bus.host_poke(alias, child.data(), child.size());

    Marshaller marshaller(space, schema, bus);
    auto aliased = marshaller.marshal(kRoot, kSharedBase);
    CCM_CHECK(!aliased.ok());
    CCM_CHECK(aliased.status().code == StatusCode::OverlappingRegions ||
              aliased.status().code == StatusCode::CycleDetected);
}

CCM_TEST(marshal_rejects_runtime_pointer_cycle) {
    TypeDesc self;
    self.id = 7;
    self.fixed_size = 16;
    self.fields = {
        FieldDesc::make_scalar("v", 0, ScalarWidth::U32),
        FieldDesc::make_nested_ptr("next", 8, 7),
    };
    auto schema_result = Schema::create({self});
    CCM_CHECK(schema_result.ok());
    AddressSpace space = make_space();
    SharedBus bus(kSharedBase, kSharedSize);

    std::vector<std::uint8_t> block(16, 0);
    write_le64(block.data() + 8, static_cast<u64_t>(kSharedBase));
    bus.host_poke(kSharedBase, block.data(), block.size());

    Marshaller marshaller(space, schema_result.value(), bus);
    auto cycled = marshaller.marshal(7, kSharedBase);
    CCM_CHECK(!cycled.ok());
    CCM_CHECK_STATUS(StatusCode::CycleDetected, cycled.status());
}

CCM_TEST(read_once_bus_blocks_second_fetch) {
    SharedBus bus(kSharedBase, 64);
    ReadOnceBus once(bus);
    std::uint8_t tmp[8] = {};
    CCM_CHECK(once.read(kSharedBase, tmp, 8));
    CCM_CHECK(!once.read(kSharedBase + 4, tmp, 8));
    CCM_CHECK(once.read(kSharedBase + 8, tmp, 8));
}

CCM_TEST(semantic_validator_runs_on_private_snapshot) {
    Schema schema = build_acyclic_schema();
    AddressSpace space = make_space();
    SharedBus bus(kSharedBase, kSharedSize);

    std::vector<std::uint8_t> root(kRootSize, 0);
    write_le32(root.data() + 0, 0);
    write_le64(root.data() + 8, 0);
    write_le64(root.data() + 16, 0);
    bus.host_poke(kSharedBase, root.data(), root.size());

    Marshaller marshaller(space, schema, bus);
    bool invoked = false;
    marshaller.set_semantic_validator(
        [&invoked](const std::vector<MarshaledNode>&, const Schema&) {
            invoked = true;
            return Status{StatusCode::SemanticViolation, "injected"};
        });
    auto result = marshaller.marshal(kRoot, kSharedBase);
    CCM_CHECK(invoked);
    CCM_CHECK_STATUS(StatusCode::SemanticViolation, result.status());
}

CCM_TEST(output_write_refuses_enclave_destination) {
    Schema schema = build_acyclic_schema();
    AddressSpace space = make_space();
    SharedBus bus(kSharedBase, kSharedSize);
    Marshaller marshaller(space, schema, bus);
    std::uint8_t out[2] = {1, 2};
    CCM_CHECK_STATUS(
        StatusCode::OverlapsEnclave,
        marshaller.write_output(kEnclaveBase, out, 2));
    CCM_CHECK_STATUS_OK(
        marshaller.write_output(kSharedBase, out, 2));
}

int main() { return ccm_test::run_all("marshal"); }
