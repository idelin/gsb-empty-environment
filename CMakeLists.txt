cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

include(GNUInstallDirs)
find_package(Threads REQUIRED)

# Some sandboxed macOS images expose /usr/bin/ar and /usr/bin/ranlib as
# xcode-select stubs. Prefer the Command Line Tools copies when the SDKROOT
# points at an installed toolchain. This is a no-op on normal installations.
if(APPLE AND DEFINED ENV{SDKROOT})
    get_filename_component(_netsec_sdk_dir "$ENV{SDKROOT}" DIRECTORY)
    get_filename_component(_netsec_clt_root "${_netsec_sdk_dir}" DIRECTORY)
    if(EXISTS "${_netsec_clt_root}/usr/bin/ar" AND
       CMAKE_AR STREQUAL "/usr/bin/ar")
        set(CMAKE_AR "${_netsec_clt_root}/usr/bin/ar"
            CACHE FILEPATH "Archiver" FORCE)
    endif()
    if(EXISTS "${_netsec_clt_root}/usr/bin/ranlib" AND
       CMAKE_RANLIB STREQUAL "/usr/bin/ranlib")
        set(CMAKE_RANLIB "${_netsec_clt_root}/usr/bin/ranlib"
            CACHE FILEPATH "Ranlib" FORCE)
    endif()
endif()

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
target_link_libraries(netsec PUBLIC Threads::Threads)
target_include_directories(
    netsec
    PUBLIC
        "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
        "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>"
)
set_target_properties(
    netsec PROPERTIES
    CXX_EXTENSIONS OFF
    OUTPUT_NAME netsec
    WINDOWS_EXPORT_ALL_SYMBOLS ON
)

include(CMakePackageConfigHelpers)
install(
    TARGETS netsec
    EXPORT netsecTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
    LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR}
    INCLUDES DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}
)
install(
    FILES netsec.hpp
    DESTINATION ${CMAKE_INSTALL_INCLUDEDIR}
)
install(
    EXPORT netsecTargets
    FILE netsecTargets.cmake
    NAMESPACE netsec::
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)
configure_package_config_file(
    cmake/netsecConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    VERSION ${PROJECT_VERSION}
    COMPATIBILITY SameMajorVersion
)
install(
    FILES
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec
)

option(NETSEC_BUILD_TESTS "Build the netsec behavioral tests" ON)
if(NETSEC_BUILD_TESTS AND CMAKE_SOURCE_DIR STREQUAL CMAKE_CURRENT_SOURCE_DIR)
    enable_testing()
    add_executable(netsec_tests tests/test_netsec.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
