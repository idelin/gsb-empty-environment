cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some sandboxed macOS toolchains expose clang through a wrapper while the
# xcrun shim for /usr/bin/ar cannot run without an interactive developer
# directory selection.  Prefer the archiver shipped beside the compiler.
if(APPLE)
  get_filename_component(_netsec_cxx_real "${CMAKE_CXX_COMPILER}" REALPATH)
  get_filename_component(_netsec_cxx_dir "${_netsec_cxx_real}" DIRECTORY)
  if(EXISTS "${_netsec_cxx_dir}/ar" AND
     (NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar"))
    set(CMAKE_AR "${_netsec_cxx_dir}/ar" CACHE FILEPATH "" FORCE)
  endif()
  if(EXISTS "${_netsec_cxx_dir}/ranlib" AND
     (NOT CMAKE_RANLIB OR CMAKE_RANLIB STREQUAL "/usr/bin/ranlib"))
    set(CMAKE_RANLIB "${_netsec_cxx_dir}/ranlib" CACHE FILEPATH "" FORCE)
  endif()
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
enable_testing()

add_executable(netsec_lifecycle_test tests/lifecycle_test.cpp)
target_link_libraries(netsec_lifecycle_test PRIVATE netsec)
add_test(NAME lifecycle COMMAND netsec_lifecycle_test)

add_executable(netsec_identity_probe tests/identity_probe.cpp)
target_link_libraries(netsec_identity_probe PRIVATE netsec)
add_test(NAME identity-probe COMMAND netsec_identity_probe)
