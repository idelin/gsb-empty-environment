cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(APPLE)
  # Some CI shells expose /usr/bin/ar as an xcode-select shim even though the
  # actual CommandLineTools binaries are present.
  foreach(tool AR RANLIB)
    string(TOLOWER "${tool}" tool_lower)
    if(EXISTS "/Library/Developer/CommandLineTools/usr/bin/${tool_lower}")
      set(CMAKE_${tool}
          "/Library/Developer/CommandLineTools/usr/bin/${tool_lower}"
          CACHE FILEPATH "Archiver" FORCE)
    endif()
  endforeach()
endif()

add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
target_compile_definitions(netsec PRIVATE _GNU_SOURCE)

find_package(Threads REQUIRED)
target_link_libraries(netsec PUBLIC Threads::Threads)

include(CTest)
if(BUILD_TESTING)
  add_executable(netsec_tests tests/netsec_tests.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec Threads::Threads)
  target_compile_features(netsec_tests PRIVATE cxx_std_20)
  add_test(NAME netsec_tests COMMAND netsec_tests)
endif()
