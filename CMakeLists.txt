cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

add_executable(cfi_registry_test tests/cfi_registry_test.cpp)
target_link_libraries(cfi_registry_test PRIVATE netsec)

enable_testing()
add_test(NAME cfi_registry_test COMMAND cfi_registry_test)
