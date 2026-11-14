cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some macOS sandboxes expose an /usr/bin/ar stub that delegates to
# xcode-select even though a usable toolchain is present next to clang++.
# Prefer the archiver that belongs to the selected compiler when available.
get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
get_filename_component(_netsec_cxx_dir "${_netsec_cxx_real}" DIRECTORY)
if(EXISTS "${_netsec_cxx_dir}/ar")
  set(CMAKE_AR "${_netsec_cxx_dir}/ar" CACHE FILEPATH "" FORCE)
endif()
if(EXISTS "${_netsec_cxx_dir}/ranlib")
  set(CMAKE_RANLIB "${_netsec_cxx_dir}/ranlib" CACHE FILEPATH "" FORCE)
endif()

add_library(netsec netsec.cpp)
target_compile_features(netsec PUBLIC cxx_std_20)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
if (MSVC)
  target_compile_options(netsec PRIVATE /W4)
else()
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
  add_executable(netsec_tests tests/netsec_tests.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec)
  add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
