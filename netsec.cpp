#include "netsec.hpp"

#include <array>
#include <map>
#include <set>
#include <sstream>
#include <unordered_map>
#include <unordered_set>

namespace netsec::usb {

const char* to_string(DescriptorError error) noexcept {
    switch (error) {
        case DescriptorError::None:
            return "none";
        case DescriptorError::EmptyInput:
            return "empty input";
        case DescriptorError::TruncatedDescriptor:
            return "truncated descriptor";
        case DescriptorError::InvalidDescriptorLength:
            return "invalid descriptor length";
        case DescriptorError::UnexpectedDescriptorType:
            return "unexpected descriptor type";
        case DescriptorError::MisplacedDescriptor:
            return "misplaced descriptor";
        case DescriptorError::DescriptorOverrun:
            return "descriptor overrun or overlap";
        case DescriptorError::DescriptorTooLarge:
            return "descriptor set too large";
        case DescriptorError::TooManyDescriptors:
            return "too many descriptors";
        case DescriptorError::TrailingData:
            return "trailing data";
        case DescriptorError::MissingConfiguration:
            return "missing configuration";
        case DescriptorError::InvalidDeviceDescriptor:
            return "invalid device descriptor";
        case DescriptorError::InvalidDeviceMaxPacket:
            return "invalid device endpoint-zero packet size";
        case DescriptorError::ConfigurationCountMismatch:
            return "configuration count mismatch";
        case DescriptorError::InvalidConfigurationDescriptor:
            return "invalid configuration descriptor";
        case DescriptorError::InvalidConfigurationValue:
            return "invalid configuration value";
        case DescriptorError::InvalidConfigurationAttributes:
            return "invalid configuration attributes";
        case DescriptorError::InvalidConfigurationLength:
            return "invalid configuration length";
        case DescriptorError::DuplicateConfigurationValue:
            return "duplicate configuration value";
        case DescriptorError::InvalidInterfaceNumber:
            return "invalid interface number";
        case DescriptorError::DuplicateAlternateSetting:
            return "duplicate alternate setting";
        case DescriptorError::MissingAlternateSetting:
            return "missing alternate setting zero";
        case DescriptorError::InterfaceEndpointCountMismatch:
            return "interface endpoint count mismatch";
        case DescriptorError::InterfaceClassMismatch:
            return "interface class mismatch across alternate settings";
        case DescriptorError::InvalidEndpointDescriptor:
            return "invalid endpoint descriptor";
        case DescriptorError::InvalidEndpointAddress:
            return "invalid endpoint address";
        case DescriptorError::ZeroEndpointAddress:
            return "endpoint number zero is reserved";
        case DescriptorError::DuplicateEndpointAddress:
            return "duplicate endpoint address";
        case DescriptorError::EndpointAddressConflict:
            return "endpoint address conflicts across interfaces";
        case DescriptorError::InvalidTransferType:
            return "invalid endpoint transfer type";
        case DescriptorError::InvalidMaxPacketSize:
            return "invalid endpoint packet size";
        case DescriptorError::InvalidEndpointInterval:
            return "invalid endpoint interval";
        case DescriptorError::InterfaceAssociationBounds:
            return "interface association outside configuration";
        case DescriptorError::InterfaceAssociationOverlap:
            return "overlapping interface associations";
        case DescriptorError::InterfaceAssociationMismatch:
            return "interface association mismatch";
        case DescriptorError::UnknownConfiguration:
            return "unknown configuration";
        case DescriptorError::UnknownAlternateSetting:
            return "unknown alternate setting";
        case DescriptorError::NotConfigured:
            return "host is not configured";
    }
    return "unknown error";
}

namespace {

constexpr std::uint8_t kClassSpecificFirst = 0x21;
constexpr std::uint8_t kClassSpecificLast = 0x2f;

std::uint16_t readLe16(std::span<const std::uint8_t> data, std::size_t offset) {
    return static_cast<std::uint16_t>(data[offset]) |
           static_cast<std::uint16_t>(static_cast<std::uint16_t>(data[offset + 1]) << 8u);
}

bool isClassSpecificType(std::uint8_t type) noexcept {
    return type >= kClassSpecificFirst && type <= kClassSpecificLast;
}

void addDiagnostic(std::vector<Diagnostic>& diagnostics,
                   DescriptorError error,
                   std::size_t offset,
                   std::uint8_t descriptorType,
                   std::string message) {
    diagnostics.push_back(Diagnostic{error, offset, descriptorType, std::move(message)});
}

bool validateEndpointFields(const EndpointDescriptor& endpoint,
                            const ParseOptions& options,
                            std::vector<Diagnostic>& diagnostics) {
    const std::size_t before = diagnostics.size();
    const std::uint8_t address = endpoint.address;
    const std::uint8_t attributes = endpoint.attributes;
    const std::uint16_t rawPacketSize = endpoint.maxPacketSize;
    const TransferType transfer = endpoint.transferType();
    const std::uint16_t payload = endpoint.packetPayloadSize();
    const std::uint8_t transactions = endpoint.transactionOpportunities();

    if ((address & 0x70u) != 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidEndpointAddress, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "endpoint address uses reserved bits 6:4");
    }
    if (endpoint.endpointNumber() == 0) {
        addDiagnostic(diagnostics, DescriptorError::ZeroEndpointAddress, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "interface descriptor attempted to enumerate endpoint zero");
    }

    if (transfer == TransferType::Control) {
        addDiagnostic(diagnostics, DescriptorError::InvalidTransferType, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "interface endpoint cannot use control transfer type");
    }
    if ((attributes & 0x80u) != 0 ||
        (transfer == TransferType::Isochronous ? (attributes & 0x40u) != 0
                                               : (attributes & 0x7cu) != 0)) {
        addDiagnostic(diagnostics, DescriptorError::InvalidTransferType, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "endpoint attributes use reserved bits");
    }

    if ((rawPacketSize & 0xe000u) != 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "wMaxPacketSize uses reserved bits 15:13");
    }
    if (payload == 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "endpoint packet payload must be non-zero");
    }

    const UsbSpeed speed = options.speed;
    if (speed != UsbSpeed::High && speed != UsbSpeed::Super &&
        speed != UsbSpeed::SuperPlus && speed != UsbSpeed::Unknown &&
        transactions != 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "high-bandwidth transactions require a high-speed endpoint");
    }
    if (transactions == 3) {
        addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "reserved high-bandwidth transaction encoding");
    }
    if (transfer == TransferType::Bulk && transactions != 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "bulk endpoints cannot use high-bandwidth transactions");
    }

    switch (speed) {
        case UsbSpeed::Low:
            if (transfer != TransferType::Interrupt) {
                addDiagnostic(diagnostics, DescriptorError::InvalidTransferType,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "low-speed endpoints support interrupt transfers only");
            }
            if (payload > 8) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "low-speed endpoint payload exceeds eight bytes");
            }
            break;
        case UsbSpeed::Full:
            if (transfer == TransferType::Bulk || transfer == TransferType::Interrupt) {
                if (payload > 64) {
                    addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                                  endpoint.sourceOffset, kEndpointDescriptorType,
                                  "full-speed bulk/interrupt packet exceeds 64 bytes");
                }
                if (transfer == TransferType::Bulk && payload != 8 && payload != 16 &&
                    payload != 32 && payload != 64) {
                    addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                                  endpoint.sourceOffset, kEndpointDescriptorType,
                                  "full-speed bulk packet size must be 8, 16, 32, or 64");
                }
            } else if (payload > 1023) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "full-speed isochronous packet exceeds 1023 bytes");
            }
            break;
        case UsbSpeed::High:
            if (transfer == TransferType::Bulk && payload > 512) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "high-speed bulk packet exceeds 512 bytes");
            }
            if ((transfer == TransferType::Interrupt || transfer == TransferType::Isochronous) &&
                payload > 1024) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "high-speed packet exceeds 1024 bytes");
            }
            break;
        case UsbSpeed::Super:
        case UsbSpeed::SuperPlus:
            if (payload > 1024) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "super-speed packet exceeds 1024 bytes");
            }
            if (transfer == TransferType::Isochronous && transactions > 2) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "invalid super-speed isochronous burst encoding");
            }
            break;
        case UsbSpeed::Unknown:
            if (transfer == TransferType::Bulk && payload > 512) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "bulk packet exceeds 512 bytes");
            }
            if (payload > 1024) {
                addDiagnostic(diagnostics, DescriptorError::InvalidMaxPacketSize,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "endpoint packet exceeds 1024 bytes");
            }
            break;
    }

    if (speed == UsbSpeed::High &&
        (transfer == TransferType::Interrupt || transfer == TransferType::Isochronous) &&
        (endpoint.interval < 1 || endpoint.interval > 16)) {
        addDiagnostic(diagnostics, DescriptorError::InvalidEndpointInterval, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "high-speed interrupt/isochronous interval must be in 1..16");
    }
    if (speed != UsbSpeed::High && speed != UsbSpeed::Unknown &&
        (transfer == TransferType::Interrupt || transfer == TransferType::Isochronous) &&
        endpoint.interval == 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidEndpointInterval, endpoint.sourceOffset,
                      kEndpointDescriptorType,
                      "interrupt/isochronous interval must be non-zero");
    }

    return diagnostics.size() == before;
}

class Parser {
public:
    Parser(std::span<const std::uint8_t> data, ParseOptions options)
        : data_(data), options_(std::move(options)) {}

    ParseResult parse() {
        ParseResult result;

        if (data_.empty()) {
            fail(DescriptorError::EmptyInput, 0, 0, "descriptor image is empty");
            result.diagnostics = std::move(diagnostics_);
            return result;
        }
        if (options_.maxImageBytes != 0 && data_.size() > options_.maxImageBytes) {
            fail(DescriptorError::DescriptorTooLarge, 0, 0,
                 "descriptor image exceeds configured byte limit");
            result.diagnostics = std::move(diagnostics_);
            return result;
        }

        DescriptorImage image;
        image.byteCount = data_.size();
        std::size_t offset = 0;

        const bool startsWithDevice =
            data_.size() >= 2 && data_[1] == kDeviceDescriptorType;
        if (startsWithDevice) {
            DeviceDescriptor device;
            if (!parseDevice(offset, device)) {
                result.diagnostics = std::move(diagnostics_);
                return result;
            }
            image.device = device;
            offset += kDeviceDescriptorLength;
        } else if (options_.requireDeviceDescriptor) {
            fail(DescriptorError::InvalidDeviceDescriptor, 0,
                 data_.size() >= 2 ? data_[1] : 0,
                 "descriptor image does not start with a device descriptor");
            result.diagnostics = std::move(diagnostics_);
            return result;
        }

        while (offset < data_.size()) {
            if (data_.size() - offset < 2) {
                fail(DescriptorError::TruncatedDescriptor, offset, data_[offset],
                     "descriptor header is truncated");
                result.diagnostics = std::move(diagnostics_);
                return result;
            }
            const std::uint8_t type = data_[offset + 1];
            if (type != kConfigurationDescriptorType) {
                fail(DescriptorError::UnexpectedDescriptorType, offset, type,
                     "expected a configuration descriptor at the top level");
                result.diagnostics = std::move(diagnostics_);
                return result;
            }

            ConfigurationDescriptor configuration;
            std::size_t setLength = 0;
            if (!parseConfigurationSet(offset, configuration, setLength)) {
                result.diagnostics = std::move(diagnostics_);
                return result;
            }
            image.configurations.push_back(std::move(configuration));
            offset += setLength;
        }

        if (image.configurations.empty() && options_.requireConfiguration) {
            fail(DescriptorError::MissingConfiguration, 0, 0,
                 "descriptor image contains no configuration descriptor");
            result.diagnostics = std::move(diagnostics_);
            return result;
        }

        for (std::size_t i = 0; i < image.configurations.size(); ++i) {
            for (std::size_t j = i + 1; j < image.configurations.size(); ++j) {
                if (image.configurations[i].value == image.configurations[j].value) {
                    fail(DescriptorError::DuplicateConfigurationValue,
                         image.configurations[j].sourceOffset,
                         kConfigurationDescriptorType,
                         "two configuration descriptors share one bConfigurationValue");
                    result.diagnostics = std::move(diagnostics_);
                    return result;
                }
            }
        }

        if (image.device) {
            const std::uint8_t declared = image.device->numConfigurations;
            if (image.configurations.size() > declared) {
                fail(DescriptorError::ConfigurationCountMismatch, 0,
                     kDeviceDescriptorType,
                     "more configuration descriptors exist than bNumConfigurations declares");
                result.diagnostics = std::move(diagnostics_);
                return result;
            }
            if (options_.requireAllConfigurations &&
                image.configurations.size() != declared) {
                fail(DescriptorError::ConfigurationCountMismatch, 0,
                     kDeviceDescriptorType,
                     "configuration descriptor count differs from bNumConfigurations");
                result.diagnostics = std::move(diagnostics_);
                return result;
            }
        }

        image.descriptorCount = descriptorCount_;
        result.image = std::move(image);
        result.diagnostics = std::move(diagnostics_);
        return result;
    }

private:
    bool fail(DescriptorError error, std::size_t offset, std::uint8_t type,
              std::string message) {
        addDiagnostic(diagnostics_, error, offset, type, std::move(message));
        return false;
    }

    bool countDescriptor(std::size_t offset, std::uint8_t type) {
        ++descriptorCount_;
        if (options_.maxDescriptors != 0 && descriptorCount_ > options_.maxDescriptors) {
            return fail(DescriptorError::TooManyDescriptors, offset, type,
                        "descriptor count exceeds configured limit");
        }
        return true;
    }

    bool parseDevice(std::size_t offset, DeviceDescriptor& device) {
        if (data_.size() - offset < 2) {
            return fail(DescriptorError::TruncatedDescriptor, offset, 0,
                        "device descriptor header is truncated");
        }
        if (data_[offset] != kDeviceDescriptorLength) {
            return fail(DescriptorError::InvalidDescriptorLength, offset,
                        kDeviceDescriptorType,
                        "device descriptor bLength must be 18");
        }
        if (data_[offset + 1] != kDeviceDescriptorType) {
            return fail(DescriptorError::UnexpectedDescriptorType, offset, data_[offset + 1],
                        "first descriptor is not a device descriptor");
        }
        if (data_.size() - offset < kDeviceDescriptorLength) {
            return fail(DescriptorError::TruncatedDescriptor, offset,
                        kDeviceDescriptorType,
                        "device descriptor is truncated");
        }
        if (!countDescriptor(offset, kDeviceDescriptorType)) {
            return false;
        }

        const auto* raw = data_.data() + offset;
        device.bcdUsb = static_cast<std::uint16_t>(raw[2]) |
                        static_cast<std::uint16_t>(static_cast<std::uint16_t>(raw[3]) << 8u);
        device.deviceClass = raw[4];
        device.deviceSubClass = raw[5];
        device.deviceProtocol = raw[6];
        device.maxPacketSize0 = raw[7];
        device.vendorId = static_cast<std::uint16_t>(raw[8]) |
                          static_cast<std::uint16_t>(static_cast<std::uint16_t>(raw[9]) << 8u);
        device.productId = static_cast<std::uint16_t>(raw[10]) |
                           static_cast<std::uint16_t>(static_cast<std::uint16_t>(raw[11]) << 8u);
        device.bcdDevice = static_cast<std::uint16_t>(raw[12]) |
                           static_cast<std::uint16_t>(static_cast<std::uint16_t>(raw[13]) << 8u);
        device.iManufacturer = raw[14];
        device.iProduct = raw[15];
        device.iSerialNumber = raw[16];
        device.numConfigurations = raw[17];
        device.sourceOffset = offset;

        if (device.numConfigurations == 0) {
            return fail(DescriptorError::InvalidDeviceDescriptor, offset,
                        kDeviceDescriptorType,
                        "device descriptor declares zero configurations");
        }
        const bool validPacketSize = device.maxPacketSize0 == 8 ||
                                     device.maxPacketSize0 == 16 ||
                                     device.maxPacketSize0 == 32 ||
                                     device.maxPacketSize0 == 64;
        if (!validPacketSize ||
            (options_.speed == UsbSpeed::Low && device.maxPacketSize0 != 8)) {
            return fail(DescriptorError::InvalidDeviceMaxPacket, offset,
                        kDeviceDescriptorType,
                        "bMaxPacketSize0 must be 8, 16, 32, or 64 (8 for low speed)");
        }
        return true;
    }

    bool parseConfigurationSet(std::size_t offset,
                               ConfigurationDescriptor& configuration,
                               std::size_t& setLength) {
        if (data_.size() - offset < kConfigurationDescriptorLength) {
            return fail(DescriptorError::TruncatedDescriptor, offset,
                        kConfigurationDescriptorType,
                        "configuration descriptor header is truncated");
        }
        if (data_[offset] != kConfigurationDescriptorLength) {
            return fail(DescriptorError::InvalidDescriptorLength, offset,
                        kConfigurationDescriptorType,
                        "configuration descriptor bLength must be 9");
        }
        if (data_[offset + 1] != kConfigurationDescriptorType) {
            return fail(DescriptorError::UnexpectedDescriptorType, offset, data_[offset + 1],
                        "expected a configuration descriptor");
        }

        const std::uint16_t totalLength = readLe16(data_, offset + 2);
        if (totalLength < kConfigurationDescriptorLength) {
            return fail(DescriptorError::InvalidConfigurationLength, offset,
                        kConfigurationDescriptorType,
                        "wTotalLength is smaller than the configuration descriptor itself");
        }
        if (options_.maxConfigurationLength != 0 &&
            totalLength > options_.maxConfigurationLength) {
            return fail(DescriptorError::DescriptorTooLarge, offset,
                        kConfigurationDescriptorType,
                        "wTotalLength exceeds configured configuration byte limit");
        }
        if (totalLength > data_.size() - offset) {
            return fail(DescriptorError::DescriptorOverrun, offset,
                        kConfigurationDescriptorType,
                        "wTotalLength overlaps the end of the descriptor image");
        }
        if (!countDescriptor(offset, kConfigurationDescriptorType)) {
            return false;
        }

        const std::size_t setEnd = offset + totalLength;
        configuration = {};
        configuration.value = data_[offset + 5];
        configuration.iConfiguration = data_[offset + 6];
        configuration.attributes = data_[offset + 7];
        configuration.maxPower = data_[offset + 8];
        configuration.totalLength = totalLength;
        configuration.declaredInterfaceCount = data_[offset + 4];
        configuration.sourceOffset = offset;

        if (configuration.value == 0) {
            return fail(DescriptorError::InvalidConfigurationValue, offset,
                        kConfigurationDescriptorType,
                        "bConfigurationValue zero is reserved");
        }
        if ((configuration.attributes & 0x80u) == 0 ||
            (configuration.attributes & 0x1fu) != 0) {
            return fail(DescriptorError::InvalidConfigurationAttributes, offset,
                        kConfigurationDescriptorType,
                        "bmAttributes reserved bit or bit 7 is invalid");
        }
        if (configuration.declaredInterfaceCount == 0) {
            return fail(DescriptorError::InvalidConfigurationDescriptor, offset,
                        kConfigurationDescriptorType,
                        "configuration declares zero interfaces");
        }

        std::optional<std::size_t> currentInterface;
        std::uint8_t observedEndpointCount = 0;
        std::set<std::uint8_t> seenInterfaceNumbers;

        auto closeInterface = [&]() -> bool {
            if (!currentInterface) {
                return true;
            }
            const auto& interfaceDescriptor = configuration.interfaces[*currentInterface];
            if (observedEndpointCount != interfaceDescriptor.declaredEndpointCount) {
                return fail(DescriptorError::InterfaceEndpointCountMismatch,
                            interfaceDescriptor.sourceOffset, kInterfaceDescriptorType,
                            "bNumEndpoints does not match the endpoint descriptors present");
            }
            currentInterface.reset();
            observedEndpointCount = 0;
            return true;
        };

        std::size_t cursor = offset + kConfigurationDescriptorLength;
        while (cursor < setEnd) {
            if (setEnd - cursor < 2) {
                return fail(DescriptorError::TruncatedDescriptor, cursor, data_[cursor],
                            "descriptor header is truncated inside configuration");
            }
            const std::uint8_t length = data_[cursor];
            const std::uint8_t type = data_[cursor + 1];
            if (length < 2) {
                return fail(DescriptorError::InvalidDescriptorLength, cursor, type,
                            "descriptor bLength must be at least two");
            }
            if (length > setEnd - cursor) {
                return fail(DescriptorError::DescriptorOverrun, cursor, type,
                            "descriptor overlaps the configuration boundary");
            }
            if (!countDescriptor(cursor, type)) {
                return false;
            }

            switch (type) {
                case kInterfaceDescriptorType: {
                    if (length != kInterfaceDescriptorLength) {
                        return fail(DescriptorError::InvalidDescriptorLength, cursor, type,
                                    "interface descriptor bLength must be 9");
                    }
                    if (!closeInterface()) {
                        return false;
                    }

                    InterfaceDescriptor interfaceDescriptor;
                    interfaceDescriptor.number = data_[cursor + 2];
                    interfaceDescriptor.alternateSetting = data_[cursor + 3];
                    interfaceDescriptor.declaredEndpointCount = data_[cursor + 4];
                    interfaceDescriptor.interfaceClass = data_[cursor + 5];
                    interfaceDescriptor.interfaceSubClass = data_[cursor + 6];
                    interfaceDescriptor.interfaceProtocol = data_[cursor + 7];
                    interfaceDescriptor.iInterface = data_[cursor + 8];
                    interfaceDescriptor.sourceOffset = cursor;

                    if (interfaceDescriptor.number >= configuration.declaredInterfaceCount) {
                        return fail(DescriptorError::InvalidInterfaceNumber, cursor, type,
                                    "interface number is outside bNumInterfaces");
                    }
                    for (const auto& existing : configuration.interfaces) {
                        if (existing.number == interfaceDescriptor.number &&
                            existing.alternateSetting ==
                                interfaceDescriptor.alternateSetting) {
                            return fail(DescriptorError::DuplicateAlternateSetting, cursor, type,
                                        "duplicate interface number and alternate setting");
                        }
                    }
                    configuration.interfaces.push_back(std::move(interfaceDescriptor));
                    seenInterfaceNumbers.insert(
                        configuration.interfaces.back().number);
                    currentInterface = configuration.interfaces.size() - 1;
                    observedEndpointCount = 0;
                    break;
                }
                case kEndpointDescriptorType: {
                    if (length != kEndpointDescriptorLength) {
                        return fail(DescriptorError::InvalidDescriptorLength, cursor, type,
                                    "endpoint descriptor bLength must be 7");
                    }
                    if (!currentInterface) {
                        return fail(DescriptorError::MisplacedDescriptor, cursor, type,
                                    "endpoint descriptor is not associated with an interface");
                    }

                    EndpointDescriptor endpoint;
                    endpoint.address = data_[cursor + 2];
                    endpoint.attributes = data_[cursor + 3];
                    endpoint.maxPacketSize = readLe16(data_, cursor + 4);
                    endpoint.interval = data_[cursor + 6];
                    endpoint.sourceOffset = cursor;
                    auto& interfaceDescriptor = configuration.interfaces[*currentInterface];
                    endpoint.interfaceNumber = interfaceDescriptor.number;
                    endpoint.alternateSetting = interfaceDescriptor.alternateSetting;

                    if (!validateEndpointFields(endpoint, options_, diagnostics_)) {
                        return false;
                    }
                    for (const auto& existing : interfaceDescriptor.endpoints) {
                        if (existing.address == endpoint.address) {
                            return fail(DescriptorError::DuplicateEndpointAddress, cursor, type,
                                        "duplicate endpoint address in one alternate setting");
                        }
                    }
                    interfaceDescriptor.endpoints.push_back(endpoint);
                    ++observedEndpointCount;
                    break;
                }
                case kInterfaceAssociationType: {
                    if (length != kInterfaceAssociationLength) {
                        return fail(DescriptorError::InvalidDescriptorLength, cursor, type,
                                    "interface association descriptor bLength must be 8");
                    }
                    if (!closeInterface()) {
                        return false;
                    }

                    InterfaceAssociationDescriptor association;
                    association.firstInterface = data_[cursor + 2];
                    association.interfaceCount = data_[cursor + 3];
                    association.functionClass = data_[cursor + 4];
                    association.functionSubClass = data_[cursor + 5];
                    association.functionProtocol = data_[cursor + 6];
                    association.iFunction = data_[cursor + 7];
                    association.sourceOffset = cursor;

                    const std::uint16_t first = association.firstInterface;
                    const std::uint16_t count = association.interfaceCount;
                    if (count == 0 ||
                        first + count > configuration.declaredInterfaceCount) {
                        return fail(DescriptorError::InterfaceAssociationBounds, cursor, type,
                                    "interface association range exceeds the configuration");
                    }
                    for (std::uint16_t number = first;
                         number < static_cast<std::uint16_t>(first + count); ++number) {
                        if (seenInterfaceNumbers.count(
                                static_cast<std::uint8_t>(number)) != 0) {
                            return fail(DescriptorError::MisplacedDescriptor, cursor, type,
                                        "interface association appears after an interface "
                                        "that it describes");
                        }
                    }
                    for (const auto& existing : configuration.associations) {
                        const std::uint16_t existingFirst = existing.firstInterface;
                        const std::uint16_t existingEnd =
                            static_cast<std::uint16_t>(existingFirst + existing.interfaceCount);
                        if (first < existingEnd &&
                            existingFirst < static_cast<std::uint16_t>(first + count)) {
                            return fail(DescriptorError::InterfaceAssociationOverlap, cursor, type,
                                        "interface associations overlap");
                        }
                    }
                    configuration.associations.push_back(association);
                    break;
                }
                case kDeviceDescriptorType:
                case kConfigurationDescriptorType:
                    return fail(DescriptorError::MisplacedDescriptor, cursor, type,
                                "device/configuration descriptor nested in a configuration");
                default:
                    if (isClassSpecificType(type)) {
                        if (!options_.allowClassSpecificDescriptors) {
                            return fail(DescriptorError::UnexpectedDescriptorType, cursor, type,
                                        "class-specific descriptors are disabled");
                        }
                        if (!currentInterface) {
                            return fail(DescriptorError::MisplacedDescriptor, cursor, type,
                                        "class-specific descriptor is outside an interface");
                        }
                    } else if (options_.allowUnknownDescriptors) {
                        if (!currentInterface) {
                            return fail(DescriptorError::MisplacedDescriptor, cursor, type,
                                        "unknown descriptor is outside an interface");
                        }
                    } else {
                        return fail(DescriptorError::UnexpectedDescriptorType, cursor, type,
                                    "unsupported descriptor type inside configuration");
                    }
                    break;
            }
            cursor += length;
        }

        if (!closeInterface()) {
            return false;
        }

        const ValidationResult validation = validateConfiguration(configuration, options_);
        if (!validation.ok()) {
            diagnostics_.insert(diagnostics_.end(), validation.diagnostics.begin(),
                                validation.diagnostics.end());
            return false;
        }

        setLength = totalLength;
        return true;
    }

    std::span<const std::uint8_t> data_;
    ParseOptions options_;
    std::vector<Diagnostic> diagnostics_;
    std::size_t descriptorCount_ = 0;
};

}  // namespace

ValidationResult validateConfiguration(const ConfigurationDescriptor& configuration,
                                       const ParseOptions& options) {
    std::vector<Diagnostic> diagnostics;

    if (configuration.totalLength < kConfigurationDescriptorLength) {
        addDiagnostic(diagnostics, DescriptorError::InvalidConfigurationLength,
                      configuration.sourceOffset, kConfigurationDescriptorType,
                      "configuration wTotalLength is smaller than nine bytes");
    }
    if (configuration.value == 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidConfigurationValue,
                      configuration.sourceOffset, kConfigurationDescriptorType,
                      "bConfigurationValue zero is reserved");
    }
    if ((configuration.attributes & 0x80u) == 0 ||
        (configuration.attributes & 0x1fu) != 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidConfigurationAttributes,
                      configuration.sourceOffset, kConfigurationDescriptorType,
                      "configuration attributes use reserved bits");
    }
    if (configuration.declaredInterfaceCount == 0) {
        addDiagnostic(diagnostics, DescriptorError::InvalidConfigurationDescriptor,
                      configuration.sourceOffset, kConfigurationDescriptorType,
                      "configuration declares zero interfaces");
    }

    std::map<std::uint8_t, std::vector<const InterfaceDescriptor*>> interfacesByNumber;
    std::set<std::pair<std::uint8_t, std::uint8_t>> interfaceKeys;

    for (const auto& interfaceDescriptor : configuration.interfaces) {
        if (interfaceDescriptor.number >= configuration.declaredInterfaceCount) {
            addDiagnostic(diagnostics, DescriptorError::InvalidInterfaceNumber,
                          interfaceDescriptor.sourceOffset, kInterfaceDescriptorType,
                          "interface number is outside bNumInterfaces");
        }
        if (!interfaceKeys.insert({interfaceDescriptor.number,
                                    interfaceDescriptor.alternateSetting})
                 .second) {
            addDiagnostic(diagnostics, DescriptorError::DuplicateAlternateSetting,
                          interfaceDescriptor.sourceOffset, kInterfaceDescriptorType,
                          "duplicate interface number and alternate setting");
        }
        if (interfaceDescriptor.endpoints.size() !=
            interfaceDescriptor.declaredEndpointCount) {
            addDiagnostic(diagnostics, DescriptorError::InterfaceEndpointCountMismatch,
                          interfaceDescriptor.sourceOffset, kInterfaceDescriptorType,
                          "bNumEndpoints differs from parsed endpoint count");
        }

        std::set<std::uint8_t> addressesInAlternate;
        for (const auto& endpoint : interfaceDescriptor.endpoints) {
            validateEndpointFields(endpoint, options, diagnostics);
            if (!addressesInAlternate.insert(endpoint.address).second) {
                addDiagnostic(diagnostics, DescriptorError::DuplicateEndpointAddress,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "duplicate endpoint address in one alternate setting");
            }
            if (endpoint.interfaceNumber != interfaceDescriptor.number ||
                endpoint.alternateSetting != interfaceDescriptor.alternateSetting) {
                addDiagnostic(diagnostics, DescriptorError::InterfaceAssociationMismatch,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "endpoint is associated with the wrong interface view");
            }
        }
        interfacesByNumber[interfaceDescriptor.number].push_back(&interfaceDescriptor);
    }

    for (auto& [number, alternatives] : interfacesByNumber) {
        (void)number;
        std::sort(alternatives.begin(), alternatives.end(),
                  [](const InterfaceDescriptor* lhs, const InterfaceDescriptor* rhs) {
                      return lhs->alternateSetting < rhs->alternateSetting;
                  });
        const InterfaceDescriptor* altZero = nullptr;
        for (const auto* alternative : alternatives) {
            if (alternative->alternateSetting == 0) {
                altZero = alternative;
                break;
            }
        }
        if (options.requireAlternateSettingZero && altZero == nullptr) {
            addDiagnostic(diagnostics, DescriptorError::MissingAlternateSetting,
                          alternatives.front()->sourceOffset, kInterfaceDescriptorType,
                          "interface has no alternate setting zero");
        }
        if (options.requireAlternateClassMatch && altZero != nullptr) {
            for (const auto* alternative : alternatives) {
                if (alternative->interfaceClass != altZero->interfaceClass ||
                    alternative->interfaceSubClass != altZero->interfaceSubClass ||
                    alternative->interfaceProtocol != altZero->interfaceProtocol) {
                    addDiagnostic(diagnostics, DescriptorError::InterfaceClassMismatch,
                                  alternative->sourceOffset, kInterfaceDescriptorType,
                                  "alternate settings disagree on interface class fields");
                }
            }
        }
    }

    if (options.requireContiguousInterfaces) {
        for (std::uint16_t number = 0; number < configuration.declaredInterfaceCount;
             ++number) {
            if (interfacesByNumber.find(static_cast<std::uint8_t>(number)) ==
                interfacesByNumber.end()) {
                addDiagnostic(diagnostics, DescriptorError::InvalidInterfaceNumber,
                              configuration.sourceOffset, kConfigurationDescriptorType,
                              "configuration omits an interface number declared by bNumInterfaces");
            }
        }
    }

    std::map<std::uint8_t, std::uint8_t> addressOwner;
    std::map<std::pair<std::uint8_t, std::uint8_t>, TransferType> interfaceAddressType;
    for (const auto& interfaceDescriptor : configuration.interfaces) {
        for (const auto& endpoint : interfaceDescriptor.endpoints) {
            const auto owner = addressOwner.find(endpoint.address);
            if (owner != addressOwner.end() &&
                owner->second != interfaceDescriptor.number) {
                addDiagnostic(diagnostics, DescriptorError::EndpointAddressConflict,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "one endpoint address is used by two different interfaces");
            } else {
                addressOwner.emplace(endpoint.address, interfaceDescriptor.number);
            }

            const auto key =
                std::make_pair(interfaceDescriptor.number, endpoint.address);
            const auto typeEntry = interfaceAddressType.find(key);
            if (typeEntry != interfaceAddressType.end() &&
                typeEntry->second != endpoint.transferType()) {
                addDiagnostic(diagnostics, DescriptorError::InvalidTransferType,
                              endpoint.sourceOffset, kEndpointDescriptorType,
                              "endpoint transfer type changes across alternate settings");
            } else {
                interfaceAddressType.emplace(key, endpoint.transferType());
            }
        }
    }

    for (std::size_t i = 0; i < configuration.associations.size(); ++i) {
        const auto& association = configuration.associations[i];
        const std::uint16_t first = association.firstInterface;
        const std::uint16_t count = association.interfaceCount;
        if (count == 0 ||
            first + count > configuration.declaredInterfaceCount) {
            addDiagnostic(diagnostics, DescriptorError::InterfaceAssociationBounds,
                          association.sourceOffset, kInterfaceAssociationType,
                          "interface association range exceeds bNumInterfaces");
            continue;
        }
        for (std::size_t j = i + 1; j < configuration.associations.size(); ++j) {
            const auto& other = configuration.associations[j];
            const std::uint16_t otherFirst = other.firstInterface;
            const std::uint16_t otherEnd =
                static_cast<std::uint16_t>(otherFirst + other.interfaceCount);
            if (first < otherEnd &&
                otherFirst < static_cast<std::uint16_t>(first + count)) {
                addDiagnostic(diagnostics, DescriptorError::InterfaceAssociationOverlap,
                              association.sourceOffset, kInterfaceAssociationType,
                              "interface association ranges overlap");
            }
        }

        for (std::uint16_t number = first;
             number < static_cast<std::uint16_t>(first + count); ++number) {
            const auto entry = interfacesByNumber.find(static_cast<std::uint8_t>(number));
            if (entry == interfacesByNumber.end()) {
                addDiagnostic(diagnostics, DescriptorError::InterfaceAssociationBounds,
                              association.sourceOffset, kInterfaceAssociationType,
                              "interface association covers a missing interface");
                continue;
            }
            const auto& alternatives = entry->second;
            const auto altZero = std::find_if(
                alternatives.begin(), alternatives.end(),
                [](const InterfaceDescriptor* descriptor) {
                    return descriptor->alternateSetting == 0;
                });
            if (altZero != alternatives.end() &&
                options.requireAssociationClassMatch &&
                ((*altZero)->interfaceClass != association.functionClass ||
                 (*altZero)->interfaceSubClass != association.functionSubClass ||
                 (*altZero)->interfaceProtocol != association.functionProtocol)) {
                addDiagnostic(diagnostics, DescriptorError::InterfaceAssociationMismatch,
                              association.sourceOffset, kInterfaceAssociationType,
                              "association class fields disagree with its interface");
            }
        }
    }

    return ValidationResult{std::move(diagnostics)};
}

ParseResult parseDescriptorImage(std::span<const std::uint8_t> data,
                                 const ParseOptions& options) {
    return Parser(data, options).parse();
}

ParseResult parseDeviceDescriptors(std::span<const std::uint8_t> data,
                                   const ParseOptions& options) {
    ParseOptions adjusted = options;
    adjusted.requireDeviceDescriptor = true;
    return Parser(data, adjusted).parse();
}

ParseResult parseConfigurationDescriptors(std::span<const std::uint8_t> data,
                                          const ParseOptions& options) {
    ParseOptions adjusted = options;
    adjusted.requireDeviceDescriptor = false;
    adjusted.requireConfiguration = true;
    return Parser(data, adjusted).parse();
}

HostDescriptorView::HostDescriptorView(DescriptorImage image, ParseOptions options)
    : image_(std::move(image)), options_(std::move(options)) {}

ImportResult HostDescriptorView::import(std::span<const std::uint8_t> data,
                                        const ParseOptions& options) {
    ParseResult parsed = parseDescriptorImage(data, options);
    if (!parsed.ok()) {
        return ImportResult{std::nullopt, std::move(parsed.diagnostics)};
    }

    HostDescriptorView view(std::move(*parsed.image), options);
    if (options.autoActivateFirstConfiguration && !view.image_.configurations.empty()) {
        SwitchResult switched =
            view.activateConfiguration(view.image_.configurations.front().value);
        if (!switched.ok) {
            return ImportResult{std::nullopt, std::move(switched.diagnostics)};
        }
    }
    return ImportResult{std::move(view), {}};
}

ImportResult importHostDescriptorView(std::span<const std::uint8_t> data,
                                      const ParseOptions& options) {
    return HostDescriptorView::import(data, options);
}

bool HostDescriptorView::buildActiveView(
    std::uint8_t configurationValue,
    const std::vector<std::uint8_t>& selectedAltSettings,
    std::uint64_t generation,
    ActiveView& result,
    std::vector<Diagnostic>& diagnostics) const {
    const ConfigurationDescriptor* configuration =
        image_.findConfiguration(configurationValue);
    if (configuration == nullptr) {
        addDiagnostic(diagnostics, DescriptorError::UnknownConfiguration, 0,
                      kConfigurationDescriptorType,
                      "configuration value is not present in the imported image");
        return false;
    }

    ActiveView candidate;
    candidate.configured = true;
    candidate.configurationValue = configurationValue;
    candidate.generation = generation;

    std::map<std::uint8_t, std::uint8_t> chosen;
    for (const auto& interfaceDescriptor : configuration->interfaces) {
        if (chosen.find(interfaceDescriptor.number) != chosen.end()) {
            continue;
        }
        if (interfaceDescriptor.number >= selectedAltSettings.size() ||
            selectedAltSettings[interfaceDescriptor.number] == 0xffu) {
            addDiagnostic(diagnostics, DescriptorError::UnknownAlternateSetting,
                          interfaceDescriptor.sourceOffset, kInterfaceDescriptorType,
                          "no alternate setting was selected for an interface");
            return false;
        }
        chosen.emplace(interfaceDescriptor.number,
                       selectedAltSettings[interfaceDescriptor.number]);
    }

    for (const auto& [number, alternate] : chosen) {
        const InterfaceDescriptor* interfaceDescriptor =
            configuration->findInterface(number, alternate);
        if (interfaceDescriptor == nullptr) {
            addDiagnostic(diagnostics, DescriptorError::UnknownAlternateSetting,
                          configuration->sourceOffset, kInterfaceDescriptorType,
                          "selected alternate setting does not exist");
            return false;
        }
        candidate.interfaces.push_back(ActiveInterfaceView{
            interfaceDescriptor->number,
            interfaceDescriptor->alternateSetting,
            interfaceDescriptor->interfaceClass,
            interfaceDescriptor->interfaceSubClass,
            interfaceDescriptor->interfaceProtocol,
            interfaceDescriptor->sourceOffset,
        });
        for (const auto& endpoint : interfaceDescriptor->endpoints) {
            candidate.endpoints.push_back(ActiveEndpointView{
                endpoint.interfaceNumber,
                endpoint.alternateSetting,
                endpoint.address,
                endpoint.attributes,
                endpoint.maxPacketSize,
                endpoint.interval,
                endpoint.sourceOffset,
            });
        }
    }

    std::set<std::uint8_t> activeAddresses;
    for (const auto& endpoint : candidate.endpoints) {
        if (!activeAddresses.insert(endpoint.address).second) {
            addDiagnostic(diagnostics, DescriptorError::EndpointAddressConflict,
                          endpoint.descriptorOffset, kEndpointDescriptorType,
                          "selected view would bind one endpoint address twice");
            return false;
        }
    }

    result = std::move(candidate);
    return true;
}

ActiveView HostDescriptorView::activeView() const {
    return active_;
}

std::vector<ActiveEndpointView> HostDescriptorView::activeEndpoints() const {
    return active_.endpoints;
}

std::vector<ActiveInterfaceView> HostDescriptorView::activeInterfaces() const {
    return active_.interfaces;
}

const ConfigurationDescriptor* HostDescriptorView::activeConfiguration() const noexcept {
    if (!active_.configured) {
        return nullptr;
    }
    return image_.findConfiguration(active_.configurationValue);
}

SwitchResult HostDescriptorView::activateConfiguration(
    std::uint8_t configurationValue) {
    lastDiagnostics_.clear();

    const ConfigurationDescriptor* configuration =
        image_.findConfiguration(configurationValue);
    if (configuration == nullptr) {
        addDiagnostic(lastDiagnostics_, DescriptorError::UnknownConfiguration, 0,
                      kConfigurationDescriptorType,
                      "configuration value is not present in the imported image");
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    const ValidationResult validation = validateConfiguration(*configuration, options_);
    if (!validation.ok()) {
        lastDiagnostics_ = validation.diagnostics;
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    std::vector<std::uint8_t> selected(256, 0xffu);
    for (const auto& interfaceDescriptor : configuration->interfaces) {
        selected[interfaceDescriptor.number] = 0;
    }

    ActiveView candidate;
    std::vector<Diagnostic> diagnostics;
    if (!buildActiveView(configurationValue, selected, active_.generation + 1,
                         candidate, diagnostics)) {
        lastDiagnostics_ = std::move(diagnostics);
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    active_ = std::move(candidate);
    return SwitchResult{true, active_.generation, active_.configurationValue, {}};
}

SwitchResult HostDescriptorView::selectAlternateSetting(
    std::uint8_t interfaceNumber, std::uint8_t alternateSetting) {
    lastDiagnostics_.clear();

    if (!active_.configured) {
        addDiagnostic(lastDiagnostics_, DescriptorError::NotConfigured, 0, 0,
                      "cannot select an alternate setting before configuration");
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    const ConfigurationDescriptor* configuration =
        image_.findConfiguration(active_.configurationValue);
    if (configuration == nullptr) {
        addDiagnostic(lastDiagnostics_, DescriptorError::UnknownConfiguration, 0,
                      kConfigurationDescriptorType,
                      "active configuration disappeared from the imported image");
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    if (configuration->findInterface(interfaceNumber, alternateSetting) == nullptr) {
        addDiagnostic(lastDiagnostics_, DescriptorError::UnknownAlternateSetting, 0,
                      kInterfaceDescriptorType,
                      "requested interface/alternate setting is not present");
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    const ValidationResult validation = validateConfiguration(*configuration, options_);
    if (!validation.ok()) {
        lastDiagnostics_ = validation.diagnostics;
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    std::vector<std::uint8_t> selected(256, 0xffu);
    for (const auto& activeInterface : active_.interfaces) {
        selected[activeInterface.number] = activeInterface.alternateSetting;
    }
    selected[interfaceNumber] = alternateSetting;

    ActiveView candidate;
    std::vector<Diagnostic> diagnostics;
    if (!buildActiveView(active_.configurationValue, selected,
                         active_.generation + 1, candidate, diagnostics)) {
        lastDiagnostics_ = std::move(diagnostics);
        return SwitchResult{false, active_.generation, active_.configurationValue,
                            lastDiagnostics_};
    }

    active_ = std::move(candidate);
    return SwitchResult{true, active_.generation, active_.configurationValue, {}};
}

SwitchResult HostDescriptorView::deactivate() {
    lastDiagnostics_.clear();
    ActiveView candidate;
    candidate.configured = false;
    candidate.generation = active_.generation + 1;
    active_ = std::move(candidate);
    return SwitchResult{true, active_.generation, 0, {}};
}

}  // namespace netsec::usb
