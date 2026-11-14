cmake_minimum_required(VERSION 3.16)

project(netsec LANGUAGES CXX)
# Some command-line-tools installations expose /usr/bin/{ar,ranlib} as xcrun
# shims that fail when no active developer directory is selected.  Prefer the
# real tools when they are available under the SDK installation.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
  set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "Archiver" FORCE)
  set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "Ranlib" FORCE)
endif()
set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
if(APPLE)
  target_link_libraries(netsec PUBLIC "-framework Security" "-framework CoreFoundation")
endif()
enable_testing()

if(EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/tests/test_netsec.cpp" AND
   EXISTS "${CMAKE_CURRENT_SOURCE_DIR}/tests/fixtures/ca1.der")
  add_executable(netsec_tests tests/test_netsec.cpp)
  target_link_libraries(netsec_tests PRIVATE netsec)
  add_test(NAME netsec_tests COMMAND netsec_tests)
  set_tests_properties(netsec_tests PROPERTIES
    WORKING_DIRECTORY "${CMAKE_CURRENT_SOURCE_DIR}")
endif()
