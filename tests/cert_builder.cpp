#include "cert_builder.hpp"

namespace netsec::testutil {

namespace {

constexpr std::string_view kCertType =
    "ssh-ed25519-cert-v01@openssh.com";
constexpr std::string_view kKeyType = "ssh-ed25519";

void writeOptionList(wire::Writer& writer,
                     const std::vector<NamedOption>& options) {
    writer.writeUint32(static_cast<std::uint32_t>(options.size()));
    for (const NamedOption& option : options) {
        writer.writeString(option.name);
        writer.writeString(option.value);
    }
}

}  // namespace

CertBuilder::CertBuilder() {
    caKey_ = makeKeyBlob("default-ca");
}

CertBuilder& CertBuilder::serial(std::uint64_t value) {
    serial_ = value;
    return *this;
}

CertBuilder& CertBuilder::userCertificate() {
    type_ = 1;
    return *this;
}

CertBuilder& CertBuilder::hostCertificate() {
    type_ = 2;
    return *this;
}

CertBuilder& CertBuilder::principal(std::string principal) {
    principals_.push_back(std::move(principal));
    return *this;
}

CertBuilder& CertBuilder::validity(std::uint64_t after,
                                   std::uint64_t before) {
    validAfter_ = after;
    validBefore_ = before;
    return *this;
}

CertBuilder& CertBuilder::keyId(std::string id) {
    keyId_ = std::move(id);
    return *this;
}

CertBuilder& CertBuilder::criticalOption(std::string name,
                                         std::string value) {
    critical_.push_back(NamedOption{std::move(name), std::move(value)});
    return *this;
}

CertBuilder& CertBuilder::extension(std::string name, std::string value) {
    extensions_.push_back(NamedOption{std::move(name), std::move(value)});
    return *this;
}

CertBuilder& CertBuilder::caKey(std::string_view rawCaKey) {
    // Callers supply either a full blob (makeKeyBlob) or raw key bytes.
    if (rawCaKey.rfind("\x00\x00\x00\x0bssh-ed25519", 0) == 0) {
        caKey_.assign(rawCaKey);
    } else {
        wire::Writer writer;
        writer.writeString(kKeyType);
        writer.writeString(rawCaKey);
        caKey_ = writer.take();
    }
    return *this;
}

CertBuilder& CertBuilder::malformedSignature() {
    malformedSignature_ = true;
    return *this;
}

std::string CertBuilder::build() const {
    wire::Writer writer;
    writer.writeString(kCertType);
    writer.writeString(std::string_view("nonce-0123456789"));
    writer.writeString(kKeyType);
    writer.writeString(std::string_view("ed25519-public-key-bytes"));
    writer.writeUint64(serial_);
    writer.writeUint32(type_);

    writer.writeUint32(static_cast<std::uint32_t>(principals_.size()));
    for (const std::string& principal : principals_) {
        writer.writeString(principal);
    }

    writer.writeUint64(validAfter_);
    writer.writeUint64(validBefore_);
    writer.writeString(keyId_);
    writeOptionList(writer, critical_);
    writeOptionList(writer, extensions_);
    writer.writeString(std::string_view{});  // reserved

    // Embedded CA key.
    wire::Reader caReader(caKey_);
    std::string_view caType;
    std::string_view caBytes;
    if (!caReader.readString(caType) || !caReader.readString(caBytes) ||
        !caReader.empty()) {
        return {};
    }
    writer.writeString(caType);
    writer.writeString(caBytes);

    if (malformedSignature_) {
        writer.writeRaw(std::string_view("\x00\x00"));  // truncated
    } else {
        writer.writeString(makeSignature("cert-sig"));
    }
    return writer.take();
}

std::string makeKeyBlob(std::string_view seedName) {
    wire::Writer writer;
    writer.writeString(kKeyType);
    writer.writeString(seedName);
    return writer.take();
}

std::string makeSignature(std::string_view marker) {
    wire::Writer writer;
    writer.writeString(std::string_view("ssh-ed25519"));
    writer.writeString(marker);
    return writer.take();
}

}  // namespace netsec::testutil
