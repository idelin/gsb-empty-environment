#pragma once

#include <algorithm>
#include <cstddef>
#include <cstdint>
#include <optional>
#include <span>
#include <string>
#include <utility>
#include <vector>

namespace netsec::usb {

inline constexpr std::size_t kDeviceDescriptorLength = 18;
inline constexpr std::size_t kConfigurationDescriptorLength = 9;
inline constexpr std::size_t kInterfaceDescriptorLength = 9;
inline constexpr std::size_t kEndpointDescriptorLength = 7;
inline constexpr std::size_t kInterfaceAssociationLength = 8;

inline constexpr std::uint8_t kDeviceDescriptorType = 0x01;
inline constexpr std::uint8_t kConfigurationDescriptorType = 0x02;
inline constexpr std::uint8_t kInterfaceDescriptorType = 0x04;
inline constexpr std::uint8_t kEndpointDescriptorType = 0x05;
inline constexpr std::uint8_t kInterfaceAssociationType = 0x0b;

enum class DescriptorType : std::uint8_t {
    Device = kDeviceDescriptorType,
    Configuration = kConfigurationDescriptorType,
    String = 0x03,
    Interface = kInterfaceDescriptorType,
    Endpoint = kEndpointDescriptorType,
    DeviceQualifier = 0x06,
    OtherSpeedConfiguration = 0x07,
    InterfacePower = 0x08,
    InterfaceAssociation = kInterfaceAssociationType,
};

enum class TransferType : std::uint8_t {
    Control = 0,
    Isochronous = 1,
    Bulk = 2,
    Interrupt = 3,
};

enum class UsbSpeed : std::uint8_t {
    Unknown = 0,
    Low = 1,
    Full = 2,
    High = 3,
    Super = 4,
    SuperPlus = 5,
};

enum class DescriptorError {
    None = 0,
    EmptyInput,
    TruncatedDescriptor,
    InvalidDescriptorLength,
    UnexpectedDescriptorType,
    MisplacedDescriptor,
    DescriptorOverrun,
    DescriptorTooLarge,
    TooManyDescriptors,
    TrailingData,
    MissingConfiguration,
    InvalidDeviceDescriptor,
    InvalidDeviceMaxPacket,
    ConfigurationCountMismatch,
    InvalidConfigurationDescriptor,
    InvalidConfigurationValue,
    InvalidConfigurationAttributes,
    InvalidConfigurationLength,
    DuplicateConfigurationValue,
    InvalidInterfaceNumber,
    DuplicateAlternateSetting,
    MissingAlternateSetting,
    InterfaceEndpointCountMismatch,
    InterfaceClassMismatch,
    InvalidEndpointDescriptor,
    InvalidEndpointAddress,
    ZeroEndpointAddress,
    DuplicateEndpointAddress,
    EndpointAddressConflict,
    InvalidTransferType,
    InvalidMaxPacketSize,
    InvalidEndpointInterval,
    InterfaceAssociationBounds,
    InterfaceAssociationOverlap,
    InterfaceAssociationMismatch,
    UnknownConfiguration,
    UnknownAlternateSetting,
    NotConfigured,
};

const char* to_string(DescriptorError error) noexcept;

struct Diagnostic {
    DescriptorError error = DescriptorError::None;
    std::size_t offset = 0;
    std::uint8_t descriptorType = 0;
    std::string message;

    friend bool operator==(const Diagnostic&, const Diagnostic&) = default;
};

struct ValidationResult {
    std::vector<Diagnostic> diagnostics;

    [[nodiscard]] bool ok() const noexcept {
        return diagnostics.empty();
    }
    [[nodiscard]] explicit operator bool() const noexcept {
        return ok();
    }
};

struct DeviceDescriptor {
    std::uint16_t bcdUsb = 0;
    std::uint8_t deviceClass = 0;
    std::uint8_t deviceSubClass = 0;
    std::uint8_t deviceProtocol = 0;
    std::uint8_t maxPacketSize0 = 0;
    std::uint16_t vendorId = 0;
    std::uint16_t productId = 0;
    std::uint16_t bcdDevice = 0;
    std::uint8_t iManufacturer = 0;
    std::uint8_t iProduct = 0;
    std::uint8_t iSerialNumber = 0;
    std::uint8_t numConfigurations = 0;
    std::size_t sourceOffset = 0;
};

struct EndpointDescriptor {
    std::uint8_t address = 0;
    std::uint8_t attributes = 0;
    std::uint16_t maxPacketSize = 0;
    std::uint8_t interval = 0;
    std::uint8_t interfaceNumber = 0;
    std::uint8_t alternateSetting = 0;
    std::size_t sourceOffset = 0;

    [[nodiscard]] TransferType transferType() const noexcept {
        return static_cast<TransferType>(attributes & 0x03u);
    }
    [[nodiscard]] std::uint8_t endpointNumber() const noexcept {
        return static_cast<std::uint8_t>(address & 0x0fu);
    }
    [[nodiscard]] bool isIn() const noexcept {
        return (address & 0x80u) != 0;
    }
    [[nodiscard]] std::uint16_t packetPayloadSize() const noexcept {
        return static_cast<std::uint16_t>(maxPacketSize & 0x07ffu);
    }
    [[nodiscard]] std::uint8_t transactionOpportunities() const noexcept {
        return static_cast<std::uint8_t>((maxPacketSize >> 11u) & 0x03u);
    }
};

struct InterfaceDescriptor {
    std::uint8_t number = 0;
    std::uint8_t alternateSetting = 0;
    std::uint8_t interfaceClass = 0;
    std::uint8_t interfaceSubClass = 0;
    std::uint8_t interfaceProtocol = 0;
    std::uint8_t iInterface = 0;
    std::uint8_t declaredEndpointCount = 0;
    std::vector<EndpointDescriptor> endpoints;
    std::size_t sourceOffset = 0;

    [[nodiscard]] const EndpointDescriptor* findEndpoint(std::uint8_t address) const noexcept {
        for (const auto& endpoint : endpoints) {
            if (endpoint.address == address) {
                return &endpoint;
            }
        }
        return nullptr;
    }
};

struct InterfaceAssociationDescriptor {
    std::uint8_t firstInterface = 0;
    std::uint8_t interfaceCount = 0;
    std::uint8_t functionClass = 0;
    std::uint8_t functionSubClass = 0;
    std::uint8_t functionProtocol = 0;
    std::uint8_t iFunction = 0;
    std::size_t sourceOffset = 0;
};

struct ConfigurationDescriptor {
    std::uint8_t value = 0;
    std::uint8_t attributes = 0;
    std::uint8_t maxPower = 0;
    std::uint16_t totalLength = 0;
    std::uint8_t declaredInterfaceCount = 0;
    std::uint8_t iConfiguration = 0;
    std::vector<InterfaceAssociationDescriptor> associations;
    std::vector<InterfaceDescriptor> interfaces;
    std::size_t sourceOffset = 0;

    [[nodiscard]] const InterfaceDescriptor* findInterface(std::uint8_t number,
                                                          std::uint8_t alternateSetting) const noexcept {
        for (const auto& interfaceDescriptor : interfaces) {
            if (interfaceDescriptor.number == number &&
                interfaceDescriptor.alternateSetting == alternateSetting) {
                return &interfaceDescriptor;
            }
        }
        return nullptr;
    }

    [[nodiscard]] std::vector<const InterfaceDescriptor*> alternateSettings(
        std::uint8_t number) const {
        std::vector<const InterfaceDescriptor*> result;
        for (const auto& interfaceDescriptor : interfaces) {
            if (interfaceDescriptor.number == number) {
                result.push_back(&interfaceDescriptor);
            }
        }
        std::sort(result.begin(), result.end(),
                  [](const InterfaceDescriptor* lhs, const InterfaceDescriptor* rhs) {
                      return lhs->alternateSetting < rhs->alternateSetting;
                  });
        return result;
    }
};

struct DescriptorImage {
    std::optional<DeviceDescriptor> device;
    std::vector<ConfigurationDescriptor> configurations;
    std::size_t descriptorCount = 0;
    std::size_t byteCount = 0;

    [[nodiscard]] const ConfigurationDescriptor* findConfiguration(std::uint8_t value) const noexcept {
        for (const auto& configuration : configurations) {
            if (configuration.value == value) {
                return &configuration;
            }
        }
        return nullptr;
    }
};

struct ParseOptions {
    UsbSpeed speed = UsbSpeed::Unknown;
    std::size_t maxImageBytes = 1u << 20;
    std::size_t maxConfigurationLength = 0xffffu;
    std::size_t maxDescriptors = 4096;
    bool requireDeviceDescriptor = false;
    bool requireConfiguration = false;
    bool requireAllConfigurations = false;
    bool requireAlternateSettingZero = true;
    bool requireContiguousInterfaces = true;
    bool requireAlternateClassMatch = true;
    bool requireAssociationClassMatch = false;
    bool allowClassSpecificDescriptors = true;
    bool allowUnknownDescriptors = false;
    bool autoActivateFirstConfiguration = true;
};

struct ParseResult {
    std::optional<DescriptorImage> image;
    std::vector<Diagnostic> diagnostics;

    [[nodiscard]] bool ok() const noexcept {
        return image.has_value() && diagnostics.empty();
    }
    [[nodiscard]] explicit operator bool() const noexcept {
        return ok();
    }
    [[nodiscard]] const DeviceDescriptor* device() const noexcept {
        return image ? (image->device ? &*image->device : nullptr) : nullptr;
    }
    [[nodiscard]] const ConfigurationDescriptor* configuration(std::uint8_t value) const noexcept {
        return image ? image->findConfiguration(value) : nullptr;
    }
};

ParseResult parseDescriptorImage(std::span<const std::uint8_t> data,
                                 const ParseOptions& options = {});
ParseResult parseDeviceDescriptors(std::span<const std::uint8_t> data,
                                   const ParseOptions& options = {});
ParseResult parseConfigurationDescriptors(std::span<const std::uint8_t> data,
                                          const ParseOptions& options = {});
ValidationResult validateConfiguration(const ConfigurationDescriptor& configuration,
                                       const ParseOptions& options = {});

struct ActiveInterfaceView {
    std::uint8_t number = 0;
    std::uint8_t alternateSetting = 0;
    std::uint8_t interfaceClass = 0;
    std::uint8_t interfaceSubClass = 0;
    std::uint8_t interfaceProtocol = 0;
    std::size_t descriptorOffset = 0;

    friend bool operator==(const ActiveInterfaceView&, const ActiveInterfaceView&) = default;
};

struct ActiveEndpointView {
    std::uint8_t interfaceNumber = 0;
    std::uint8_t alternateSetting = 0;
    std::uint8_t address = 0;
    std::uint8_t attributes = 0;
    std::uint16_t maxPacketSize = 0;
    std::uint8_t interval = 0;
    std::size_t descriptorOffset = 0;

    [[nodiscard]] TransferType transferType() const noexcept {
        return static_cast<TransferType>(attributes & 0x03u);
    }
    [[nodiscard]] std::uint8_t endpointNumber() const noexcept {
        return static_cast<std::uint8_t>(address & 0x0fu);
    }
    [[nodiscard]] bool isIn() const noexcept {
        return (address & 0x80u) != 0;
    }
    [[nodiscard]] std::uint16_t packetPayloadSize() const noexcept {
        return static_cast<std::uint16_t>(maxPacketSize & 0x07ffu);
    }

    friend bool operator==(const ActiveEndpointView&, const ActiveEndpointView&) = default;
};

struct ActiveView {
    bool configured = false;
    std::uint8_t configurationValue = 0;
    std::uint64_t generation = 0;
    std::vector<ActiveInterfaceView> interfaces;
    std::vector<ActiveEndpointView> endpoints;
};

struct SwitchResult {
    bool ok = false;
    std::uint64_t generation = 0;
    std::uint8_t configurationValue = 0;
    std::vector<Diagnostic> diagnostics;
};

struct ImportResult;

class HostDescriptorView {
public:
    [[nodiscard]] static ImportResult import(std::span<const std::uint8_t> data,
                                             const ParseOptions& options = {});

    HostDescriptorView(HostDescriptorView&&) noexcept = default;
    HostDescriptorView& operator=(HostDescriptorView&&) noexcept = default;
    HostDescriptorView(const HostDescriptorView&) = delete;
    HostDescriptorView& operator=(const HostDescriptorView&) = delete;

    [[nodiscard]] const DescriptorImage& image() const noexcept {
        return image_;
    }
    [[nodiscard]] const DeviceDescriptor* device() const noexcept {
        return image_.device ? &*image_.device : nullptr;
    }
    [[nodiscard]] std::span<const ConfigurationDescriptor> configurations() const noexcept {
        return image_.configurations;
    }
    [[nodiscard]] const ConfigurationDescriptor* findConfiguration(std::uint8_t value) const noexcept {
        return image_.findConfiguration(value);
    }
    [[nodiscard]] const ParseOptions& options() const noexcept {
        return options_;
    }

    [[nodiscard]] bool configured() const noexcept {
        return active_.configured;
    }
    [[nodiscard]] std::uint8_t activeConfigurationValue() const noexcept {
        return active_.configurationValue;
    }
    [[nodiscard]] std::uint64_t generation() const noexcept {
        return active_.generation;
    }
    [[nodiscard]] bool isCurrent(const ActiveView& view) const noexcept {
        return view.generation == active_.generation &&
               view.configured == active_.configured &&
               (!active_.configured ||
                view.configurationValue == active_.configurationValue);
    }

    // Returns a validated snapshot by value. A caller cannot mutate the host's
    // canonical view, and an old snapshot is never silently reinterpreted.
    [[nodiscard]] ActiveView activeView() const;
    [[nodiscard]] std::vector<ActiveEndpointView> activeEndpoints() const;
    [[nodiscard]] std::vector<ActiveInterfaceView> activeInterfaces() const;
    [[nodiscard]] const ConfigurationDescriptor* activeConfiguration() const noexcept;
    [[nodiscard]] std::optional<ActiveEndpointView> activeEndpoint(
        std::uint8_t address) const {
        for (const auto& endpoint : active_.endpoints) {
            if (endpoint.address == address) {
                return endpoint;
            }
        }
        return std::nullopt;
    }
    [[nodiscard]] std::optional<ActiveInterfaceView> activeInterface(
        std::uint8_t number) const {
        for (const auto& interfaceView : active_.interfaces) {
            if (interfaceView.number == number) {
                return interfaceView;
            }
        }
        return std::nullopt;
    }

    // Every switch revalidates the selected configuration and the resulting
    // endpoint set. A failed switch leaves the previous view untouched.
    SwitchResult activateConfiguration(std::uint8_t configurationValue);
    SwitchResult selectAlternateSetting(std::uint8_t interfaceNumber,
                                        std::uint8_t alternateSetting);
    SwitchResult deactivate();

    [[nodiscard]] const std::vector<Diagnostic>& lastDiagnostics() const noexcept {
        return lastDiagnostics_;
    }

private:
    HostDescriptorView(DescriptorImage image, ParseOptions options);

    bool buildActiveView(std::uint8_t configurationValue,
                         const std::vector<std::uint8_t>& selectedAltSettings,
                         std::uint64_t generation,
                         ActiveView& result,
                         std::vector<Diagnostic>& diagnostics) const;

    DescriptorImage image_;
    ParseOptions options_;
    ActiveView active_;
    std::vector<Diagnostic> lastDiagnostics_;
};

struct ImportResult {
    std::optional<HostDescriptorView> view;
    std::vector<Diagnostic> diagnostics;

    [[nodiscard]] bool ok() const noexcept {
        return view.has_value() && diagnostics.empty();
    }
    [[nodiscard]] explicit operator bool() const noexcept {
        return ok();
    }
};

ImportResult importHostDescriptorView(std::span<const std::uint8_t> data,
                                      const ParseOptions& options = {});

}  // namespace netsec::usb

namespace netsec {

namespace usbdesc = usb;

using usb::ActiveEndpointView;
using usb::ActiveInterfaceView;
using usb::ActiveView;
using usb::ConfigurationDescriptor;
using usb::DescriptorError;
using usb::DescriptorImage;
using usb::DeviceDescriptor;
using usb::Diagnostic;
using usb::EndpointDescriptor;
using usb::HostDescriptorView;
using usb::ImportResult;
using usb::InterfaceAssociationDescriptor;
using usb::InterfaceDescriptor;
using usb::ParseOptions;
using usb::ParseResult;
using usb::SwitchResult;
using usb::UsbSpeed;
using usb::ValidationResult;
using usb::importHostDescriptorView;
using usb::parseConfigurationDescriptors;
using usb::parseDescriptorImage;
using usb::parseDeviceDescriptors;
using usb::validateConfiguration;

inline ParseResult parseUsbDescriptors(std::span<const std::uint8_t> data,
                                       const ParseOptions& options = {}) {
    return usb::parseDescriptorImage(data, options);
}

inline ValidationResult validateUsbConfiguration(
    const ConfigurationDescriptor& configuration,
    const ParseOptions& options = {}) {
    return usb::validateConfiguration(configuration, options);
}

}  // namespace netsec
