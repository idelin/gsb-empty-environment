cmake_minimum_required(VERSION 3.16)
project(netsec LANGUAGES CXX)

# Some CI sandboxes expose only xcrun stubs at /usr/bin/ar while the actual
# toolchain lives in CommandLineTools.  Avoid breaking the library build in
# that environment without overriding an explicitly configured archiver.
if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar" AND
   (NOT CMAKE_AR OR CMAKE_AR STREQUAL "/usr/bin/ar"))
  set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
      CACHE FILEPATH "Archiver" FORCE)
  set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
      CACHE FILEPATH "Ranlib" FORCE)
endif()

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)
find_package(Threads REQUIRED)
add_library(netsec netsec.cpp)
target_include_directories(netsec PUBLIC "${CMAKE_CURRENT_SOURCE_DIR}")
target_compile_features(netsec PUBLIC cxx_std_20)
target_link_libraries(netsec PUBLIC Threads::Threads)
enable_testing()
add_executable(netsec_tests tests/netsec_tests.cpp)
target_link_libraries(netsec_tests PRIVATE netsec)
add_test(NAME netsec_tests COMMAND netsec_tests)
