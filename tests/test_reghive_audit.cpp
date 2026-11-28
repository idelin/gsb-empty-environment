#include "reghive_audit.hpp"
#include "hive_builder.hpp"

#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>
#include <vector>

using namespace reghive_audit;
using namespace reghive_audit_test;

namespace {

int g_failures = 0;
int g_checks = 0;

void check(bool condition, const std::string& name,
           AuditStatus got = AuditStatus::Ok) {
    ++g_checks;
    if (!condition) {
        ++g_failures;
        std::printf("[FAIL] %s (status=%s)\n", name.c_str(),
                    status_name(got));
    } else {
        std::printf("[ ok ] %s\n", name.c_str());
    }
}

void expect_status(const AuditReport& r, AuditStatus expected,
                   const std::string& name) {
    check(!r.ok && r.status == expected, name, r.status);
}

std::vector<uint8_t> name_bytes(const char* s) {
    return std::vector<uint8_t>(s, s + std::strlen(s));
}

// Fully valid hive exercising every structure type: lf/lh/li/ri subkey
// indexes, nk nodes, value lists, vk inline/external values, plain data and
// a db big-data cell with two segments.
//
// Subkey topology (all four list signatures are reachable):
//   root --stable(lf)--> leafA, leafB
//        --volatile(ri)--> li --> leafC
//        (lh is used as the stable index in a dedicated hive variant)
std::vector<uint8_t> build_valid_hive(bool use_lh = false) {
    HiveBuilder b;

    auto leaf_a = b.add_cell(HiveBuilder::make_nk(name_bytes("L_A")));
    auto leaf_b = b.add_cell(HiveBuilder::make_nk(name_bytes("L_B")));
    auto leaf_c = b.add_cell(HiveBuilder::make_nk(name_bytes("L_C")));

    std::vector<uint32_t> stable_refs = {
        leaf_a.hive_offset, leaf_b.hive_offset};
    auto stable_list = use_lh
        ? b.add_cell(HiveBuilder::make_list('l', 'h', stable_refs,
                                            true, true))
        : b.add_cell(HiveBuilder::make_list('l', 'f', stable_refs,
                                            true, true));
    auto li_list = b.add_cell(HiveBuilder::make_list(
        'l', 'i', std::vector<uint32_t>{leaf_c.hive_offset},
        false, false));
    auto ri_list = b.add_cell(HiveBuilder::make_list(
        'r', 'i', std::vector<uint32_t>{li_list.hive_offset},
        false, false));

    std::vector<uint8_t> plain_bytes = {'H', 'E', 'L', 'L', 'O', 0};
    auto data_plain = b.add_cell(
        HiveBuilder::make_plain_data(plain_bytes));
    auto vk_ext = b.add_cell(HiveBuilder::make_vk(
        name_bytes("ExtVal"),
        static_cast<uint32_t>(plain_bytes.size()),
        data_plain.hive_offset, 0x18));

    std::vector<uint8_t> seg1_bytes(20, 'A');
    std::vector<uint8_t> seg2_bytes(12, 'B');
    auto seg1 = b.add_cell(HiveBuilder::make_plain_data(seg1_bytes));
    auto seg2 = b.add_cell(HiveBuilder::make_plain_data(seg2_bytes));
    auto seg_list = b.add_cell(HiveBuilder::make_segment_list(
        std::vector<uint32_t>{seg1.hive_offset, seg2.hive_offset}));
    auto db_cell = b.add_cell(
        HiveBuilder::make_db(2, seg_list.hive_offset));
    auto vk_db = b.add_cell(HiveBuilder::make_vk(
        name_bytes("BigVal"), 30u, db_cell.hive_offset, 0x18));

    std::vector<uint8_t> vk_inline_payload =
        HiveBuilder::make_vk(name_bytes("Inl"), 0x80000003u, 0u, 0x18);
    vk_inline_payload[0x0C] = 'X';
    vk_inline_payload[0x0D] = 'Y';
    vk_inline_payload[0x0E] = 'Z';
    auto vk_inline = b.add_cell(vk_inline_payload);

    auto value_list = b.add_cell(HiveBuilder::make_value_list(
        std::vector<uint32_t>{vk_ext.hive_offset, vk_db.hive_offset,
                              vk_inline.hive_offset}));

    auto root = b.add_cell(HiveBuilder::make_nk(
        name_bytes("CMI_ROOT"), NONE,
        stable_list.hive_offset, ri_list.hive_offset,
        2u, 1u, value_list.hive_offset, 3u));

    return b.build(root);
}

}  // namespace

int main() {
    // ---- checksum boundary contract ----
    {
        std::vector<uint8_t> zeros(508u, 0);
        check(regf_checksum(zeros.data(), zeros.size()) == 0x00000000u,
              "checksum of 508 zero bytes is 0x00000000");
        std::vector<uint8_t> ones(508u, 0xFF);
        check(regf_checksum(ones.data(), ones.size()) == 0xFFFFFFFFu,
              "checksum of 508 0xFF bytes is 0xFFFFFFFF");
        check(regf_checksum(nullptr, 508u) == 0u,
              "checksum rejects null buffer");
        check(regf_checksum(zeros.data(), 507u) == 0u,
              "checksum rejects wrong length");
    }

    // ---- hand-crafted valid micro hive (lf + ri/li) ----
    std::vector<uint8_t> valid = build_valid_hive(false);
    {
        AuditReport r = audit(valid);
        check(r.ok && r.status == AuditStatus::Ok,
              "valid hand-crafted hive (lf/ri) accepted", r.status);
        check(r.hbin_count >= 1, "hbin count populated", r.status);
        check(r.nk_count == 4, "four nk nodes audited", r.status);
        check(r.vk_count == 3, "three vk nodes audited", r.status);
        // ri -> li -> leaf nests one index level below the direct lf leaves.
        check(r.max_depth_reached == 2, "max depth reflects ri/li nesting",
              r.status);
    }

    // ---- same hive through the lh signature path ----
    {
        std::vector<uint8_t> lh_hive = build_valid_hive(true);
        AuditReport r = audit(lh_hive);
        check(r.ok && r.status == AuditStatus::Ok,
              "valid hive (lh index) accepted", r.status);
    }

    // ---- multi-hbin hive: cells span more than one 4096-byte block ----
    {
        HiveBuilder b;
        std::vector<HiveBuilder::PlacedCell> leaves;
        for (int i = 0; i < 60; ++i) {
            leaves.push_back(
                b.add_cell(HiveBuilder::make_nk(name_bytes("BIG_KEY_NODE"))));
        }
        std::vector<uint32_t> refs;
        for (const auto& leaf : leaves) {
            refs.push_back(leaf.hive_offset);
        }
        auto li = b.add_cell(HiveBuilder::make_list(
            'l', 'i', refs, false, false));
        auto root = b.add_cell(HiveBuilder::make_nk(
            name_bytes("ROOT"), NONE, li.hive_offset, NONE,
            static_cast<uint32_t>(leaves.size()), 0u, NONE, 0u));
        std::vector<uint8_t> image = b.build(root);
        AuditReport r = audit(image);
        check(r.ok && r.status == AuditStatus::Ok,
              "multi-hbin hive accepted", r.status);
        check(r.hbin_count >= 2, "multiple hbins scanned", r.status);
        check(r.nk_count == static_cast<size_t>(leaves.size()) + 1u,
              "all cross-hbin nk nodes reached", r.status);
    }

    // ---- attack 1: regf checksum tampering ----
    {
        std::vector<uint8_t> bad = valid;
        bad[0x30] = static_cast<uint8_t>(bad[0x30] ^ 0x01u);
        expect_status(audit(bad), AuditStatus::ChecksumMismatch,
                      "regf checksum tamper detected");
    }

    // ---- regf header structural damage ----
    {
        std::vector<uint8_t> bad = valid;
        put32(bad, 0x08, get32(bad, 0x04) + 1u);
        expect_status(audit(bad), AuditStatus::SequenceMismatch,
                      "primary/secondary sequence mismatch detected");
    }
    {
        std::vector<uint8_t> bad = valid;
        bad[0] = 'X';
        expect_status(audit(bad), AuditStatus::BadRegfMagic,
                      "bad regf magic detected");
    }
    {
        std::vector<uint8_t> bad = valid;
        bad.push_back(0);
        expect_status(audit(bad), AuditStatus::HiveBinsSizeMismatch,
                      "trailing hidden data rejected");
    }
    {
        std::vector<uint8_t> bad(valid.begin(),
                                 valid.begin() + 0x1000);
        expect_status(audit(bad), AuditStatus::HiveBinsSizeMismatch,
                      "truncated hive body rejected");
    }

    // ---- attack 2: hbin cell overlap ----
    // Corrupt a subkey entry so it points at the payload interior of another
    // allocated cell.  RootCellOffset itself is left intact (otherwise the
    // regf checksum check would fire first).
    {
        std::vector<uint8_t> bad = valid;
        // Root nk's stable list pointer field (nk payload offset 0x18).
        uint32_t root_off = get32(bad, 0x24);
        size_t list_ptr_field = 0x1000u + root_off + 4u + 0x18u;
        uint32_t list_hive = get32(bad, list_ptr_field);
        size_t entry_field = 0x1000u + list_hive + 4u + 6u;  // lf first entry
        // 8-byte-aligned point strictly inside the list cell's footprint.
        put32(bad, entry_field, list_hive + 8u);
        put32(bad, 0x1FC, compute_regf_checksum(bad));
        expect_status(audit(bad), AuditStatus::CellOverlap,
                      "overlapping cell reference detected");
    }

    // ---- hbin structural damage ----
    {
        std::vector<uint8_t> bad = valid;
        bad[0x1000] = 'x';
        expect_status(audit(bad), AuditStatus::BadHbinMagic,
                      "bad hbin magic detected");
    }
    {
        std::vector<uint8_t> bad = valid;
        put32(bad, 0x1000 + 0x04, 0x1000u);  // rel offset no longer 0
        expect_status(audit(bad),
                      AuditStatus::HbinOffsetDiscontinuity,
                      "hbin relative-offset discontinuity detected");
    }
    {
        std::vector<uint8_t> bad = valid;
        put32(bad, 0x1000 + 0x04, 0x0100u);  // not 4096-aligned
        put32(bad, 0x1FC, compute_regf_checksum(bad));
        expect_status(audit(bad), AuditStatus::HbinOffsetMisaligned,
                      "non-4096-aligned hbin offset detected");
    }

    // ---- attack 3: nk subkey directed cycle ----
    // Two leaf nk cells; point the second leaf's stable list at an li whose
    // entry points back into the second leaf itself.
    {
        HiveBuilder b;
        auto leaf = b.add_cell(HiveBuilder::make_nk(name_bytes("L")));
        auto li_list = b.add_cell(HiveBuilder::make_list(
            'l', 'i', std::vector<uint32_t>{0u}, false, false));
        auto root = b.add_cell(HiveBuilder::make_nk(
            name_bytes("ROOT"), NONE, li_list.hive_offset, NONE,
            1u, 0u, NONE, 0u));
        std::vector<uint8_t> image = b.build(root);

        // Patch the li entry to reference itself's child nk (= leaf), and
        // force leaf to loop back through the same li list.
        size_t li_payload_file = 0x1000u + li_list.hive_offset + 4u + 8u;
        put32(image, li_payload_file, leaf.hive_offset);
        // Rewrite leaf's stable list to the li list, forming leaf -> li -> leaf.
        size_t leaf_list_field =
            0x1000u + leaf.hive_offset + 4u + 0x18u;
        put32(image, leaf_list_field, li_list.hive_offset);
        size_t leaf_count_field =
            0x1000u + leaf.hive_offset + 4u + 0x20u;
        put32(image, leaf_count_field, 1u);
        put32(image, 0x1FC, compute_regf_checksum(image));

        AuditReport cr = audit(image);
        expect_status(cr, AuditStatus::CycleDetected,
                      "nk subkey directed cycle detected");
        check(cr.hive_offset >= 0 &&
                  cr.file_offset == 0x1000u +
                      static_cast<size_t>(cr.hive_offset),
              "cycle report carries precise cell offsets");
    }

    // ---- attack 4: out-of-bounds vk data offset ----
    {
        HiveBuilder b;
        auto vk = b.add_cell(HiveBuilder::make_vk(
            name_bytes("V"), 8u, 0x0FFFFFF8u, 0x18));
        auto value_list = b.add_cell(HiveBuilder::make_value_list(
            std::vector<uint32_t>{vk.hive_offset}));
        auto root = b.add_cell(HiveBuilder::make_nk(
            name_bytes("ROOT"), NONE, NONE, NONE, 0u, 0u,
            value_list.hive_offset, 1u));
        std::vector<uint8_t> image = b.build(root);
        expect_status(audit(image), AuditStatus::ValueDataOutOfBounds,
                      "out-of-bounds vk data offset detected");
    }

    // ---- unaligned cell reference ----
    {
        HiveBuilder b;
        auto root = b.add_cell(HiveBuilder::make_nk(name_bytes("R")));
        std::vector<uint8_t> image = b.build(root);
        // Odd offset that still lands inside the hive body, so the alignment
        // check (not an earlier size/checksum check) is the one to fire.
        put32(image, 0x24, 0x21u);
        put32(image, 0x1FC, compute_regf_checksum(image));
        expect_status(audit(image), AuditStatus::CellOffsetUnaligned,
                      "non-8-byte-aligned root offset detected");
    }

    // ---- zero-length cell inside an hbin ----
    {
        std::vector<uint8_t> bad = valid;
        put32(bad, 0x1000 + 0x20, 0u);  // first cell size -> 0
        expect_status(audit(bad), AuditStatus::CellSizeZero,
                      "zero-length cell rejected");
    }

    // ---- db big-data segment overrun ----
    {
        HiveBuilder b;
        auto seg1 = b.add_cell(
            HiveBuilder::make_plain_data(std::vector<uint8_t>(4, 'A')));
        auto seg_list = b.add_cell(HiveBuilder::make_segment_list(
            std::vector<uint32_t>{seg1.hive_offset}));
        auto db_cell = b.add_cell(
            HiveBuilder::make_db(1, seg_list.hive_offset));
        auto vk = b.add_cell(HiveBuilder::make_vk(
            name_bytes("Big"), 4096u, db_cell.hive_offset, 0x18));
        auto value_list = b.add_cell(HiveBuilder::make_value_list(
            std::vector<uint32_t>{vk.hive_offset}));
        auto root = b.add_cell(HiveBuilder::make_nk(
            name_bytes("ROOT"), NONE, NONE, NONE, 0u, 0u,
            value_list.hive_offset, 1u));
        std::vector<uint8_t> image = b.build(root);
        expect_status(audit(image), AuditStatus::BigDataInvalid,
                      "db segment total shorter than value length");
    }

    // ---- depth limit guards stack exhaustion ----
    {
        HiveBuilder b;
        const uint32_t chain_len = 20;
        std::vector<HiveBuilder::PlacedCell> nodes;
        for (uint32_t i = 0; i < chain_len; ++i) {
            nodes.push_back(
                b.add_cell(HiveBuilder::make_nk(name_bytes("N"))));
        }
        // Each node uses an li index pointing at the next node.
        std::vector<HiveBuilder::PlacedCell> lists;
        for (uint32_t i = 0; i + 1u < chain_len; ++i) {
            auto li = b.add_cell(HiveBuilder::make_list(
                'l', 'i',
                std::vector<uint32_t>{nodes[i + 1u].hive_offset},
                false, false));
            lists.push_back(li);
        }
        auto root = nodes[0];
        for (uint32_t i = 0; i + 1u < chain_len; ++i) {
            size_t list_field =
                0x1000u + nodes[i].hive_offset + 4u + 0x18u;
            size_t count_field =
                0x1000u + nodes[i].hive_offset + 4u + 0x20u;
            // Patched after build because offsets are already final.
            (void)list_field;
            (void)count_field;
        }
        std::vector<uint8_t> image = b.build(root);
        for (uint32_t i = 0; i + 1u < chain_len; ++i) {
            size_t list_field =
                0x1000u + nodes[i].hive_offset + 4u + 0x18u;
            size_t count_field =
                0x1000u + nodes[i].hive_offset + 4u + 0x20u;
            put32(image, list_field, lists[i].hive_offset);
            put32(image, count_field, 1u);
        }
        put32(image, 0x1FC, compute_regf_checksum(image));

        AuditOptions tight;
        tight.max_tree_depth = 5;
        expect_status(audit(image, tight),
                      AuditStatus::DepthLimitExceeded,
                      "depth limit enforced");
        AuditReport shallow = audit(image);
        check(shallow.ok && shallow.max_depth_reached == chain_len - 1u,
              "chain accepted with default depth", shallow.status);
    }

    std::printf("\n%d/%d checks passed\n",
                g_checks - g_failures, g_checks);
    return g_failures == 0 ? 0 : 1;
}
