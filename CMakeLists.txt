cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
  set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "Archiver" FORCE)
  set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "Ranlib" FORCE)
  set(CMAKE_CXX_ARCHIVE_CREATE
      "/Library/Developer/CommandLineTools/usr/bin/ar qc <TARGET> <LINK_FLAGS> <OBJECTS>")
  set(CMAKE_CXX_ARCHIVE_APPEND
      "/Library/Developer/CommandLineTools/usr/bin/ar q <TARGET> <LINK_FLAGS> <OBJECTS>")
  set(CMAKE_CXX_ARCHIVE_FINISH
      "/Library/Developer/CommandLineTools/usr/bin/ranlib <TARGET>")
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)

add_executable(netsec_cli tools/netsec_cli.cpp)
target_link_libraries(netsec_cli PRIVATE netsec)

enable_testing()
add_executable(netsec_tests tests/test_netsec.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_revision_tests COMMAND netsec_tests)
