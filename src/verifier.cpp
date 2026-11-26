#include "entitlements.h"
#include "machosec/machosec.h"

#include <algorithm>
#include <cstring>
#include <cstdio>
#include <fstream>
#include <limits>
#include <stdexcept>
#include <string_view>

namespace machosec {
namespace {

using detail::fail;

constexpr size_t MACH_HEADER_64_SIZE = 32;
constexpr size_t FAT_HEADER_SIZE = 8;
constexpr uint32_t FAT_MAX_SLICES = 64;
constexpr uint32_t MACHO_MAX_COMMANDS = 4096;

struct ParsedBlob {
    uint32_t type = 0;
    uint32_t offset = 0;
    ByteView view;
};

[[noreturn]] void fail_prefix(const std::string& context, const std::string& message) {
    fail(context + ": " + message);
}

uint64_t checked_add(uint64_t a, uint64_t b) {
    if (b > std::numeric_limits<uint64_t>::max() - a) {
        fail("integer overflow while computing range end");
    }
    return a + b;
}

size_t checked_size(uint64_t value, const std::string& message) {
    if (value > std::numeric_limits<size_t>::max()) {
        fail(message);
    }
    return static_cast<size_t>(value);
}

ByteView bounded_view(ByteView bytes, size_t offset, size_t size) {
    if (offset > bytes.size || size > bytes.size - offset) {
        fail("byte range is out of bounds");
    }
    return ByteView(bytes.data + offset, size);
}

uint32_t read_le32(ByteView bytes, size_t offset) {
    ByteView value = bounded_view(bytes, offset, 4);
    return uint32_t(value.data[0]) |
           (uint32_t(value.data[1]) << 8u) |
           (uint32_t(value.data[2]) << 16u) |
           (uint32_t(value.data[3]) << 24u);
}

uint64_t read_le64(ByteView bytes, size_t offset) {
    ByteView value = bounded_view(bytes, offset, 8);
    uint64_t result = 0;
    for (size_t i = 0; i < 8; ++i) {
        result |= uint64_t(value.data[i]) << (i * 8u);
    }
    return result;
}

uint32_t read_be32(ByteView bytes, size_t offset) {
    ByteView value = bounded_view(bytes, offset, 4);
    return (uint32_t(value.data[0]) << 24u) |
           (uint32_t(value.data[1]) << 16u) |
           (uint32_t(value.data[2]) << 8u) |
           uint32_t(value.data[3]);
}

uint64_t read_be64(ByteView bytes, size_t offset) {
    ByteView value = bounded_view(bytes, offset, 8);
    uint64_t result = 0;
    for (size_t i = 0; i < 8; ++i) {
        result = (result << 8u) | uint64_t(value.data[i]);
    }
    return result;
}

std::string read_fixed_cstring(ByteView bytes, size_t offset, size_t capacity) {
    bounded_view(bytes, offset, capacity);
    size_t length = 0;
    while (length < capacity && bytes.data[offset + length] != 0) {
        ++length;
    }
    if (length == capacity) {
        fail("fixed-length string is not NUL terminated");
    }
    return std::string(reinterpret_cast<const char*>(bytes.data + offset), length);
}

std::string read_command_cstring(ByteView macho,
                                 size_t command_offset,
                                 uint32_t command_size,
                                 uint32_t string_offset) {
    if (string_offset >= command_size) {
        fail("load command string offset is out of bounds");
    }
    const size_t absolute = command_offset + string_offset;
    const size_t capacity = size_t(command_size - string_offset);
    bounded_view(macho, absolute, capacity);
    size_t length = 0;
    while (length < capacity && macho.data[absolute + length] != 0) {
        ++length;
    }
    if (length == capacity) {
        fail("load command string is not NUL terminated");
    }
    return std::string(reinterpret_cast<const char*>(macho.data + absolute), length);
}

bool ranges_overlap(uint64_t a_offset, uint64_t a_size,
                    uint64_t b_offset, uint64_t b_size) {
    if (a_size == 0 || b_size == 0) return false;
    const uint64_t a_end = checked_add(a_offset, a_size);
    const uint64_t b_end = checked_add(b_offset, b_size);
    return a_offset < b_end && b_offset < a_end;
}

std::string architecture_name(uint32_t cpu_type, uint32_t cpu_subtype) {
    const uint32_t abi64 = 0x01000000u;
    const uint32_t base = cpu_type & ~abi64;
    if ((cpu_type & abi64) != 0 && base == 7) return "x86_64";
    if ((cpu_type & abi64) != 0 && base == 12) return "arm64";
    if (base == 7) return "i386";
    if (base == 12) return "arm";
    return "cpu-" + [&] {
        char buffer[32]{};
        std::snprintf(buffer, sizeof(buffer), "0x%x/0x%x", cpu_type, cpu_subtype);
        return std::string(buffer);
    }();
}

bool digest_equals(const Sha256Digest& expected, const uint8_t* actual) {
    uint8_t difference = 0;
    for (size_t i = 0; i < expected.size(); ++i) {
        difference |= uint8_t(expected[i] ^ actual[i]);
    }
    return difference == 0;
}

uint32_t minimum_code_directory_header_size(uint32_t version) {
    if (version >= 0x20600u) return 108;
    if (version >= 0x20500u) return 96;
    if (version >= 0x20400u) return 88;
    if (version >= 0x20300u) return 64;
    if (version >= 0x20200u) return 52;
    if (version >= 0x20100u) return 48;
    return 44;
}

const ParsedBlob* find_blob(const std::vector<ParsedBlob>& blobs, uint32_t type) {
    for (const ParsedBlob& blob : blobs) {
        if (blob.type == type) return &blob;
    }
    return nullptr;
}

void verify_requirements_blob(const ParsedBlob& blob) {
    if (read_be32(blob.view, 0) != CSMAGIC_REQUIREMENTS) {
        fail("LC_CODE_SIGNATURE requirements slot has wrong magic");
    }
    const uint32_t length = read_be32(blob.view, 4);
    if (length != blob.view.size) {
        fail("requirements blob length is inconsistent");
    }
    const uint32_t count = read_be32(blob.view, 8);
    const uint64_t table_end = checked_add(12, uint64_t(count) * 8u);
    if (table_end > blob.view.size) {
        fail("requirements table is out of bounds");
    }
    for (uint32_t i = 0; i < count; ++i) {
        const size_t entry = 12 + size_t(i) * 8;
        const uint32_t requirement_offset = read_be32(blob.view, entry + 4);
        if (requirement_offset + 8u > blob.view.size) {
            fail("requirement entry is out of bounds");
        }
        if (read_be32(blob.view, requirement_offset) != 0xFADE0C00u) {
            fail("requirement entry has wrong magic");
        }
        const uint32_t requirement_length = read_be32(blob.view, requirement_offset + 4);
        if (requirement_length < 8u ||
            uint64_t(requirement_offset) + requirement_length > blob.view.size) {
            fail("requirement entry length is invalid");
        }
    }
}

void verify_code_signature(ByteView macho,
                           uint64_t signature_offset_64,
                           uint64_t signature_size_64,
                           const VerifyOptions& options,
                           SliceReport& report) {
    const size_t signature_offset = checked_size(signature_offset_64,
                                                 "LC_CODE_SIGNATURE offset is too large");
    const size_t signature_size = checked_size(signature_size_64,
                                               "LC_CODE_SIGNATURE size is too large");
    if (signature_offset > macho.size ||
        signature_size > macho.size - signature_offset) {
        fail("LC_CODE_SIGNATURE range is out of bounds");
    }
    ByteView signature(macho.data + signature_offset, signature_size);

    if (signature.size < 12 || read_be32(signature, 0) != CSMAGIC_EMBEDDED_SIGNATURE) {
        fail("code signature is not a CSMAGIC_EMBEDDED_SIGNATURE SuperBlob");
    }

    const uint32_t super_length = read_be32(signature, 4);
    const uint32_t blob_count = read_be32(signature, 8);
    if (super_length < 12u || super_length > signature.size) {
        fail("code signature SuperBlob length is invalid");
    }
    if (super_length < signature.size) {
        if (!options.allow_zero_signature_padding) {
            fail("unsigned padding follows code signature SuperBlob");
        }
        for (size_t i = super_length; i < signature.size; ++i) {
            if (signature.data[i] != 0) {
                fail("unsigned non-zero bytes follow code signature SuperBlob");
            }
        }
    }

    if (blob_count == 0 || blob_count > 1024u) {
        fail("code signature SuperBlob entry count is invalid");
    }
    const uint64_t index_end = checked_add(12, uint64_t(blob_count) * 8u);
    if (index_end > super_length) {
        fail("code signature SuperBlob index is out of bounds");
    }

    std::vector<ParsedBlob> blobs;
    blobs.reserve(blob_count);
    for (uint32_t i = 0; i < blob_count; ++i) {
        const size_t index_offset = 12 + size_t(i) * 8;
        ParsedBlob entry;
        entry.type = read_be32(signature, index_offset);
        entry.offset = read_be32(signature, index_offset + 4);
        if (entry.offset < index_end || entry.offset + 8u > super_length) {
            fail("code signature blob offset is outside its SuperBlob");
        }
        for (const ParsedBlob& existing : blobs) {
            if (existing.type == entry.type) {
                fail("duplicate code signature blob type");
            }
        }
        const uint32_t blob_length = read_be32(signature, entry.offset + 4);
        if (blob_length < 8u ||
            uint64_t(entry.offset) + blob_length > super_length) {
            fail("code signature child blob length is invalid");
        }
        entry.view = bounded_view(signature, entry.offset, blob_length);
        blobs.push_back(entry);
    }

    for (size_t i = 0; i < blobs.size(); ++i) {
        for (size_t j = i + 1; j < blobs.size(); ++j) {
            if (ranges_overlap(blobs[i].offset, blobs[i].view.size,
                               blobs[j].offset, blobs[j].view.size)) {
                fail("code signature child blobs overlap");
            }
        }
    }

    const ParsedBlob* directory_blob = find_blob(blobs, 0);
    if (directory_blob == nullptr ||
        read_be32(directory_blob->view, 0) != CSMAGIC_CODEDIRECTORY) {
        fail("code signature has no CodeDirectory blob");
    }
    const ByteView directory = directory_blob->view;
    if (directory.size < 44) {
        fail("CodeDirectory is shorter than its fixed header");
    }

    const uint32_t version = read_be32(directory, 8);
    const uint32_t flags = read_be32(directory, 12);
    const uint32_t hash_offset = read_be32(directory, 16);
    const uint32_t identifier_offset = read_be32(directory, 20);
    const uint32_t special_slots = read_be32(directory, 24);
    const uint32_t code_slots = read_be32(directory, 28);
    const uint32_t code_limit = read_be32(directory, 32);
    const uint8_t hash_size = directory.data[36];
    const uint8_t hash_type = directory.data[37];
    const uint8_t page_shift = directory.data[39];

    report.code_directory_version = version;
    report.code_directory_flags = flags;
    report.code_slots = code_slots;
    report.special_slots = special_slots;
    report.page_size_shift = page_shift;

    if (directory.size < minimum_code_directory_header_size(version)) {
        fail("CodeDirectory version-specific fixed header is truncated");
    }

    if (hash_size != CS_SHA256_SIZE || hash_type != CS_HASHTYPE_SHA256) {
        fail("CodeDirectory must use 32-byte SHA-256 hashes");
    }
    if (page_shift < 8 || page_shift > 20) {
        fail("CodeDirectory page size exponent is invalid");
    }
    if (options.require_hardened_runtime && (flags & CS_RUNTIME) == 0) {
        fail("CodeDirectory lacks CS_RUNTIME hardened runtime flag");
    }
    if (identifier_offset != 0) {
        if (identifier_offset >= directory.size) {
            fail("CodeDirectory identifier offset is out of bounds");
        }
        (void)read_command_cstring(directory, 0,
                                   static_cast<uint32_t>(directory.size),
                                   identifier_offset);
    }

    const uint64_t special_hash_bytes = uint64_t(special_slots) * CS_SHA256_SIZE;
    if (special_hash_bytes > hash_offset) {
        fail("CodeDirectory special hash slots are out of bounds");
    }
    const uint64_t special_start = uint64_t(hash_offset) - special_hash_bytes;
    if (special_start < 44) {
        fail("CodeDirectory hash slots overlap its fixed header");
    }
    const uint64_t code_hash_end =
        checked_add(hash_offset, uint64_t(code_slots) * CS_SHA256_SIZE);
    if (code_hash_end != directory.size) {
        fail("CodeDirectory length does not exactly cover its hash slots");
    }

    if (code_limit != signature_offset_64) {
        fail("CodeDirectory codeLimit must end exactly at LC_CODE_SIGNATURE start");
    }
    if (version >= 0x20300u && directory.size >= 64) {
        const uint64_t code_limit_64 = read_be64(directory, 56);
        if (code_limit_64 != 0 && code_limit_64 != signature_offset_64) {
            fail("CodeDirectory codeLimit64 disagrees with signature offset");
        }
    }

    const uint64_t page_size = uint64_t(1) << page_shift;
    const uint64_t expected_code_slots =
        code_limit == 0 ? 0 : (uint64_t(code_limit) + page_size - 1) / page_size;
    if (uint64_t(code_slots) != expected_code_slots) {
        fail("CodeDirectory nCodeSlots does not match codeLimit/pageSize");
    }

    for (uint32_t slot = 0; slot < code_slots; ++slot) {
        const uint64_t page_start = uint64_t(slot) * page_size;
        const uint64_t page_end =
            std::min(checked_add(page_start, page_size), uint64_t(code_limit));
        const size_t page_start_size = checked_size(page_start, "page offset too large");
        const size_t page_length = checked_size(page_end - page_start,
                                                "page length too large");
        const Sha256Digest actual =
            Sha256::hash(bounded_view(macho, page_start_size, page_length));
        const size_t stored_offset =
            size_t(hash_offset) + size_t(slot) * CS_SHA256_SIZE;
        if (!digest_equals(actual, directory.data + stored_offset)) {
            fail("Mach-O code page SHA-256 mismatch at page " + std::to_string(slot));
        }
    }

    report.code_directory_hash = Sha256::hash(directory);

    for (uint32_t slot = 1; slot <= special_slots; ++slot) {
        const uint64_t stored_offset_64 =
            uint64_t(hash_offset) - uint64_t(slot) * CS_SHA256_SIZE;
        const size_t stored_offset = checked_size(stored_offset_64,
                                                  "special hash offset too large");
        const ParsedBlob* blob = find_blob(blobs, slot);
        if (blob == nullptr) {
            for (size_t i = 0; i < CS_SHA256_SIZE; ++i) {
                if (directory.data[stored_offset + i] != 0) {
                    fail("non-zero CodeDirectory special slot has no SuperBlob entry");
                }
            }
            continue;
        }

        const Sha256Digest actual = Sha256::hash(blob->view);
        if (!digest_equals(actual, directory.data + stored_offset)) {
            fail("CodeDirectory special slot hash mismatch for slot -" +
                 std::to_string(slot));
        }

        if (slot == CSSLOT_REQUIREMENTS) {
            verify_requirements_blob(*blob);
        }
    }

    const ParsedBlob* requirements = find_blob(blobs, CSSLOT_REQUIREMENTS);
    if (options.require_requirements_slot && requirements == nullptr) {
        fail("code signature is missing the -2 Requirements slot");
    }

    const ParsedBlob* xml = find_blob(blobs, CSSLOT_ENTITLEMENTS_XML);
    const ParsedBlob* der = find_blob(blobs, CSSLOT_ENTITLEMENTS_DER);
    if (options.require_xml_entitlements && xml == nullptr) {
        fail("code signature is missing the -5 XML Entitlements slot");
    }
    if (options.require_der_entitlements && der == nullptr) {
        fail("code signature is missing the -7 DER Entitlements slot");
    }

    if (xml != nullptr &&
        read_be32(xml->view, 0) != CSMAGIC_ENTITLEMENTS_XML) {
        fail("XML entitlements blob has wrong magic");
    }
    if (der != nullptr &&
        read_be32(der->view, 0) != CSMAGIC_ENTITLEMENTS_DER) {
        fail("DER entitlements blob has wrong magic");
    }

    if (options.require_entitlement_semantic_equality &&
        xml != nullptr && der != nullptr) {
        ByteView xml_payload(xml->view.data + 8, xml->view.size - 8);
        ByteView der_payload(der->view.data + 8, der->view.size - 8);
        detail::EntValue xml_value = detail::parse_xml_entitlements(xml_payload);
        detail::EntValue der_value = detail::parse_der_entitlements(der_payload);
        std::string difference;
        if (!detail::semantic_entitlements_equal(
                xml_value, der_value,
                options.compare_only_sensitive_entitlements, &difference)) {
            fail(difference.empty() ? "XML/DER entitlement mismatch" : difference);
        }
    }

    const ParsedBlob* pkcs7 = find_blob(blobs, CSSLOT_SIGNATURE);
    if (options.require_pkcs7_signature_slot && pkcs7 == nullptr) {
        fail("code signature is missing the PKCS#7 signature slot");
    }
    const bool has_pkcs7_verifier =
        options.pkcs7_verifier != nullptr ||
        static_cast<bool>(options.pkcs7_callback);
    if (options.require_pkcs7_verifier && !has_pkcs7_verifier) {
        fail("PKCS#7 verification requires an IPkcs7Verifier implementation");
    }
    if (pkcs7 != nullptr) {
        if (read_be32(pkcs7->view, 0) != CSMAGIC_BLOBWRAPPER) {
            fail("PKCS#7 signature blob has wrong magic");
        }
        report.pkcs7_slot_present = true;
        if (options.pkcs7_verifier != nullptr ||
            static_cast<bool>(options.pkcs7_callback)) {
            Pkcs7Context context;
            context.super_blob_slot = CSSLOT_SIGNATURE;
            context.cms_bytes = ByteView(pkcs7->view.data + 8,
                                         pkcs7->view.size - 8);
            context.code_directory = directory;
            context.code_directory_hash = report.code_directory_hash;
            std::string pkcs7_error;
            bool verified = true;
            if (options.pkcs7_verifier != nullptr) {
                verified = options.pkcs7_verifier->verify_pkcs7(context, pkcs7_error);
            } else {
                verified = options.pkcs7_callback(context, pkcs7_error);
            }
            report.pkcs7_verifier_called = true;
            report.pkcs7_verified = verified;
            if (!verified) {
                fail(pkcs7_error.empty() ? "PKCS#7 callback rejected signature"
                                         : pkcs7_error);
            }
        }
    }
}

void verify_macho(ByteView bytes, const VerifyOptions& options, SliceReport& report) {
    if (bytes.size < MACH_HEADER_64_SIZE) {
        fail("Mach-O slice is shorter than mach_header_64");
    }
    if (read_le32(bytes, 0) != MH_MAGIC_64) {
        fail("only little-endian 64-bit Mach-O magic 0xFEEDFACF is supported");
    }

    report.cpu_type = read_le32(bytes, 4);
    report.cpu_subtype = read_le32(bytes, 8);
    report.architecture = architecture_name(report.cpu_type, report.cpu_subtype);

    const uint32_t command_count = read_le32(bytes, 16);
    const uint32_t sizeof_commands = read_le32(bytes, 20);
    if (command_count > MACHO_MAX_COMMANDS) {
        fail("Mach-O load command count is implausibly large");
    }
    if (sizeof_commands < 8u * command_count) {
        fail("Mach-O sizeofcmds is too small for ncmds");
    }
    const uint64_t commands_end_64 =
        checked_add(MACH_HEADER_64_SIZE, sizeof_commands);
    if (commands_end_64 > bytes.size) {
        fail("Mach-O load command table extends beyond slice");
    }
    if ((sizeof_commands & 7u) != 0) {
        fail("Mach-O load commands are not 8-byte aligned");
    }
    const size_t commands_end = checked_size(commands_end_64, "commands end too large");

    size_t cursor = MACH_HEADER_64_SIZE;
    bool has_signature_command = false;

    for (uint32_t command_index = 0; command_index < command_count; ++command_index) {
        if (cursor + 8 > commands_end) {
            fail("Mach-O load command header is out of bounds");
        }
        const uint32_t cmd = read_le32(bytes, cursor);
        const uint32_t cmd_size = read_le32(bytes, cursor + 4);
        if (cmd_size < 8u || uint64_t(cursor) + cmd_size > commands_end) {
            fail("Mach-O load command size is invalid");
        }

        LoadCommandInfo command_info;
        command_info.cmd = cmd;
        command_info.cmd_size = cmd_size;
        command_info.file_offset = cursor;

        if (cmd == LC_SEGMENT_64) {
            if (cmd_size < 72u) {
                fail("LC_SEGMENT_64 command is shorter than segment_command_64");
            }
            SegmentInfo segment;
            segment.name = read_fixed_cstring(bytes, cursor + 8, 16);
            segment.vmaddr = read_le64(bytes, cursor + 24);
            segment.vmsize = read_le64(bytes, cursor + 32);
            segment.fileoff = read_le64(bytes, cursor + 40);
            segment.filesize = read_le64(bytes, cursor + 48);
            segment.maxprot = read_le32(bytes, cursor + 56);
            segment.initprot = read_le32(bytes, cursor + 60);
            segment.section_count = read_le32(bytes, cursor + 64);

            const uint64_t file_end = checked_add(segment.fileoff, segment.filesize);
            if (file_end > bytes.size) {
                fail_prefix(segment.name, "segment file range is out of bounds or wraps");
            }
            const uint64_t vm_end = checked_add(segment.vmaddr, segment.vmsize);
            if (vm_end < segment.vmaddr && segment.vmsize != 0) {
                fail_prefix(segment.name, "segment virtual range wraps");
            }
            if ((segment.initprot & VM_PROT_WRITE) != 0 &&
                (segment.initprot & VM_PROT_EXECUTE) != 0) {
                fail_prefix(segment.name, "segment is simultaneously writable and executable");
            }
            if ((segment.maxprot & VM_PROT_WRITE) != 0 &&
                (segment.maxprot & VM_PROT_EXECUTE) != 0) {
                fail_prefix(segment.name, "segment maximum protection allows W^X violation");
            }

            const uint64_t section_bytes = uint64_t(segment.section_count) * 80u;
            if (checked_add(72, section_bytes) > cmd_size) {
                fail_prefix(segment.name, "section_64 array extends beyond load command");
            }

            command_info.description = segment.name;
            report.segments.push_back(std::move(segment));
        } else if (cmd == LC_CODE_SIGNATURE) {
            if (cmd_size != 16u) {
                fail("LC_CODE_SIGNATURE has invalid linkedit_data_command size");
            }
            if (has_signature_command) {
                fail("Mach-O contains duplicate LC_CODE_SIGNATURE commands");
            }
            has_signature_command = true;
            report.code_signature_offset = read_le32(bytes, cursor + 8);
            report.code_signature_size = read_le32(bytes, cursor + 12);
            report.has_code_signature = true;
            if (checked_add(report.code_signature_offset,
                            report.code_signature_size) > bytes.size) {
                fail("LC_CODE_SIGNATURE data extends beyond slice");
            }
            command_info.description = "LC_CODE_SIGNATURE";
        } else if (cmd == LC_LOAD_DYLIB) {
            if (cmd_size < 24u) {
                fail("LC_LOAD_DYLIB command is too short");
            }
            const uint32_t name_offset = read_le32(bytes, cursor + 8);
            if (name_offset < 24u) {
                fail("LC_LOAD_DYLIB name offset precedes dylib_command header");
            }
            std::string dylib =
                read_command_cstring(bytes, cursor, cmd_size, name_offset);
            report.dylibs.push_back(dylib);
            command_info.description = dylib;
        } else if (cmd == LC_RPATH) {
            if (cmd_size < 12u) {
                fail("LC_RPATH command is too short");
            }
            const uint32_t path_offset = read_le32(bytes, cursor + 8);
            if (path_offset < 12u) {
                fail("LC_RPATH path offset precedes rpath_command header");
            }
            std::string rpath =
                read_command_cstring(bytes, cursor, cmd_size, path_offset);
            if (options.reject_relative_rpaths && is_dangerous_rpath(rpath)) {
                fail("dangerous relative LC_RPATH rejected: " + rpath);
            }
            report.rpaths.push_back(rpath);
            command_info.description = rpath;
        } else {
            command_info.description = "load-command-0x" + [&] {
                char buffer[16]{};
                std::snprintf(buffer, sizeof(buffer), "%x", cmd);
                return std::string(buffer);
            }();
        }

        report.load_commands.push_back(std::move(command_info));
        cursor += cmd_size;
    }

    if (cursor != commands_end) {
        fail("Mach-O load command table was not consumed exactly");
    }

    for (size_t i = 0; i < report.segments.size(); ++i) {
        for (size_t j = i + 1; j < report.segments.size(); ++j) {
            const SegmentInfo& a = report.segments[i];
            const SegmentInfo& b = report.segments[j];
            if (ranges_overlap(a.vmaddr, a.vmsize, b.vmaddr, b.vmsize)) {
                fail("Mach-O segment virtual addresses overlap: " + a.name + "/" + b.name);
            }
            if (options.reject_file_segment_overlap &&
                ranges_overlap(a.fileoff, a.filesize, b.fileoff, b.filesize)) {
                fail("Mach-O segment file ranges overlap: " + a.name + "/" + b.name);
            }
        }
    }

    if (!has_signature_command) {
        fail("Mach-O slice has no LC_CODE_SIGNATURE and is unsigned");
    }

    const uint64_t signature_end =
        checked_add(report.code_signature_offset, report.code_signature_size);
    if (signature_end != bytes.size) {
        fail("unsigned trailing bytes follow LC_CODE_SIGNATURE range");
    }

    verify_code_signature(bytes,
                          report.code_signature_offset,
                          report.code_signature_size,
                          options,
                          report);
}

void verify_fat(ByteView bytes, const VerifyOptions& options, Report& report) {
    if (bytes.size < FAT_HEADER_SIZE) {
        fail("Fat header is truncated");
    }
    const uint32_t magic = read_be32(bytes, 0);
    if (magic != FAT_MAGIC && magic != FAT_MAGIC_64) {
        fail("not a supported Fat Binary magic");
    }
    report.is_fat = true;
    report.is_fat64 = magic == FAT_MAGIC_64;

    const uint32_t slice_count = read_be32(bytes, 4);
    if (slice_count == 0 || slice_count > FAT_MAX_SLICES) {
        fail("Fat slice count is invalid");
    }
    const size_t entry_size = report.is_fat64 ? 32 : 20;
    const uint64_t table_end =
        checked_add(FAT_HEADER_SIZE, uint64_t(slice_count) * entry_size);
    if (table_end > bytes.size) {
        fail("Fat architecture table extends beyond file");
    }

    struct FatRange {
        uint64_t offset;
        uint64_t size;
    };
    std::vector<FatRange> ranges;
    ranges.reserve(slice_count);
    uint64_t maximum_end = 0;

    for (uint32_t i = 0; i < slice_count; ++i) {
        const size_t entry = FAT_HEADER_SIZE + size_t(i) * entry_size;
        SliceReport slice_report;
        slice_report.cpu_type = read_be32(bytes, entry);
        slice_report.cpu_subtype = read_be32(bytes, entry + 4);

        uint64_t offset = 0;
        uint64_t size = 0;
        uint32_t alignment_shift = 0;
        if (report.is_fat64) {
            offset = read_be64(bytes, entry + 8);
            size = read_be64(bytes, entry + 16);
            alignment_shift = read_be32(bytes, entry + 24);
            const uint32_t reserved = read_be32(bytes, entry + 28);
            if (reserved != 0) fail("fat_arch_64 reserved field is non-zero");
        } else {
            offset = read_be32(bytes, entry + 8);
            size = read_be32(bytes, entry + 12);
            alignment_shift = read_be32(bytes, entry + 16);
        }

        if (size < MACH_HEADER_64_SIZE) {
            fail("Fat slice is too small to contain mach_header_64");
        }
        if (offset < table_end) {
            fail("Fat slice overlaps the Fat header or architecture table");
        }
        if (alignment_shift > 30) {
            fail("Fat slice alignment exponent is invalid");
        }
        const uint64_t alignment = uint64_t(1) << alignment_shift;
        if (offset % alignment != 0) {
            fail("Fat slice offset violates its declared alignment");
        }
        const uint64_t slice_end = checked_add(offset, size);
        if (slice_end > bytes.size) {
            fail("Fat slice extends beyond file");
        }
        for (const FatRange& range : ranges) {
            if (ranges_overlap(offset, size, range.offset, range.size)) {
                fail("Fat slices overlap");
            }
        }
        ranges.push_back({offset, size});
        maximum_end = std::max(maximum_end, slice_end);

        slice_report.fat_offset = offset;
        slice_report.fat_size = size;
        slice_report.architecture =
            architecture_name(slice_report.cpu_type, slice_report.cpu_subtype);
        const size_t offset_size = checked_size(offset, "Fat offset too large");
        const size_t slice_size = checked_size(size, "Fat size too large");
        verify_macho(ByteView(bytes.data + offset_size, slice_size),
                     options, slice_report);
        report.slices.push_back(std::move(slice_report));
    }

    if (maximum_end != bytes.size) {
        fail("unsigned trailing bytes follow the last Fat slice");
    }
}

}  // namespace

bool is_dangerous_rpath(std::string_view rpath) {
    if (rpath.empty()) return true;
    if (rpath[0] != '/') return true;

    size_t begin = 0;
    while (begin <= rpath.size()) {
        size_t end = rpath.find('/', begin);
        std::string_view component =
            end == std::string_view::npos
                ? rpath.substr(begin)
                : rpath.substr(begin, end - begin);
        if (component == "..") return true;
        if (end == std::string_view::npos) break;
        begin = end + 1;
    }
    return false;
}

bool verify_bytes(ByteView bytes,
                  const VerifyOptions& options,
                  Report* report,
                  std::string* error) {
    try {
        Report local_report;
        if (bytes.data == nullptr || bytes.size < 4) {
            fail("input is smaller than a Mach-O/Fat magic");
        }
        const uint32_t fat_magic = read_be32(bytes, 0);
        if (fat_magic == FAT_MAGIC || fat_magic == FAT_MAGIC_64) {
            verify_fat(bytes, options, local_report);
        } else {
            SliceReport slice;
            verify_macho(bytes, options, slice);
            local_report.slices.push_back(std::move(slice));
        }
        if (report != nullptr) *report = std::move(local_report);
        if (error != nullptr) error->clear();
        return true;
    } catch (const std::exception& exception) {
        if (error != nullptr) *error = exception.what();
        return false;
    }
}

bool verify_file(const std::string& path,
                 const VerifyOptions& options,
                 Report* report,
                 std::string* error) {
    std::ifstream file(path, std::ios::binary);
    if (!file) {
        if (error != nullptr) *error = "cannot open file: " + path;
        return false;
    }
    std::vector<uint8_t> bytes((std::istreambuf_iterator<char>(file)),
                               std::istreambuf_iterator<char>());
    if (!file && !file.eof()) {
        if (error != nullptr) *error = "cannot read file: " + path;
        return false;
    }
    return verify_bytes(ByteView(bytes), options, report, error);
}

}  // namespace machosec
