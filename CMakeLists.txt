cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
# Header-only component: expose it as an INTERFACE library so embedding
# projects need no separate archive/static-lib step.
add_library(netsec INTERFACE)
target_include_directories(netsec INTERFACE "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec INTERFACE cxx_std_17)

enable_testing()

# netsec.cpp only includes the header; compile it here as an include sanity
# check without producing a separate static archive.
add_executable(test_spec_index test_spec_index.cpp netsec.cpp)
target_link_libraries(test_spec_index PRIVATE netsec)
add_test(NAME spec_index COMMAND test_spec_index)
