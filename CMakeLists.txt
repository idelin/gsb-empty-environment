cmake_minimum_required(VERSION 3.20)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# The standalone Command Line Tools installation on macOS can leave
# /usr/bin/ar behind a broken xcode-select shim. Prefer the real archiver.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "Archiver for static libraries" FORCE)
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "Ranlib for static libraries" FORCE)
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
    $<INSTALL_INTERFACE:include>
)
target_compile_features(netsec PUBLIC cxx_std_20)
add_library(netsec::netsec ALIAS netsec)

if(MSVC)
    target_compile_options(netsec PRIVATE /W4 /permissive-)
else()
    target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(netsec_tests tests/trust_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    if(MSVC)
        target_compile_options(netsec_tests PRIVATE /W4 /permissive-)
    else()
        target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
    endif()
    add_test(NAME positioning_trust_tests COMMAND netsec_tests)
endif()

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)
install(TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
    LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
)
install(FILES netsec.hpp DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT netsecTargets
    FILE netsecTargets.cmake
    NAMESPACE netsec::
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)

configure_package_config_file(
    cmake/netsecConfig.cmake.in
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
    INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)
write_basic_package_version_file(
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
    VERSION ${PROJECT_VERSION}
    COMPATIBILITY SameMajorVersion
)
install(FILES
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
    ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)
