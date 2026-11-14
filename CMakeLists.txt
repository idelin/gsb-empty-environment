cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec SHARED netsec.cpp)
add_library(netsec::netsec ALIAS netsec)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

enable_testing()

add_executable(netsec_tests tests/font_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
target_compile_features(netsec_tests PRIVATE cxx_std_20)

add_test(NAME netsec_font_structure COMMAND netsec_tests)
