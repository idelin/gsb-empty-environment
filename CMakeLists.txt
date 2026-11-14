cmake_minimum_required(VERSION 3.20)
project(netsec VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# On macOS the /usr/bin archive tools are xcrun shims. If xcode-select is
# unavailable, use the archive tools next to the selected compiler.
if(APPLE AND CMAKE_AR STREQUAL "/usr/bin/ar")
  file(REAL_PATH "${CMAKE_CXX_COMPILER}" NETSEC_REAL_COMPILER)
  get_filename_component(NETSEC_TOOL_DIR "${NETSEC_REAL_COMPILER}" DIRECTORY)
  if(EXISTS "${NETSEC_TOOL_DIR}/ar" AND EXISTS "${NETSEC_TOOL_DIR}/ranlib")
    set(CMAKE_AR "${NETSEC_TOOL_DIR}/ar" CACHE FILEPATH "" FORCE)
    set(CMAKE_RANLIB "${NETSEC_TOOL_DIR}/ranlib" CACHE FILEPATH "" FORCE)
  endif()
endif()

include(GNUInstallDirs)
include(CMakePackageConfigHelpers)

add_library(netsec netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(
  netsec
  PUBLIC
    "$<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}>"
    "$<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>")

if(MSVC)
  target_compile_options(netsec PRIVATE /W4)
else()
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
  add_executable(netsec_tests tests/netsec_tests.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec)
  if(MSVC)
    target_compile_options(netsec_tests PRIVATE /W4)
  else()
    target_compile_options(netsec_tests PRIVATE -Wall -Wextra -Wpedantic)
  endif()
  add_test(NAME netsec_tests COMMAND netsec_tests)
endif()

set(NETSEC_CMAKE_DIR "${CMAKE_INSTALL_LIBDIR}/cmake/netsec")

install(TARGETS netsec EXPORT netsecTargets)
install(FILES netsec.hpp DESTINATION "${CMAKE_INSTALL_INCLUDEDIR}")
configure_package_config_file(
  cmake/netsecConfig.cmake.in
  "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
  INSTALL_DESTINATION "${NETSEC_CMAKE_DIR}")
write_basic_package_version_file(
  "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
  VERSION "${PROJECT_VERSION}"
  COMPATIBILITY SameMajorVersion)
install(
  FILES "${CMAKE_CURRENT_BINARY_DIR}/netsecConfig.cmake"
        "${CMAKE_CURRENT_BINARY_DIR}/netsecConfigVersion.cmake"
  DESTINATION "${NETSEC_CMAKE_DIR}")
install(
  EXPORT netsecTargets
  FILE netsecTargets.cmake
  NAMESPACE netsec::
  DESTINATION "${NETSEC_CMAKE_DIR}")
