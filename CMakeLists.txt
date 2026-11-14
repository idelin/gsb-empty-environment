cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

if(APPLE)
  # In headless Command Line Tools installs /usr/bin/{ar,ranlib} can be xcrun
  # shims that fail before invoking the real tool. Prefer the SDK copies when
  # CMake selected those shims.
  set(_netsec_clt_bin "/Library/Developer/CommandLineTools/usr/bin")
  if(CMAKE_AR STREQUAL "/usr/bin/ar" AND EXISTS "${_netsec_clt_bin}/ar")
    set(CMAKE_AR "${_netsec_clt_bin}/ar" CACHE FILEPATH "Archiver" FORCE)
  endif()
  if(CMAKE_RANLIB STREQUAL "/usr/bin/ranlib" AND
     EXISTS "${_netsec_clt_bin}/ranlib")
    set(CMAKE_RANLIB "${_netsec_clt_bin}/ranlib"
        CACHE FILEPATH "Archive indexer" FORCE)
  endif()
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

add_executable(netsec-check netsec_check.cpp)
target_link_libraries(netsec-check PRIVATE netsec)

enable_testing()
add_executable(netsec-tests tests/netsec_tests.cpp)
target_link_libraries(netsec-tests PRIVATE netsec)
add_test(NAME netsec-tests COMMAND netsec-tests)

add_test(NAME netsec-fixture
         COMMAND netsec-tests --write-fixture
                 "${CMAKE_CURRENT_BINARY_DIR}/netsec-valid.so")
set_tests_properties(netsec-fixture PROPERTIES FIXTURES_SETUP netsec-elf)
add_test(NAME netsec-cli-allow
         COMMAND netsec-check "${CMAKE_CURRENT_BINARY_DIR}/netsec-valid.so")
add_test(NAME netsec-cli-json
         COMMAND netsec-check --json
                 "${CMAKE_CURRENT_BINARY_DIR}/netsec-valid.so")
set_tests_properties(netsec-cli-allow netsec-cli-json PROPERTIES
                     FIXTURES_REQUIRED netsec-elf)
