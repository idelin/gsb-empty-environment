cmake_minimum_required(VERSION 3.20)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

# Some CI sandboxes put the /usr/bin stubs of the Apple Command Line Tools in
# front of the real tools. If the active SDK belongs to a CommandLineTools
# installation, use that installation's ar/ranlib instead of the stubs.
if(APPLE AND CMAKE_OSX_SYSROOT)
    get_filename_component(_netsec_sdk_parent "${CMAKE_OSX_SYSROOT}" DIRECTORY)
    get_filename_component(_netsec_tools_root "${_netsec_sdk_parent}" DIRECTORY)
    if(EXISTS "${_netsec_tools_root}/usr/bin/ar")
        set(CMAKE_AR "${_netsec_tools_root}/usr/bin/ar")
        set(CMAKE_RANLIB "${_netsec_tools_root}/usr/bin/ranlib")
    endif()
endif()

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)

target_compile_features(netsec PUBLIC cxx_std_20)
set_target_properties(netsec PROPERTIES
    CXX_EXTENSIONS OFF
    CXX_STANDARD_REQUIRED ON
)

target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>"
)

if(CMAKE_SOURCE_DIR STREQUAL PROJECT_SOURCE_DIR)
    option(NETSEC_BUILD_TESTS "Build netsec tests" ON)
else()
    option(NETSEC_BUILD_TESTS "Build netsec tests" OFF)
endif()

if(NETSEC_BUILD_TESTS)
    enable_testing()
    add_executable(netsec_tests tests/netsec_tests.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec::netsec)
    target_compile_features(netsec_tests PRIVATE cxx_std_20)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()

install(TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}"
)
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
    FILE netsecTargets.cmake
    NAMESPACE netsec::
    DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec"
)

configure_package_config_file(
    cmake/netsecConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    INSTALL_DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec"
)
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    VERSION "${PROJECT_VERSION}"
    COMPATIBILITY SameMajorVersion
)
install(FILES
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec"
)
