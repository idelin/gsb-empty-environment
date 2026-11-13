cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Some command-line-tool installations expose compilers and archivers in the
# same directory while /usr/bin/ar is an xcrun proxy.
get_filename_component(_netsec_compiler_real "${CMAKE_CXX_COMPILER}" REALPATH)
get_filename_component(_netsec_compiler_dir "${_netsec_compiler_real}" DIRECTORY)
if(EXISTS "${_netsec_compiler_dir}/ar")
  set(CMAKE_AR "${_netsec_compiler_dir}/ar" CACHE FILEPATH "" FORCE)
endif()
if(EXISTS "${_netsec_compiler_dir}/ranlib")
  set(CMAKE_RANLIB "${_netsec_compiler_dir}/ranlib" CACHE FILEPATH "" FORCE)
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

enable_testing()

add_executable(netsec_tests tests/connection_id_manager_test.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME connection_id_manager COMMAND netsec_tests)
