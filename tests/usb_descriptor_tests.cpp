#include "netsec.hpp"

#include <algorithm>
#include <cstdint>
#include <iostream>
#include <string>
#include <string_view>
#include <utility>
#include <vector>

namespace {

using netsec::usb::DescriptorError;
using Bytes = std::vector<std::uint8_t>;

int failures = 0;

void check(bool condition, std::string_view expression, int line) {
    if (!condition) {
        std::cerr << "FAIL line " << line << ": " << expression << '\n';
        ++failures;
    }
}

#define CHECK(expression) check((expression), #expression, __LINE__)

void append(Bytes& destination, const Bytes& source) {
    destination.insert(destination.end(), source.begin(), source.end());
}

void patchLe16(Bytes& bytes, std::size_t offset, std::uint16_t value) {
    bytes[offset] = static_cast<std::uint8_t>(value & 0xffu);
    bytes[offset + 1] = static_cast<std::uint8_t>((value >> 8u) & 0xffu);
}

Bytes deviceDescriptor(std::uint8_t configurationCount = 1) {
    return Bytes{
        18, 0x01,        // bLength, DEVICE
        0x00, 0x02,      // bcdUSB 2.00
        0xef, 0x02, 0x01,// class/subclass/protocol (misc composite)
        64,              // bMaxPacketSize0
        0x34, 0x12,      // idVendor
        0x78, 0x56,      // idProduct
        0x00, 0x01,      // bcdDevice
        1, 2, 3,         // string indexes
        configurationCount,
    };
}

Bytes interfaceDescriptor(std::uint8_t number,
                          std::uint8_t alternate,
                          std::uint8_t endpointCount,
                          std::uint8_t interfaceClass = 0xff,
                          std::uint8_t subClass = 1,
                          std::uint8_t protocol = 1) {
    return Bytes{9, 0x04, number, alternate, endpointCount,
                 interfaceClass, subClass, protocol, 0};
}

Bytes endpointDescriptor(std::uint8_t address,
                         std::uint8_t attributes,
                         std::uint16_t maxPacketSize,
                         std::uint8_t interval) {
    return Bytes{7, 0x05, address, attributes,
                 static_cast<std::uint8_t>(maxPacketSize & 0xffu),
                 static_cast<std::uint8_t>((maxPacketSize >> 8u) & 0xffu),
                 interval};
}

Bytes interfaceAssociation(std::uint8_t firstInterface,
                           std::uint8_t count,
                           std::uint8_t functionClass = 0xff,
                           std::uint8_t subClass = 1,
                           std::uint8_t protocol = 1) {
    return Bytes{8, 0x0b, firstInterface, count, functionClass, subClass, protocol, 0};
}

Bytes configurationSet(std::uint8_t value,
                       std::uint8_t interfaceCount,
                       const Bytes& body) {
    Bytes result{9, 0x02, 0, 0, interfaceCount, value, 0, 0x80, 50};
    append(result, body);
    const auto total = static_cast<std::uint16_t>(result.size());
    patchLe16(result, 2, total);
    return result;
}

Bytes legalCompositeConfiguration() {
    Bytes body;
    append(body, interfaceAssociation(0, 2, 0xff, 1, 1));

    append(body, interfaceDescriptor(0, 0, 1));
    append(body, Bytes{5, 0x24, 0x01, 0x02, 0x03});  // class-specific interface
    append(body, endpointDescriptor(0x81, 0x03, 64, 10));

    append(body, interfaceDescriptor(0, 1, 1));
    append(body, endpointDescriptor(0x86, 0x03, 64, 10));

    append(body, interfaceDescriptor(1, 0, 1));
    append(body, endpointDescriptor(0x02, 0x02, 64, 0));

    append(body, interfaceAssociation(2, 1, 0xff, 2, 2));
    append(body, interfaceDescriptor(2, 0, 1, 0xff, 2, 2));
    append(body, endpointDescriptor(0x83, 0x03, 64, 10));

    return configurationSet(1, 3, body);
}

Bytes alternateConfiguration() {
    Bytes body;
    append(body, interfaceDescriptor(0, 0, 1, 0xff, 3, 3));
    append(body, endpointDescriptor(0x84, 0x02, 64, 0));
    return configurationSet(2, 1, body);
}

bool hasError(const std::vector<netsec::usb::Diagnostic>& diagnostics,
              DescriptorError error) {
    return std::any_of(diagnostics.begin(), diagnostics.end(),
                       [error](const netsec::usb::Diagnostic& diagnostic) {
                           return diagnostic.error == error;
                       });
}

bool parseFails(const Bytes& bytes, DescriptorError error,
                const netsec::usb::ParseOptions& options = {}) {
    const auto result = netsec::usb::parseDescriptorImage(bytes, options);
    return !result.ok() && hasError(result.diagnostics, error);
}

bool containsAddress(const std::vector<netsec::usb::ActiveEndpointView>& endpoints,
                     std::uint8_t address) {
    return std::any_of(endpoints.begin(), endpoints.end(),
                       [address](const netsec::usb::ActiveEndpointView& endpoint) {
                           return endpoint.address == address;
                       });
}

void testLegalCompositeAndConfigurationSwitch() {
    Bytes image = deviceDescriptor(2);
    append(image, legalCompositeConfiguration());
    append(image, alternateConfiguration());

    auto parsed = netsec::usb::parseDescriptorImage(image);
    CHECK(parsed.ok());
    CHECK(netsec::parseUsbDescriptors(image).ok());
    CHECK(parsed.image.has_value());
    CHECK(parsed.image->configurations.size() == 2);
    CHECK(parsed.image->configurations[0].interfaces.size() == 4);
    CHECK(parsed.image->configurations[0].associations.size() == 2);

    auto imported = netsec::usb::HostDescriptorView::import(image);
    CHECK(imported.ok());
    CHECK(imported.view.has_value());
    if (!imported.view) {
        return;
    }

    auto host = std::move(*imported.view);
    CHECK(host.configured());
    CHECK(host.activeConfigurationValue() == 1);
    CHECK(host.activeInterfaces().size() == 3);
    CHECK(host.activeEndpoints().size() == 3);
    CHECK(containsAddress(host.activeEndpoints(), 0x81));
    CHECK(containsAddress(host.activeEndpoints(), 0x02));
    CHECK(containsAddress(host.activeEndpoints(), 0x83));
    CHECK(!containsAddress(host.activeEndpoints(), 0x84));

    const std::uint64_t firstGeneration = host.generation();
    const auto oldSnapshot = host.activeView();
    CHECK(host.isCurrent(oldSnapshot));

    auto alternate = host.selectAlternateSetting(0, 1);
    CHECK(alternate.ok);
    CHECK(host.generation() == firstGeneration + 1);
    CHECK(host.activeInterfaces().size() == 3);
    CHECK(host.activeEndpoints().size() == 3);
    CHECK(!containsAddress(host.activeEndpoints(), 0x81));
    CHECK(containsAddress(host.activeEndpoints(), 0x86));
    CHECK(containsAddress(host.activeEndpoints(), 0x02));
    CHECK(containsAddress(host.activeEndpoints(), 0x83));

    // Snapshots are values: the old view is not quietly mutated.
    CHECK(oldSnapshot.generation == firstGeneration);
    CHECK(containsAddress(oldSnapshot.endpoints, 0x81));
    CHECK(!host.isCurrent(oldSnapshot));

    const std::uint64_t alternateGeneration = host.generation();
    auto switched = host.activateConfiguration(2);
    CHECK(switched.ok);
    CHECK(host.activeConfigurationValue() == 2);
    CHECK(host.generation() == alternateGeneration + 1);
    CHECK(host.activeInterfaces().size() == 1);
    CHECK(host.activeEndpoints().size() == 1);
    CHECK(containsAddress(host.activeEndpoints(), 0x84));
    CHECK(!containsAddress(host.activeEndpoints(), 0x86));
    CHECK(host.activeEndpoint(0x84).has_value());
    CHECK(!host.activeEndpoint(0x86).has_value());

    const auto beforeFailure = host.activeEndpoints();
    const std::uint64_t beforeFailureGeneration = host.generation();
    auto badConfiguration = host.activateConfiguration(99);
    CHECK(!badConfiguration.ok);
    CHECK(hasError(badConfiguration.diagnostics, DescriptorError::UnknownConfiguration));
    CHECK(host.generation() == beforeFailureGeneration);
    CHECK(host.activeEndpoints() == beforeFailure);

    auto badAlternate = host.selectAlternateSetting(0, 7);
    CHECK(!badAlternate.ok);
    CHECK(hasError(badAlternate.diagnostics, DescriptorError::UnknownAlternateSetting));
    CHECK(host.generation() == beforeFailureGeneration);
    CHECK(host.activeEndpoints() == beforeFailure);
}

void testDualViewDescriptorRejected() {
    Bytes bodyOne;
    append(bodyOne, interfaceDescriptor(0, 0, 1));
    append(bodyOne, endpointDescriptor(0x81, 0x03, 64, 4));

    Bytes bodyTwo;
    append(bodyTwo, interfaceDescriptor(0, 0, 1));
    append(bodyTwo, endpointDescriptor(0x82, 0x03, 64, 4));

    Bytes image = deviceDescriptor(2);
    append(image, configurationSet(1, 1, bodyOne));
    append(image, configurationSet(1, 1, bodyTwo));

    CHECK(parseFails(image, DescriptorError::DuplicateConfigurationValue));
    auto imported = netsec::usb::HostDescriptorView::import(image);
    CHECK(!imported.ok());
}

void testAlternateSettingConfusionRejected() {
    Bytes classMismatchBody;
    append(classMismatchBody, interfaceDescriptor(0, 0, 1, 0xff, 1, 1));
    append(classMismatchBody, endpointDescriptor(0x81, 0x03, 64, 4));
    append(classMismatchBody, interfaceDescriptor(0, 1, 1, 0x03, 0, 0));
    append(classMismatchBody, endpointDescriptor(0x81, 0x03, 64, 4));
    const Bytes classMismatch = configurationSet(1, 1, classMismatchBody);
    CHECK(parseFails(classMismatch, DescriptorError::InterfaceClassMismatch));

    Bytes countMismatchBody;
    append(countMismatchBody, interfaceDescriptor(0, 0, 1));
    append(countMismatchBody, endpointDescriptor(0x81, 0x03, 64, 4));
    append(countMismatchBody, interfaceDescriptor(0, 1, 2));
    append(countMismatchBody, endpointDescriptor(0x82, 0x03, 64, 4));
    const Bytes countMismatch = configurationSet(1, 1, countMismatchBody);
    CHECK(parseFails(countMismatch, DescriptorError::InterfaceEndpointCountMismatch));

    Bytes missingZeroBody;
    append(missingZeroBody, interfaceDescriptor(0, 1, 1));
    append(missingZeroBody, endpointDescriptor(0x81, 0x03, 64, 4));
    const Bytes missingZero = configurationSet(1, 1, missingZeroBody);
    CHECK(parseFails(missingZero, DescriptorError::MissingAlternateSetting));

    Bytes altConflictBody;
    append(altConflictBody, interfaceDescriptor(0, 0, 1));
    append(altConflictBody, endpointDescriptor(0x81, 0x03, 64, 4));
    append(altConflictBody, interfaceDescriptor(0, 1, 1));
    append(altConflictBody, endpointDescriptor(0x02, 0x02, 64, 0));
    append(altConflictBody, interfaceDescriptor(1, 0, 1, 0xff, 2, 2));
    append(altConflictBody, endpointDescriptor(0x02, 0x02, 64, 0));
    const Bytes altConflict = configurationSet(1, 2, altConflictBody);
    CHECK(parseFails(altConflict, DescriptorError::EndpointAddressConflict));
}

void testEndpointConflictsAndSizes() {
    Bytes duplicateBody;
    append(duplicateBody, interfaceDescriptor(0, 0, 2));
    append(duplicateBody, endpointDescriptor(0x81, 0x03, 64, 4));
    append(duplicateBody, endpointDescriptor(0x81, 0x03, 64, 4));
    const Bytes duplicate = configurationSet(1, 1, duplicateBody);
    CHECK(parseFails(duplicate, DescriptorError::DuplicateEndpointAddress));

    Bytes crossInterfaceBody;
    append(crossInterfaceBody, interfaceDescriptor(0, 0, 1));
    append(crossInterfaceBody, endpointDescriptor(0x81, 0x03, 64, 4));
    append(crossInterfaceBody, interfaceDescriptor(1, 0, 1, 0xff, 2, 2));
    append(crossInterfaceBody, endpointDescriptor(0x81, 0x02, 64, 0));
    const Bytes crossInterface = configurationSet(1, 2, crossInterfaceBody);
    CHECK(parseFails(crossInterface, DescriptorError::EndpointAddressConflict));

    Bytes reservedAddressBody;
    append(reservedAddressBody, interfaceDescriptor(0, 0, 1));
    append(reservedAddressBody, endpointDescriptor(0x91, 0x03, 64, 4));
    const Bytes reservedAddress = configurationSet(1, 1, reservedAddressBody);
    CHECK(parseFails(reservedAddress, DescriptorError::InvalidEndpointAddress));

    Bytes zeroAddressBody;
    append(zeroAddressBody, interfaceDescriptor(0, 0, 1));
    append(zeroAddressBody, endpointDescriptor(0x00, 0x03, 64, 4));
    const Bytes zeroAddress = configurationSet(1, 1, zeroAddressBody);
    CHECK(parseFails(zeroAddress, DescriptorError::ZeroEndpointAddress));

    Bytes bigBulkBody;
    append(bigBulkBody, interfaceDescriptor(0, 0, 1));
    append(bigBulkBody, endpointDescriptor(0x01, 0x02, 512, 0));
    const Bytes bigBulk = configurationSet(1, 1, bigBulkBody);
    netsec::usb::ParseOptions fullSpeed;
    fullSpeed.speed = netsec::usb::UsbSpeed::Full;
    CHECK(parseFails(bigBulk, DescriptorError::InvalidMaxPacketSize, fullSpeed));
    CHECK(netsec::usb::parseDescriptorImage(bigBulk).ok());

    Bytes zeroPacketBody;
    append(zeroPacketBody, interfaceDescriptor(0, 0, 1));
    append(zeroPacketBody, endpointDescriptor(0x81, 0x03, 0, 4));
    const Bytes zeroPacket = configurationSet(1, 1, zeroPacketBody);
    CHECK(parseFails(zeroPacket, DescriptorError::InvalidMaxPacketSize));

    Bytes controlEndpointBody;
    append(controlEndpointBody, interfaceDescriptor(0, 0, 1));
    append(controlEndpointBody, endpointDescriptor(0x81, 0x00, 64, 4));
    const Bytes controlEndpoint = configurationSet(1, 1, controlEndpointBody);
    CHECK(parseFails(controlEndpoint, DescriptorError::InvalidTransferType));

    Bytes highBandwidthBody;
    append(highBandwidthBody, interfaceDescriptor(0, 0, 1));
    append(highBandwidthBody, endpointDescriptor(0x81, 0x03, 0x0c40, 4));
    const Bytes highBandwidth = configurationSet(1, 1, highBandwidthBody);
    CHECK(parseFails(highBandwidth, DescriptorError::InvalidMaxPacketSize, fullSpeed));

    netsec::usb::ParseOptions highSpeed;
    highSpeed.speed = netsec::usb::UsbSpeed::High;
    Bytes legalHighSpeedBody;
    append(legalHighSpeedBody, interfaceDescriptor(0, 0, 1));
    append(legalHighSpeedBody, endpointDescriptor(0x81, 0x03, 0x1400, 4));
    const Bytes legalHighSpeed = configurationSet(1, 1, legalHighSpeedBody);
    CHECK(netsec::usb::parseDescriptorImage(legalHighSpeed, highSpeed).ok());
}

void testDescriptorBoundariesAndOverlap() {
    Bytes beforeInterfaceBody;
    append(beforeInterfaceBody, endpointDescriptor(0x81, 0x03, 64, 4));
    append(beforeInterfaceBody, interfaceDescriptor(0, 0, 0));
    const Bytes beforeInterface = configurationSet(1, 1, beforeInterfaceBody);
    CHECK(parseFails(beforeInterface, DescriptorError::MisplacedDescriptor));

    Bytes zeroLengthBody{0, 0x04};
    append(zeroLengthBody, interfaceDescriptor(0, 0, 0));
    const Bytes zeroLength = configurationSet(1, 1, zeroLengthBody);
    CHECK(parseFails(zeroLength, DescriptorError::InvalidDescriptorLength));

    Bytes wrongInterfaceLengthBody{8, 0x04, 0, 0, 0, 0xff, 1, 1};
    const Bytes wrongInterfaceLength = configurationSet(1, 1, wrongInterfaceLengthBody);
    CHECK(parseFails(wrongInterfaceLength, DescriptorError::InvalidDescriptorLength));

    Bytes goodBody;
    append(goodBody, interfaceDescriptor(0, 0, 1));
    append(goodBody, endpointDescriptor(0x81, 0x03, 64, 4));
    Bytes truncated = configurationSet(1, 1, goodBody);
    patchLe16(truncated, 2, static_cast<std::uint16_t>(truncated.size() - 1));
    CHECK(parseFails(truncated, DescriptorError::DescriptorOverrun));

    Bytes tooLarge = configurationSet(1, 1, goodBody);
    patchLe16(tooLarge, 2, static_cast<std::uint16_t>(tooLarge.size() + 20));
    CHECK(parseFails(tooLarge, DescriptorError::DescriptorOverrun));

    netsec::usb::ParseOptions tinyConfiguration;
    tinyConfiguration.maxConfigurationLength = 12;
    CHECK(parseFails(tooLarge, DescriptorError::DescriptorTooLarge, tinyConfiguration));

    netsec::usb::ParseOptions tinyImage;
    tinyImage.maxImageBytes = 10;
    CHECK(parseFails(configurationSet(1, 1, goodBody), DescriptorError::DescriptorTooLarge,
                     tinyImage));

    Bytes overlapBody;
    append(overlapBody, interfaceAssociation(0, 2));
    append(overlapBody, interfaceAssociation(1, 1));
    append(overlapBody, interfaceDescriptor(0, 0, 0));
    append(overlapBody, interfaceDescriptor(1, 0, 0));
    const Bytes overlap = configurationSet(1, 2, overlapBody);
    CHECK(parseFails(overlap, DescriptorError::InterfaceAssociationOverlap));

    Bytes lateAssociationBody;
    append(lateAssociationBody, interfaceDescriptor(0, 0, 0));
    append(lateAssociationBody, interfaceAssociation(0, 1));
    const Bytes lateAssociation = configurationSet(1, 1, lateAssociationBody);
    CHECK(parseFails(lateAssociation, DescriptorError::MisplacedDescriptor));

    Bytes trailing = configurationSet(1, 1, goodBody);
    patchLe16(trailing, 2, 9);
    CHECK(!netsec::usb::parseDescriptorImage(trailing).ok());

    Bytes badDevice = deviceDescriptor(1);
    badDevice[0] = 17;
    CHECK(parseFails(badDevice, DescriptorError::InvalidDescriptorLength));

    Bytes countBody;
    append(countBody, interfaceDescriptor(0, 0, 0));
    Bytes countMismatch = deviceDescriptor(2);
    append(countMismatch, configurationSet(1, 1, countBody));
    netsec::usb::ParseOptions requireAll;
    requireAll.requireAllConfigurations = true;
    CHECK(parseFails(countMismatch, DescriptorError::ConfigurationCountMismatch, requireAll));
}

void testImportedViewIsAnImmutableCopy() {
    Bytes body;
    append(body, interfaceDescriptor(0, 0, 1));
    append(body, endpointDescriptor(0x81, 0x03, 64, 4));
    Bytes image = configurationSet(1, 1, body);

    std::size_t endpointAddressOffset = 0;
    for (std::size_t i = 0; i + 1 < image.size(); ++i) {
        if (image[i] == 7 && image[i + 1] == 0x05) {
            endpointAddressOffset = i + 2;
            break;
        }
    }
    CHECK(endpointAddressOffset != 0);

    auto imported = netsec::usb::HostDescriptorView::import(image);
    CHECK(imported.ok());
    if (!imported.view) {
        return;
    }
    auto host = std::move(*imported.view);
    CHECK(containsAddress(host.activeEndpoints(), 0x81));

    image[endpointAddressOffset] = 0x82;
    CHECK(containsAddress(host.activeEndpoints(), 0x81));
    CHECK(!containsAddress(host.activeEndpoints(), 0x82));

    auto switcher = host.selectAlternateSetting(0, 0);
    CHECK(switcher.ok);
    CHECK(containsAddress(host.activeEndpoints(), 0x81));
}

void testAssociationClassOption() {
    Bytes body;
    append(body, interfaceAssociation(0, 1, 0xff, 9, 9));
    append(body, interfaceDescriptor(0, 0, 0, 0xff, 1, 1));
    const Bytes image = configurationSet(1, 1, body);

    CHECK(netsec::usb::parseDescriptorImage(image).ok());

    netsec::usb::ParseOptions strict;
    strict.requireAssociationClassMatch = true;
    CHECK(parseFails(image, DescriptorError::InterfaceAssociationMismatch, strict));
}

}  // namespace

int main() {
    testLegalCompositeAndConfigurationSwitch();
    testDualViewDescriptorRejected();
    testAlternateSettingConfusionRejected();
    testEndpointConflictsAndSizes();
    testDescriptorBoundariesAndOverlap();
    testImportedViewIsAnImmutableCopy();
    testAssociationClassOption();

    if (failures != 0) {
        std::cerr << failures << " check(s) failed\n";
        return 1;
    }
    std::cout << "all USB descriptor consistency checks passed\n";
    return 0;
}
