cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

include(GNUInstallDirs)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")
target_compile_features(netsec PUBLIC cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "GNU|Clang")
    target_compile_options(netsec PRIVATE
        -fno-builtin-memcmp
        -fno-builtin-bcmp)
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(netsec_tests tests/test_timing_guard.cpp)
    target_link_libraries(netsec_tests PRIVATE netsec)
    add_test(NAME netsec_tests COMMAND netsec_tests)
endif()

include(CMakePackageConfigHelpers)

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

configure_package_config_file(
    cmake/netsecConfig.cmake.in
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    INSTALL_DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
write_basic_package_version_file(
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    VERSION 1.0.0
    COMPATIBILITY SameMajorVersion)
install(FILES
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
    "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
    DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
