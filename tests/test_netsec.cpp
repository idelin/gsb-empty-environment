#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <cstdio>
#include <map>
#include <optional>
#include <set>
#include <string>
#include <string_view>
#include <vector>

namespace {

int failures = 0;

#define CHECK(condition)                                                           \
    do {                                                                           \
        if (!(condition)) {                                                        \
            std::fprintf(stderr, "CHECK failed at %s:%d: %s\n", __FILE__, __LINE__, \
                         #condition);                                              \
            ++failures;                                                            \
        }                                                                          \
    } while (false)

struct BuildResult {
    std::string pdf;
    std::uint64_t xref_offset = 0;
    std::uint64_t eof_end = 0;
    std::uint32_t size = 0;
    std::map<std::uint32_t, std::uint64_t> offsets;
};

struct UpdateObject {
    std::uint32_t number = 0;
    std::string body;
    bool deleted = false;
    std::uint32_t generation = 0;
};

std::string xref_entry(std::uint64_t offset, std::uint32_t generation, char type) {
    char buffer[64];
    std::snprintf(buffer, sizeof(buffer), "%010llu %05u %c \n",
                  static_cast<unsigned long long>(offset), generation, type);
    return buffer;
}

std::string stream_body(std::string dictionary, std::string_view data) {
    if (!dictionary.empty()) {
        dictionary.push_back(' ');
    }
    dictionary += "/Length " + std::to_string(data.size());
    std::string body = "<<" + dictionary + ">>\nstream\n";
    body.append(data);
    body += "\nendstream";
    return body;
}

std::string bytes_from_hex(std::string_view hex) {
    std::string bytes;
    int high = -1;
    for (char c : hex) {
        if (c == ' ' || c == '\n' || c == '\r' || c == '\t') {
            continue;
        }
        int value = 0;
        if (c >= '0' && c <= '9') {
            value = c - '0';
        } else if (c >= 'a' && c <= 'f') {
            value = c - 'a' + 10;
        } else if (c >= 'A' && c <= 'F') {
            value = c - 'A' + 10;
        }
        if (high < 0) {
            high = value;
        } else {
            bytes.push_back(static_cast<char>((high << 4) | value));
            high = -1;
        }
    }
    return bytes;
}

std::string content_stream(std::string_view text) {
    return stream_body("", "BT /F1 12 Tf 72 720 Td (" + std::string(text) + ") Tj ET");
}

std::string append_xref_table(std::string& pdf,
                              const std::vector<std::pair<std::uint32_t,
                                                          std::pair<std::uint64_t, std::uint32_t>>>&
                                  entries,
                              std::uint32_t size, std::uint32_t root,
                              std::optional<std::uint64_t> previous,
                              std::string extra_trailer = {},
                              bool close_document = true) {
    const std::uint64_t xref_offset = pdf.size();
    pdf += "xref\n";
    std::size_t cursor = 0;
    while (cursor < entries.size()) {
        std::size_t end = cursor + 1;
        while (end < entries.size() &&
               entries[end].first == entries[end - 1].first + 1) {
            ++end;
        }
        pdf += std::to_string(entries[cursor].first) + " " +
               std::to_string(end - cursor) + "\n";
        for (std::size_t i = cursor; i < end; ++i) {
            const bool free = entries[i].second.first == 0 &&
                              entries[i].second.second != 0;
            pdf += xref_entry(entries[i].second.first, entries[i].second.second,
                              free ? 'f' : 'n');
        }
        cursor = end;
    }
    pdf += "trailer\n<< /Size " + std::to_string(size) + " /Root " +
           std::to_string(root) + " 0 R";
    if (previous.has_value()) {
        pdf += " /Prev " + std::to_string(*previous);
    }
    if (!extra_trailer.empty()) {
        pdf += " " + extra_trailer;
    }
    pdf += " >>\n";
    if (close_document) {
        pdf += "startxref\n" + std::to_string(xref_offset) + "\n%%EOF\n";
    }
    return std::to_string(xref_offset);
}

BuildResult build_base(bool flate_content = false) {
    BuildResult result;
    std::string pdf = "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n";
    auto add = [&](std::uint32_t number, const std::string& body) {
        result.offsets[number] = pdf.size();
        pdf += std::to_string(number) + " 0 obj\n" + body + "\nendobj\n";
    };
    add(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm 7 0 R "
           "/Names << /EmbeddedFiles 9 0 R >> >>");
    add(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>");
    add(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
           "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> "
           "/Annots [] >>");
    if (flate_content) {
        const std::string flate =
            bytes_from_hex("78da730a51d07733543034520849533037022203859014050db79cc"
                           "492548592d48a124d85902c05d71000cc950aad");
        add(4, stream_body("/Filter /FlateDecode", flate));
    } else {
        add(4, content_stream("Original invoice total 100"));
    }
    add(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>");
    add(6, "<< /Unused true >>");
    add(7, "<< /Fields [8 0 R] /DA (/Helv 0 Tf 0 g) >>");
    add(8, "<< /FT /Tx /T (amount) /V (100) /DV (100) >>");
    add(9, "<< /Names [(readme.txt) 10 0 R] >>");
    add(10, "<< /Type /Filespec /F (readme.txt) /UF (readme.txt) "
            "/EF << /F 11 0 R >> >>");
    add(11, stream_body("/Type /EmbeddedFile", "attachment body"));

    std::vector<std::pair<std::uint32_t, std::pair<std::uint64_t, std::uint32_t>>> entries;
    entries.emplace_back(0, std::make_pair(0ULL, 65535U));
    for (std::uint32_t number = 1; number <= 11; ++number) {
        auto found = result.offsets.find(number);
        if (found == result.offsets.end()) {
            entries.emplace_back(number, std::make_pair(0ULL, 1U));
        } else {
            entries.emplace_back(number, std::make_pair(found->second, 0U));
        }
    }
    result.xref_offset =
        std::stoull(append_xref_table(pdf, entries, 12, 1, std::nullopt));
    result.pdf = std::move(pdf);
    result.eof_end = result.pdf.size();
    result.size = 12;
    return result;
}

BuildResult append_update(const BuildResult& previous,
                          const std::vector<UpdateObject>& updates) {
    BuildResult result;
    result.pdf = previous.pdf;
    result.size = previous.size;
    result.offsets = previous.offsets;
    std::vector<std::pair<std::uint32_t, std::pair<std::uint64_t, std::uint32_t>>> entries;
    for (const UpdateObject& update : updates) {
        result.size = std::max(result.size, update.number + 1);
        if (update.deleted) {
            entries.emplace_back(update.number, std::make_pair(0ULL, update.generation));
            continue;
        }
        result.offsets[update.number] = result.pdf.size();
        result.pdf += std::to_string(update.number) + " " +
                      std::to_string(update.generation) + " obj\n" + update.body +
                      "\nendobj\n";
        entries.emplace_back(update.number,
                             std::make_pair(result.offsets[update.number],
                                            update.generation));
    }
    std::sort(entries.begin(), entries.end(),
              [](const auto& left, const auto& right) { return left.first < right.first; });
    result.xref_offset =
        std::stoull(append_xref_table(result.pdf, entries, result.size, 1,
                                      previous.xref_offset));
    result.eof_end = result.pdf.size();
    return result;
}

BuildResult append_object_stream_update(const BuildResult& previous,
                                        const std::string& page_body,
                                        std::string_view replacement_text) {
    BuildResult result;
    result.pdf = previous.pdf;
    result.size = std::max(previous.size, 22U);
    result.offsets = previous.offsets;

    const std::uint32_t content_number = 12;
    result.offsets[content_number] = result.pdf.size();
    result.pdf += "12 0 obj\n" + content_stream(replacement_text) + "\nendobj\n";

    const std::string object_stream_data = "3 0 " + page_body;
    const std::uint32_t object_stream_number = 20;
    result.offsets[object_stream_number] = result.pdf.size();
    result.pdf += std::to_string(object_stream_number) + " 0 obj\n" +
                  stream_body("/Type /ObjStm /N 1 /First 4", object_stream_data) +
                  "\nendobj\n";

    const std::uint32_t xref_stream_number = 21;
    const std::uint64_t xref_offset = result.pdf.size();
    result.offsets[xref_stream_number] = xref_offset;
    const std::uint64_t content_offset = result.offsets[content_number];
    const std::uint64_t object_stream_offset = result.offsets[object_stream_number];
    std::string entries;
    auto type2 = [&](std::uint32_t stream, std::uint32_t index) {
        entries.push_back(2);
        entries += std::string(4, '\0');
        entries.back() = static_cast<char>(stream & 0xff);
        entries += std::string(2, '\0');
        (void)index;
    };
    auto type1 = [&](std::uint64_t offset) {
        entries.push_back(1);
        for (int shift = 24; shift >= 0; shift -= 8) {
            entries.push_back(static_cast<char>((offset >> shift) & 0xff));
        }
        entries += std::string(2, '\0');
    };
    type2(object_stream_number, 0);
    type1(content_offset);
    type1(object_stream_offset);
    type1(xref_offset);
    const std::string xref_dictionary =
        "/Type /XRef /Size 22 /Root 1 0 R /Prev " +
        std::to_string(previous.xref_offset) +
        " /W [1 4 2] /Index [3 1 12 1 20 2]";
    result.pdf += std::to_string(xref_stream_number) + " 0 obj\n" +
                  stream_body(xref_dictionary, entries) + "\nendobj\n";
    result.pdf += "startxref\n" + std::to_string(xref_offset) + "\n%%EOF\n";
    result.xref_offset = xref_offset;
    result.eof_end = result.pdf.size();
    return result;
}

BuildResult build_xref_stream_base() {
    BuildResult result;
    std::string pdf = "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n";
    auto add = [&](std::uint32_t number, const std::string& body) {
        result.offsets[number] = pdf.size();
        pdf += std::to_string(number) + " 0 obj\n" + body + "\nendobj\n";
    };
    add(1, "<< /Type /Catalog /Pages 2 0 R >>");
    add(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>");
    add(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
           "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> /Annots [] >>");
    add(4, content_stream("Original xref stream text"));
    add(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>");
    const std::uint64_t xref_offset = pdf.size();
    result.offsets[6] = xref_offset;
    std::string entries;
    auto write_entry = [&](std::uint32_t type, std::uint64_t offset,
                           std::uint32_t generation) {
        entries.push_back(static_cast<char>(type));
        for (int shift = 24; shift >= 0; shift -= 8) {
            entries.push_back(static_cast<char>((offset >> shift) & 0xff));
        }
        entries.push_back(static_cast<char>((generation >> 8) & 0xff));
        entries.push_back(static_cast<char>(generation & 0xff));
    };
    write_entry(0, 0, 0);
    for (std::uint32_t number = 1; number <= 6; ++number) {
        write_entry(1, result.offsets[number], 0);
    }
    pdf += "6 0 obj\n" +
           stream_body("/Type /XRef /Size 7 /Root 1 0 R /W [1 4 2] "
                       "/Index [0 7]",
                       entries) +
           "\nendobj\n";
    pdf += "startxref\n" + std::to_string(xref_offset) + "\n%%EOF\n";
    result.pdf = std::move(pdf);
    result.xref_offset = xref_offset;
    result.eof_end = result.pdf.size();
    result.size = 7;
    return result;
}

BuildResult build_linearized_with_length(std::string length) {
    BuildResult result;
    std::string pdf = "%PDF-1.7\n%\xE2\xE3\xCF\xD3\n";
    auto add = [&](std::uint32_t number, const std::string& body) {
        result.offsets[number] = pdf.size();
        pdf += std::to_string(number) + " 0 obj\n" + body + "\nendobj\n";
    };
    add(12, "<< /Linearized 1 /L " + length + " /O 3 /E 500 /N 1 /T 1000 >>");
    add(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>");
    add(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
           "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> "
           "/Annots [] >>");
    add(4, content_stream("Linearized original text"));
    add(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>");
    add(6, "<< /Unused true >>");
    add(1, "<< /Type /Catalog /Pages 2 0 R /AcroForm 7 0 R "
           "/Names << /EmbeddedFiles 9 0 R >> >>");
    add(7, "<< /Fields [8 0 R] /DA (/Helv 0 Tf 0 g) >>");
    add(8, "<< /FT /Tx /T (amount) /V (100) /DV (100) >>");
    add(9, "<< /Names [(readme.txt) 10 0 R] >>");
    add(10, "<< /Type /Filespec /F (readme.txt) /UF (readme.txt) "
            "/EF << /F 11 0 R >> >>");
    add(11, stream_body("/Type /EmbeddedFile", "attachment body"));

    std::vector<std::pair<std::uint32_t, std::pair<std::uint64_t, std::uint32_t>>> entries;
    entries.emplace_back(0, std::make_pair(0ULL, 65535U));
    for (std::uint32_t number = 1; number <= 12; ++number) {
        auto found = result.offsets.find(number);
        entries.emplace_back(number,
                             std::make_pair(found == result.offsets.end() ? 0ULL
                                                                         : found->second,
                                            found == result.offsets.end() ? 1U : 0U));
    }
    const std::uint64_t first_xref = std::stoull(
        append_xref_table(pdf, entries, 13, 1, 0, {}, false));
    result.xref_offset =
        std::stoull(append_xref_table(pdf, entries, 13, 1, first_xref));
    result.pdf = std::move(pdf);
    result.eof_end = result.pdf.size();
    result.size = 13;
    return result;
}

BuildResult build_linearized() {
    BuildResult first = build_linearized_with_length("0000000000");
    char length[16];
    std::snprintf(length, sizeof(length), "%010llu",
                  static_cast<unsigned long long>(first.pdf.size()));
    return build_linearized_with_length(length);
}

const netsec::Change* find_change(const netsec::Assessment& assessment,
                                  netsec::ChangeKind kind) {
    for (const netsec::Change& change : assessment.changes) {
        if (change.kind == kind) {
            return &change;
        }
    }
    return nullptr;
}

const netsec::ObjectRevisionInfo* find_object(const netsec::Assessment& assessment,
                                              std::uint32_t number,
                                              std::optional<std::size_t> revision = {}) {
    for (const netsec::ObjectRevisionInfo& object : assessment.objects) {
        if (object.object_number == number &&
            (!revision.has_value() || object.revision == *revision)) {
            return &object;
        }
    }
    return nullptr;
}

void test_original_valid() {
    BuildResult base = build_base();
    netsec::Assessment assessment = netsec::assess(
        base.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(assessment.valid());
    CHECK(assessment.decision == netsec::Decision::Valid);
    CHECK(assessment.revisions.size() == 1);
    CHECK(assessment.signature_covers_original);
    CHECK(!assessment.has_incremental_updates);
    CHECK(assessment.revisions.front().kind == netsec::RevisionKind::Original);
}

void test_replacement_text() {
    BuildResult base = build_base();
    const std::string replacement =
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> /Annots [] >>";
    BuildResult update = append_update(
        base, {{3, replacement, false, 0},
               {12, content_stream("Tampered invoice total 999"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    CHECK(assessment.decision ==
          netsec::Decision::UnauthenticatedIncrementalUpdate);
    CHECK(assessment.revisions.size() == 2);
    CHECK(assessment.revisions[1].begin == base.eof_end ||
          assessment.revisions[1].begin <= base.eof_end);
    CHECK(assessment.signature_covers_original);
    const netsec::Change* change = find_change(assessment, netsec::ChangeKind::Text);
    CHECK(change != nullptr);
    if (change != nullptr) {
        CHECK(change->before.find("Original") != std::string::npos);
        CHECK(change->after.find("Tampered") != std::string::npos);
        CHECK(!change->covered_by_signature);
    }
    const netsec::ObjectRevisionInfo* page = find_object(assessment, 3, 1);
    CHECK(page != nullptr);
    if (page != nullptr) {
        CHECK(page->reused_object_number);
        CHECK(page->revision == 1);
    }
}

void test_page_replacement() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{2, "<< /Type /Pages /Kids [13 0 R] /Count 1 >>", false, 0},
               {13, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Replacement page"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    CHECK(find_change(assessment, netsec::ChangeKind::Page) != nullptr);
    CHECK(assessment.revisions.size() == 2);
}

void test_form_default() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{8, "<< /FT /Tx /T (amount) /V (100) /DV (999) >>", false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    const netsec::Change* change =
        find_change(assessment, netsec::ChangeKind::FormValue);
    CHECK(change != nullptr);
    if (change != nullptr) {
        CHECK(change->description.find("default") != std::string::npos);
        CHECK(change->before == "100");
        CHECK(change->after == "999");
    }
}

void test_hidden_annotation() {
    BuildResult base = build_base();
    const std::string page =
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> "
        "/Annots [12 0 R] >>";
    BuildResult update = append_update(
        base, {{3, page, false, 0},
               {12, "<< /Type /Annot /Subtype /Text /Rect [0 0 10 10] /F 2 "
                    "/Contents (hidden note) >>", false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    const netsec::Change* change =
        find_change(assessment, netsec::ChangeKind::Annotation);
    CHECK(change != nullptr);
    if (change != nullptr) {
        CHECK(change->hidden);
        CHECK(change->subtype == "Text");
    }
}

void test_pure_annotation_allowlist() {
    BuildResult base = build_base();
    const std::string page =
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> "
        "/Annots [12 0 R] >>";
    BuildResult update = append_update(
        base, {{3, page, false, 0},
               {12, "<< /Type /Annot /Subtype /Text /Rect [10 10 200 40] /F 4 "
                    "/NM (approval-note) /Contents (Approved for payment) >>",
                false, 0}});
    netsec::Policy policy = netsec::Policy::pure_annotations({"Text"});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end}, policy);
    CHECK(assessment.valid());
    const netsec::Change* change =
        find_change(assessment, netsec::ChangeKind::Annotation);
    CHECK(change != nullptr);
    if (change != nullptr) {
        CHECK(change->allowed);
        CHECK(change->after.find("Approved") != std::string::npos);
    }
    CHECK(assessment.visible_difference_summary.find("allowlisted") !=
          std::string::npos);
}

void test_countersigning() {
    BuildResult base = build_base();
    const std::string page =
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> "
        "/Annots [14 0 R] >>";
    const std::string acroform =
        "<< /Fields [8 0 R 12 0 R] /DA (/Helv 0 Tf 0 g) >>";
    const std::string field =
        "<< /FT /Sig /T (cosign) /V 13 0 R /Kids [14 0 R] >>";
    const std::string signature =
        "<< /Type /Sig /ByteRange [0 100 200 300] "
        "/Contents <0102030405060708> /SubFilter /adbe.pkcs7.detached >>";
    const std::string widget =
        "<< /Type /Annot /Subtype /Widget /Rect [300 40 500 100] /F 4 "
        "/T (cosign) /Parent 12 0 R /AP << /N 15 0 R >> >>";
    const std::string appearance = stream_body(
        "/Type /XObject /Subtype /Form /BBox [0 0 200 60] "
        "/Resources << /Font << /F1 16 0 R >> >>",
        "BT /F1 10 Tf 12 18 Td (Digitally signed) Tj ET");
    const std::string new_font =
        "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>";
    BuildResult update = append_update(
        base, {{3, page, false, 0},
               {7, acroform, false, 0},
               {12, field, false, 0},
               {13, signature, false, 0},
               {14, widget, false, 0},
               {15, appearance, false, 0},
               {16, new_font, false, 0}});
    netsec::Assessment assessment = netsec::assess(
        update.pdf, netsec::ByteRange{0, base.eof_end},
        netsec::Policy::countersigning());
    CHECK(assessment.valid());
    const netsec::Change* signature_change =
        find_change(assessment, netsec::ChangeKind::Signature);
    CHECK(signature_change != nullptr);
    if (signature_change != nullptr) {
        CHECK(signature_change->allowed);
    }
    CHECK(assessment.visible_difference_summary.find("signature") !=
          std::string::npos);
}

void test_full_coverage() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Signed replacement text"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, update.pdf.size()});
    CHECK(assessment.valid());
    CHECK(assessment.signature_covers_entire_document);
    CHECK(assessment.changes.size() >= 1);
    for (const netsec::Change& change : assessment.changes) {
        CHECK(change.covered_by_signature);
    }
}

void test_object_stream() {
    BuildResult base = build_base();
    const std::string page =
        "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
        "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> /Annots [] >>";
    BuildResult update =
        append_object_stream_update(base, page, "Object stream replacement text");
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    const netsec::Change* change = find_change(assessment, netsec::ChangeKind::Text);
    CHECK(change != nullptr);
    const netsec::ObjectRevisionInfo* page_info = find_object(assessment, 3, 1);
    CHECK(page_info != nullptr);
    if (page_info != nullptr) {
        CHECK(page_info->compressed);
        CHECK(page_info->object_stream == 20);
    }
}

void test_original_xref_stream() {
    BuildResult base = build_xref_stream_base();
    netsec::Assessment original =
        netsec::assess(base.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(original.valid());
    CHECK(original.revisions.size() == 1);
    CHECK(original.revisions.front().kind == netsec::RevisionKind::Original);

    BuildResult update = append_update(
        base, {{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Xref stream replacement"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    CHECK(assessment.revisions.size() == 2);
    CHECK(find_change(assessment, netsec::ChangeKind::Text) != nullptr);
}

void test_flate_content() {
    BuildResult base = build_base(true);
    BuildResult update = append_update(
        base, {{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Flate replacement text"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    const netsec::Change* change = find_change(assessment, netsec::ChangeKind::Text);
    CHECK(change != nullptr);
    if (change != nullptr) {
        CHECK(change->before.find("Flate text") != std::string::npos);
        CHECK(change->after.find("Flate replacement") != std::string::npos);
    }
}

void test_deletion_and_recreation() {
    BuildResult base = build_base();
    BuildResult deleted = append_update(
        base, {{2, "<< /Type /Pages /Kids [13 0 R] /Count 1 >>", false, 0},
               {13, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Deletion page"), false, 0},
               {3, "", true, 1}});
    BuildResult recreated = append_update(
        deleted, {{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                      "/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> "
                      "/Annots [] >>", false, 0}});
    netsec::Assessment assessment =
        netsec::assess(recreated.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    CHECK(assessment.revisions.size() == 3);
    const netsec::ObjectRevisionInfo* deleted_version =
        find_object(assessment, 3, 1);
    CHECK(deleted_version != nullptr);
    if (deleted_version != nullptr) {
        CHECK(deleted_version->deleted);
    }
    const netsec::ObjectRevisionInfo* recreated_version =
        find_object(assessment, 3, 2);
    CHECK(recreated_version != nullptr);
    if (recreated_version != nullptr) {
        CHECK(recreated_version->reused_object_number);
        CHECK(!recreated_version->deleted);
    }
}

void test_linearized() {
    BuildResult base = build_linearized();
    netsec::Assessment original =
        netsec::assess(base.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(original.valid());
    CHECK(original.revisions.front().linearized);
    CHECK(original.revisions.front().xref_sections >= 2);

    BuildResult update = append_update(
        base, {{2, "<< /Type /Pages /Kids [13 0 R] /Count 1 >>", false, 0},
               {13, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Linearized reordered page"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    CHECK(assessment.revisions.size() == 2);
    CHECK(assessment.revisions[1].kind == netsec::RevisionKind::Incremental);
    CHECK(find_change(assessment, netsec::ChangeKind::Page) != nullptr);
}

void test_attachment_change() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{11, stream_body("/Type /EmbeddedFile", "different attachment"), false,
                0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(!assessment.valid());
    CHECK(find_change(assessment, netsec::ChangeKind::EmbeddedFile) != nullptr);
}

void test_metadata_increment_valid() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{12, "<< /Producer (Updated producer) /ModDate (D:20261114000000Z) >>",
                false, 0}});
    netsec::Assessment assessment =
        netsec::assess(update.pdf, netsec::ByteRange{0, base.eof_end});
    CHECK(assessment.valid());
    CHECK(assessment.has_incremental_updates);
    CHECK(assessment.changes.empty());
}

void test_extract_signature_ranges() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{12, "<< /Type /Sig /ByteRange [0 100 200 300] "
                    "/Contents <0102030405060708> /SubFilter /adbe.pkcs7.detached >>",
                false, 0}});
    std::vector<netsec::ByteRange> ranges =
        netsec::extract_signature_byte_ranges(update.pdf);
    CHECK(ranges.size() >= 2);
    CHECK(ranges.front().offset == 0);
    CHECK(ranges.front().length == 100);
}

void test_no_signature_range() {
    BuildResult base = build_base();
    netsec::Assessment assessment = netsec::assess(base.pdf, netsec::Policy{});
    CHECK(assessment.decision == netsec::Decision::NoSignatureRange);
}

void test_original_only_helper() {
    BuildResult base = build_base();
    BuildResult update = append_update(
        base, {{3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "
                    "/Contents 12 0 R /Resources << /Font << /F1 5 0 R >> >> "
                    "/Annots [] >>", false, 0},
               {12, content_stream("Later replacement"), false, 0}});
    netsec::Assessment assessment =
        netsec::assess_original_revision_only(update.pdf);
    CHECK(!assessment.valid());
    CHECK(assessment.signature_covers_original);
}

}  // namespace

int main() {
    test_original_valid();
    test_replacement_text();
    test_page_replacement();
    test_form_default();
    test_hidden_annotation();
    test_pure_annotation_allowlist();
    test_countersigning();
    test_full_coverage();
    test_object_stream();
    test_original_xref_stream();
    test_flate_content();
    test_deletion_and_recreation();
    test_linearized();
    test_attachment_change();
    test_metadata_increment_valid();
    test_extract_signature_ranges();
    test_no_signature_range();
    test_original_only_helper();
    if (failures == 0) {
        std::printf("all tests passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d test checks failed\n", failures);
    return 1;
}
