cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

# Some sandboxes expose the Command Line Tools compiler but have a broken
# /usr/bin/ar shim because xcrun is unavailable.  Prefer the archiver that sits
# next to the real compiler when CMake picked that shim.
if(APPLE AND CMAKE_AR STREQUAL "/usr/bin/ar")
    get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
    get_filename_component(_netsec_cxx_dir "${_netsec_cxx_real}" DIRECTORY)
    if(_netsec_cxx_real MATCHES "CommandLineTools" AND
       EXISTS "${_netsec_cxx_dir}/ar" AND
       EXISTS "${_netsec_cxx_dir}/ranlib")
        set(CMAKE_AR "${_netsec_cxx_dir}/ar" CACHE FILEPATH
            "RADIUS library archiver" FORCE)
        set(CMAKE_RANLIB "${_netsec_cxx_dir}/ranlib" CACHE FILEPATH
            "RADIUS library indexer" FORCE)
    endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
include(GNUInstallDirs)

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")
target_compile_features(netsec PUBLIC cxx_std_20)

enable_testing()
add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)

install(TARGETS netsec
        EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(EXPORT netsecTargets
        FILE netsecTargets.cmake
        NAMESPACE netsec::
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

include(CMakePackageConfigHelpers)
configure_package_config_file(
    cmake/netsecConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    INSTALL_DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    VERSION "${PROJECT_VERSION}"
    COMPATIBILITY SameMajorVersion)
install(FILES
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
