cmake_minimum_required(VERSION 3.16)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

if(APPLE AND DEFINED CMAKE_AR)
  execute_process(
    COMMAND "${CMAKE_AR}" --version
    RESULT_VARIABLE _netsec_ar_status
    OUTPUT_QUIET
    ERROR_QUIET)
  if(NOT _netsec_ar_status EQUAL 0 AND
     EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "Archiver" FORCE)
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "Ranlib" FORCE)
  endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

include(GNUInstallDirs)
include(CTest)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(
  netsec
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>
    $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

install(TARGETS netsec EXPORT netsecTargets
        ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
        LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
        RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(FILES netsec.hpp DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
install(EXPORT netsecTargets
        FILE netsecTargets.cmake
        NAMESPACE netsec::
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec)

include(CMakePackageConfigHelpers)
configure_package_config_file(
  cmake/netsecConfig.cmake.in
  ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
  INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec)
write_basic_package_version_file(
  ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
  VERSION ${PROJECT_VERSION}
  COMPATIBILITY SameMajorVersion)
install(FILES
        ${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake
        ${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake
        DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/netsec)

if(BUILD_TESTING)
  add_executable(netsec_tests tests/negotiation_tests.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec::netsec)
  target_compile_features(netsec_tests PRIVATE cxx_std_20)
  if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
  endif()
  add_test(NAME netsec.negotiation COMMAND netsec_tests)

  add_executable(netsec_crypto_vectors tests/crypto_vectors.cpp)
  target_compile_features(netsec_crypto_vectors PRIVATE cxx_std_20)
  target_include_directories(netsec_crypto_vectors PRIVATE
                             ${CMAKE_CURRENT_SOURCE_DIR})
  if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
    target_compile_options(netsec_crypto_vectors PRIVATE
                           -Wall -Wextra -Wpedantic)
  endif()
  add_test(NAME netsec.crypto_vectors COMMAND netsec_crypto_vectors)
endif()
