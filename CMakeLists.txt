cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

# Build the primary target as a shared object so a runtime can import the
# decoder directly (dlopen, ctypes, FFI, and so on).
add_library(netsec SHARED netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")

add_library(netsec_shared ALIAS netsec)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU")
  target_compile_options(netsec PRIVATE -Wall -Wextra -Wpedantic)
endif()

add_executable(netsec_cli tools/netsec_cli.cpp)
target_link_libraries(netsec_cli PRIVATE netsec)
set_target_properties(netsec_cli PROPERTIES OUTPUT_NAME netsec)

enable_testing()
add_executable(netsec_tests tests/test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_element_index_tests COMMAND netsec_tests)

add_executable(netsec_import_test tests/test_import.cpp)
target_link_libraries(netsec_import_test PRIVATE ${CMAKE_DL_LIBS})
add_test(NAME netsec_import_test
         COMMAND netsec_import_test $<TARGET_FILE:netsec>)
