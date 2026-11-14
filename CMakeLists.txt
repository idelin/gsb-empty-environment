cmake_minimum_required(VERSION 3.20)
project(netsec VERSION 1.0.0 LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

add_library(netsec netsec.cpp detail/crypto.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
  $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>)
target_compile_features(netsec PUBLIC cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        LIBRARY DESTINATION "${CMAKE_INSTALL_LIBDIR}"
        RUNTIME DESTINATION "${CMAKE_INSTALL_BINDIR}")
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
install(DIRECTORY detail DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}"
        FILES_MATCHING PATTERN "*.hpp")
install(EXPORT netsecTargets NAMESPACE netsec::
        DESTINATION "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")
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

enable_testing()
find_package(Threads REQUIRED)

add_executable(netsec_crypto_tests tests/crypto_tests.cpp)
target_link_libraries(netsec_crypto_tests PRIVATE netsec)
add_test(NAME netsec_crypto_tests COMMAND netsec_crypto_tests)

add_executable(netsec_protocol_tests tests/protocol_tests.cpp)
target_link_libraries(netsec_protocol_tests PRIVATE netsec Threads::Threads)
add_test(NAME netsec_protocol_tests COMMAND netsec_protocol_tests)
