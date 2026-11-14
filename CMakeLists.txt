cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR
        "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "Archiver" FORCE)
    set(CMAKE_RANLIB
        "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "Archive indexer" FORCE)
    set(CMAKE_CXX_ARCHIVE_CREATE
        "${CMAKE_AR} qc <TARGET> <LINK_FLAGS> <OBJECTS>"
        CACHE STRING "Static archive creation rule" FORCE)
    set(CMAKE_CXX_ARCHIVE_APPEND
        "${CMAKE_AR} q <TARGET> <LINK_FLAGS> <OBJECTS>"
        CACHE STRING "Static archive append rule" FORCE)
    set(CMAKE_CXX_ARCHIVE_FINISH
        "${CMAKE_RANLIB} <TARGET>"
        CACHE STRING "Static archive finalization rule" FORCE)
endif()

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:include>"
)

include(CTest)
if(BUILD_TESTING)
    add_executable(netsec_cgroup_tests tests/cgroup_policy_test.cpp)
    target_link_libraries(netsec_cgroup_tests PRIVATE netsec::netsec)
    add_test(NAME netsec_cgroup_tests COMMAND netsec_cgroup_tests)
endif()

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

set(NETSEC_CMAKE_INSTALL_DIR
    "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

install(TARGETS netsec EXPORT netsecTargets
    ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
    LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
)
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
    FILE netsecTargets.cmake
    NAMESPACE netsec::
    DESTINATION "${NETSEC_CMAKE_INSTALL_DIR}"
)

configure_package_config_file(
    cmake/netsecConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    INSTALL_DESTINATION "${NETSEC_CMAKE_INSTALL_DIR}"
)
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    VERSION "${PROJECT_VERSION}"
    COMPATIBILITY SameMajorVersion
)
install(FILES
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    DESTINATION "${NETSEC_CMAKE_INSTALL_DIR}"
)
