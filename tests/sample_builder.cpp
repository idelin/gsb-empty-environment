#include "sample_builder.h"

#include "machosec/machosec.h"

#include <algorithm>
#include <stdexcept>

namespace machosec::sample {
namespace {

constexpr size_t kMachHeader64Size = 32;

void put_le32(std::vector<uint8_t>& out, size_t offset, uint32_t value) {
    out[offset] = static_cast<uint8_t>(value);
    out[offset + 1] = static_cast<uint8_t>(value >> 8u);
    out[offset + 2] = static_cast<uint8_t>(value >> 16u);
    out[offset + 3] = static_cast<uint8_t>(value >> 24u);
}

void put_le64(std::vector<uint8_t>& out, size_t offset, uint64_t value) {
    for (size_t i = 0; i < 8; ++i) {
        out[offset + i] = static_cast<uint8_t>(value >> (i * 8u));
    }
}

void append_le32(std::vector<uint8_t>& out, uint32_t value) {
    const size_t offset = out.size();
    out.resize(offset + 4);
    put_le32(out, offset, value);
}

void append_le64(std::vector<uint8_t>& out, uint64_t value) {
    const size_t offset = out.size();
    out.resize(offset + 8);
    put_le64(out, offset, value);
}

void put_be32(std::vector<uint8_t>& out, size_t offset, uint32_t value) {
    out[offset] = static_cast<uint8_t>(value >> 24u);
    out[offset + 1] = static_cast<uint8_t>(value >> 16u);
    out[offset + 2] = static_cast<uint8_t>(value >> 8u);
    out[offset + 3] = static_cast<uint8_t>(value);
}

void put_be64(std::vector<uint8_t>& out, size_t offset, uint64_t value) {
    for (size_t i = 0; i < 8; ++i) {
        out[offset + i] = static_cast<uint8_t>(value >> ((7 - i) * 8u));
    }
}

void append_be32(std::vector<uint8_t>& out, uint32_t value) {
    const size_t offset = out.size();
    out.resize(offset + 4);
    put_be32(out, offset, value);
}

void append_fixed(std::vector<uint8_t>& out, const std::string& value, size_t width) {
    size_t written = 0;
    while (written < width) {
        if (written < value.size()) {
            out.push_back(static_cast<uint8_t>(value[written]));
        } else {
            out.push_back(0);
        }
        ++written;
    }
}

uint32_t align8(uint32_t value) {
    return (value + 7u) & ~7u;
}

std::string xml_escape(const std::string& value) {
    std::string result;
    for (char ch : value) {
        if (ch == '&') result += "&amp;";
        else if (ch == '<') result += "&lt;";
        else if (ch == '>') result += "&gt;";
        else if (ch == '"') result += "&quot;";
        else result.push_back(ch);
    }
    return result;
}

void append_der_length(std::vector<uint8_t>& out, size_t length) {
    if (length < 0x80) {
        out.push_back(static_cast<uint8_t>(length));
        return;
    }
    std::vector<uint8_t> bytes;
    size_t value = length;
    while (value != 0) {
        bytes.push_back(static_cast<uint8_t>(value & 0xffu));
        value >>= 8u;
    }
    out.push_back(static_cast<uint8_t>(0x80u | bytes.size()));
    for (auto it = bytes.rbegin(); it != bytes.rend(); ++it) {
        out.push_back(*it);
    }
}

std::vector<uint8_t> der_tlv(uint8_t tag, const std::vector<uint8_t>& content) {
    std::vector<uint8_t> result;
    result.push_back(tag);
    append_der_length(result, content.size());
    result.insert(result.end(), content.begin(), content.end());
    return result;
}

std::vector<uint8_t> der_boolean(bool value) {
    return der_tlv(0x01, {static_cast<uint8_t>(value ? 0xff : 0x00)});
}

std::vector<uint8_t> der_integer(int64_t value) {
    uint64_t raw = static_cast<uint64_t>(value);
    std::vector<uint8_t> bytes;
    for (int shift = 56; shift >= 0; shift -= 8) {
        bytes.push_back(static_cast<uint8_t>((raw >> shift) & 0xffu));
    }
    while (bytes.size() > 1 && bytes[0] == 0 && (bytes[1] & 0x80u) == 0) {
        bytes.erase(bytes.begin());
    }
    while (bytes.size() > 1 && bytes[0] == 0xff && (bytes[1] & 0x80u) != 0) {
        bytes.erase(bytes.begin());
    }
    return der_tlv(0x02, bytes);
}

std::vector<uint8_t> der_string(const std::string& value) {
    std::vector<uint8_t> content(value.begin(), value.end());
    return der_tlv(0x0c, content);
}

std::vector<uint8_t> blob(uint32_t magic, const std::vector<uint8_t>& payload) {
    std::vector<uint8_t> result;
    append_be32(result, magic);
    append_be32(result, static_cast<uint32_t>(8u + payload.size()));
    result.insert(result.end(), payload.begin(), payload.end());
    return result;
}

struct LoadCommands {
    std::vector<uint8_t> bytes;
    uint32_t count = 0;
};

void append_text_segment(LoadCommands& commands, const MachOConfig& config) {
    std::vector<uint8_t> command;
    append_le32(command, LC_SEGMENT_64);
    append_le32(command, 152);
    append_fixed(command, "__TEXT", 16);
    append_le64(command, 0x100000000ULL);
    append_le64(command, 0x1000ULL);
    append_le64(command, 0);
    append_le64(command, kSignatureOffset);
    const uint32_t protection = config.wx_segment ? 7u : 5u;
    append_le32(command, protection);
    append_le32(command, protection);
    append_le32(command, 1);
    append_le32(command, 0);

    append_fixed(command, "__text", 16);
    append_fixed(command, "__TEXT", 16);
    append_le64(command, 0x100000000ULL + kTextOffset);
    append_le64(command, 16);
    append_le32(command, static_cast<uint32_t>(kTextOffset));
    append_le32(command, 0);
    append_le32(command, 0);
    append_le32(command, 0);
    append_le32(command, 0);
    append_le32(command, 0);
    append_le32(command, 0);
    append_le32(command, 0);

    if (command.size() != 152) throw std::runtime_error("bad section command size");
    commands.bytes.insert(commands.bytes.end(), command.begin(), command.end());
    ++commands.count;
}

void append_linkedit_segment(LoadCommands& commands,
                             const MachOConfig& config,
                             uint32_t signature_size) {
    std::vector<uint8_t> command;
    append_le32(command, LC_SEGMENT_64);
    append_le32(command, 72);
    append_fixed(command, "__LINKEDIT", 16);
    append_le64(command, config.overlapping_vm_segments ? 0x100000800ULL
                                                        : 0x100004000ULL);
    append_le64(command, 0x1000ULL);
    append_le64(command, kSignatureOffset);
    append_le64(command, config.overflowing_file_range
                             ? std::numeric_limits<uint64_t>::max()
                             : signature_size);
    append_le32(command, VM_PROT_READ);
    append_le32(command, VM_PROT_READ);
    append_le32(command, 0);
    append_le32(command, 0);
    if (command.size() != 72) throw std::runtime_error("bad segment command size");
    commands.bytes.insert(commands.bytes.end(), command.begin(), command.end());
    ++commands.count;
}

void append_code_signature(LoadCommands& commands, uint32_t signature_size) {
    append_le32(commands.bytes, LC_CODE_SIGNATURE);
    append_le32(commands.bytes, 16);
    append_le32(commands.bytes, static_cast<uint32_t>(kSignatureOffset));
    append_le32(commands.bytes, signature_size);
    ++commands.count;
}

void append_dylib(LoadCommands& commands, const std::string& path) {
    if (path.empty()) return;
    const uint32_t size = align8(static_cast<uint32_t>(24 + path.size() + 1));
    append_le32(commands.bytes, LC_LOAD_DYLIB);
    append_le32(commands.bytes, size);
    append_le32(commands.bytes, 24);
    append_le32(commands.bytes, 2);
    append_le32(commands.bytes, 0x10000);
    append_le32(commands.bytes, 0x10000);
    commands.bytes.insert(commands.bytes.end(), path.begin(), path.end());
    commands.bytes.push_back(0);
    while (commands.bytes.size() % 8 != 0) commands.bytes.push_back(0);
    ++commands.count;
}

void append_rpath(LoadCommands& commands, const std::string& path) {
    if (path.empty()) return;
    const uint32_t size = align8(static_cast<uint32_t>(12 + path.size() + 1));
    append_le32(commands.bytes, LC_RPATH);
    append_le32(commands.bytes, size);
    append_le32(commands.bytes, 12);
    commands.bytes.insert(commands.bytes.end(), path.begin(), path.end());
    commands.bytes.push_back(0);
    while (commands.bytes.size() % 8 != 0) commands.bytes.push_back(0);
    ++commands.count;
}

}  // namespace

std::vector<Entitlement> default_entitlements(bool jit) {
    return {
        Entitlement::make_bool("com.apple.security.cs.allow-jit", jit),
        Entitlement::make_bool("com.apple.security.cs.disable-library-validation", true),
        Entitlement::make_string("com.example.diagnostic-scope", "machosec-sample"),
        Entitlement::make_integer("com.example.retry-count", 3),
    };
}

std::string make_xml_entitlements(const std::vector<Entitlement>& entitlements) {
    std::string xml =
        "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n"
        "<plist version=\"1.0\"><dict>";
    for (const Entitlement& entitlement : entitlements) {
        xml += "<key>" + xml_escape(entitlement.key) + "</key>";
        if (entitlement.type == Entitlement::Type::Boolean) {
            xml += entitlement.boolean ? "<true/>" : "<false/>";
        } else if (entitlement.type == Entitlement::Type::Integer) {
            xml += "<integer>" + std::to_string(entitlement.integer) + "</integer>";
        } else {
            xml += "<string>" + xml_escape(entitlement.string) + "</string>";
        }
    }
    xml += "</dict></plist>";
    return xml;
}

std::vector<uint8_t> make_der_entitlements(
    const std::vector<Entitlement>& entitlements) {
    std::vector<uint8_t> dictionary_content;
    for (const Entitlement& entitlement : entitlements) {
        std::vector<uint8_t> pair;
        std::vector<uint8_t> key = der_string(entitlement.key);
        pair.insert(pair.end(), key.begin(), key.end());

        std::vector<uint8_t> value;
        if (entitlement.type == Entitlement::Type::Boolean) {
            value = der_boolean(entitlement.boolean);
        } else if (entitlement.type == Entitlement::Type::Integer) {
            value = der_integer(entitlement.integer);
        } else {
            value = der_string(entitlement.string);
        }
        pair.insert(pair.end(), value.begin(), value.end());
        std::vector<uint8_t> sequence = der_tlv(0x30, pair);
        dictionary_content.insert(dictionary_content.end(), sequence.begin(),
                                 sequence.end());
    }

    std::vector<uint8_t> inner_dictionary = der_tlv(0xb0, dictionary_content);
    std::vector<uint8_t> version = der_integer(1);
    std::vector<uint8_t> root_content;
    root_content.insert(root_content.end(), version.begin(), version.end());
    root_content.insert(root_content.end(), inner_dictionary.begin(),
                       inner_dictionary.end());
    return der_tlv(0x70, root_content);
}

std::vector<uint8_t> build_macho(const MachOConfig& config) {
    const std::vector<Entitlement> entitlements = default_entitlements(true);
    const std::string xml = config.use_custom_entitlements
                                ? config.xml_entitlements
                                : make_xml_entitlements(entitlements);
    const std::vector<uint8_t> der =
        config.use_custom_entitlements
            ? config.der_entitlements
            : make_der_entitlements(entitlements);

    const std::vector<uint8_t> xml_blob =
        blob(CSMAGIC_ENTITLEMENTS_XML,
             std::vector<uint8_t>(xml.begin(), xml.end()));
    const std::vector<uint8_t> der_blob = blob(CSMAGIC_ENTITLEMENTS_DER, der);
    const std::vector<uint8_t> requirements_blob = {
        0xfa, 0xde, 0x0c, 0x01,
        0x00, 0x00, 0x00, 0x0c,
        0x00, 0x00, 0x00, 0x00,
    };
    const std::string pkcs7_token = "MOCK-PKCS7-EMBEDDED-TOKEN-v1";
    const std::vector<uint8_t> pkcs7_payload(pkcs7_token.begin(), pkcs7_token.end());
    const std::vector<uint8_t> pkcs7_blob = blob(CSMAGIC_BLOBWRAPPER, pkcs7_payload);

    constexpr size_t kIndexArea = 12 + 5 * 8;
    constexpr size_t kCdSize = 384;
    const size_t signature_size = kIndexArea + kCdSize + requirements_blob.size() +
                                  xml_blob.size() + der_blob.size() +
                                  pkcs7_blob.size();

    LoadCommands commands;
    append_text_segment(commands, config);
    append_linkedit_segment(
        commands, config,
        config.include_code_signature ? static_cast<uint32_t>(signature_size) : 0);
    if (config.include_code_signature) {
        append_code_signature(commands, static_cast<uint32_t>(signature_size));
    }
    append_dylib(commands, config.dylib);
    append_rpath(commands, config.rpath);

    if (kMachHeader64Size + commands.bytes.size() > kTextOffset) {
        throw std::runtime_error("sample load commands overlap __text sample");
    }

    std::vector<uint8_t> macho(kSignatureOffset, 0);
    put_le32(macho, 0, MH_MAGIC_64);
    put_le32(macho, 4, config.cpu_type);
    put_le32(macho, 8, config.cpu_subtype);
    put_le32(macho, 12, 2);
    put_le32(macho, 16, commands.count);
    put_le32(macho, 20, static_cast<uint32_t>(commands.bytes.size()));
    put_le32(macho, 24, 0);
    put_le32(macho, 28, 0);
    std::copy(commands.bytes.begin(), commands.bytes.end(),
              macho.begin() + static_cast<std::ptrdiff_t>(kMachHeader64Size));

    for (size_t i = 0; i < 16; ++i) {
        macho[kTextOffset + i] = static_cast<uint8_t>(0xa0u + i);
    }

    const Sha256Digest code_hash = Sha256::hash(macho);

    std::vector<uint8_t> directory(kCdSize, 0);
    put_be32(directory, 0, CSMAGIC_CODEDIRECTORY);
    put_be32(directory, 4, static_cast<uint32_t>(kCdSize));
    put_be32(directory, 8, 0x00020500u);
    put_be32(directory, 12, config.hardened_runtime ? (CS_RUNTIME | 0x2u) : 0x2u);
    put_be32(directory, 16, 352);
    put_be32(directory, 20, 96);
    put_be32(directory, 24, 7);
    put_be32(directory, 28, 1);
    put_be32(directory, 32, static_cast<uint32_t>(kSignatureOffset));
    directory[36] = CS_SHA256_SIZE;
    directory[37] = CS_HASHTYPE_SHA256;
    directory[38] = 0;
    directory[39] = 12;
    put_be64(directory, 56, kSignatureOffset);
    put_be64(directory, 64, 0);
    put_be64(directory, 72, kSignatureOffset);
    put_be64(directory, 80, 1);

    if (config.identifier.size() + 1 > 32) {
        throw std::runtime_error("sample identifier is too long for fixed CD layout");
    }
    for (size_t i = 0; i < config.identifier.size(); ++i) {
        directory[96 + i] = static_cast<uint8_t>(config.identifier[i]);
    }

    const Sha256Digest requirements_hash = Sha256::hash(requirements_blob);
    const Sha256Digest xml_hash = Sha256::hash(xml_blob);
    const Sha256Digest der_hash = Sha256::hash(der_blob);
    auto put_special = [&](uint32_t slot, const Sha256Digest& digest) {
        const size_t offset = 352 - size_t(slot) * CS_SHA256_SIZE;
        std::copy(digest.begin(), digest.end(),
                  directory.begin() + static_cast<std::ptrdiff_t>(offset));
    };
    put_special(CSSLOT_REQUIREMENTS, requirements_hash);
    put_special(CSSLOT_ENTITLEMENTS_XML, xml_hash);
    put_special(CSSLOT_ENTITLEMENTS_DER, der_hash);
    std::copy(code_hash.begin(), code_hash.end(), directory.begin() + 352);

    const size_t cd_offset = kIndexArea;
    const size_t requirements_offset = cd_offset + directory.size();
    const size_t xml_offset = requirements_offset + requirements_blob.size();
    const size_t der_offset = xml_offset + xml_blob.size();
    const size_t pkcs7_offset = der_offset + der_blob.size();
    const size_t total_size = pkcs7_offset + pkcs7_blob.size();
    if (total_size != signature_size) {
        throw std::runtime_error("signature size calculation mismatch");
    }

    std::vector<uint8_t> super_blob(total_size, 0);
    put_be32(super_blob, 0, CSMAGIC_EMBEDDED_SIGNATURE);
    put_be32(super_blob, 4, static_cast<uint32_t>(total_size));
    put_be32(super_blob, 8, 5);
    const uint32_t types[5] = {
        0, CSSLOT_REQUIREMENTS, CSSLOT_ENTITLEMENTS_XML,
        CSSLOT_ENTITLEMENTS_DER, CSSLOT_SIGNATURE};
    const size_t offsets[5] = {
        cd_offset, requirements_offset, xml_offset, der_offset, pkcs7_offset};
    for (size_t i = 0; i < 5; ++i) {
        put_be32(super_blob, 12 + i * 8, types[i]);
        put_be32(super_blob, 16 + i * 8, static_cast<uint32_t>(offsets[i]));
    }

    auto copy_blob = [&](size_t offset, const std::vector<uint8_t>& source) {
        std::copy(source.begin(), source.end(),
                  super_blob.begin() + static_cast<std::ptrdiff_t>(offset));
    };
    copy_blob(cd_offset, directory);
    copy_blob(requirements_offset, requirements_blob);
    copy_blob(xml_offset, xml_blob);
    copy_blob(der_offset, der_blob);
    copy_blob(pkcs7_offset, pkcs7_blob);

    if (!config.include_code_signature) {
        return macho;
    }
    macho.insert(macho.end(), super_blob.begin(), super_blob.end());
    return macho;
}

std::vector<uint8_t> build_fat(const std::vector<std::vector<uint8_t>>& slices,
                              bool sixty_four) {
    if (slices.empty() || slices.size() > 64) {
        throw std::runtime_error("invalid sample Fat slice count");
    }
    const size_t entry_size = sixty_four ? 32 : 20;
    const size_t table_size = 8 + slices.size() * entry_size;
    std::vector<uint8_t> fat(table_size, 0);
    put_be32(fat, 0, sixty_four ? FAT_MAGIC_64 : FAT_MAGIC);
    put_be32(fat, 4, static_cast<uint32_t>(slices.size()));

    size_t offset = table_size;
    for (size_t i = 0; i < slices.size(); ++i) {
        const std::vector<uint8_t>& slice = slices[i];
        if (slice.size() < 12) throw std::runtime_error("slice too short");
        const uint32_t cpu_type =
            uint32_t(slice[4]) | (uint32_t(slice[5]) << 8u) |
            (uint32_t(slice[6]) << 16u) | (uint32_t(slice[7]) << 24u);
        const uint32_t cpu_subtype =
            uint32_t(slice[8]) | (uint32_t(slice[9]) << 8u) |
            (uint32_t(slice[10]) << 16u) | (uint32_t(slice[11]) << 24u);
        const size_t entry = 8 + i * entry_size;
        put_be32(fat, entry, cpu_type);
        put_be32(fat, entry + 4, cpu_subtype);
        if (sixty_four) {
            put_be64(fat, entry + 8, offset);
            put_be64(fat, entry + 16, slice.size());
            put_be32(fat, entry + 24, 0);
            put_be32(fat, entry + 28, 0);
        } else {
            put_be32(fat, entry + 8, static_cast<uint32_t>(offset));
            put_be32(fat, entry + 12, static_cast<uint32_t>(slice.size()));
            put_be32(fat, entry + 16, 0);
        }
        fat.insert(fat.end(), slice.begin(), slice.end());
        offset += slice.size();
    }
    return fat;
}

}  // namespace machosec::sample
