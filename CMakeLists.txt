cmake_minimum_required(VERSION 3.16)

if(APPLE AND EXISTS "/Library/Developer/CommandLineTools/usr/bin/ar")
    set(CMAKE_AR "/Library/Developer/CommandLineTools/usr/bin/ar"
        CACHE FILEPATH "System archiver" FORCE)
    set(CMAKE_RANLIB "/Library/Developer/CommandLineTools/usr/bin/ranlib"
        CACHE FILEPATH "System ranlib" FORCE)
endif()

project(h26_4fe6e0a2f4 CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

add_library(module_plan STATIC
    src/module_plan.cpp
    src/exhaustive.cpp
)
add_library(module_plan::module_plan ALIAS module_plan)

target_include_directories(module_plan PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
)
target_compile_features(module_plan PUBLIC cxx_std_20)

if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU|AppleClang")
    target_compile_options(module_plan PRIVATE -Wall -Wextra -Wpedantic)
endif()

include(CTest)
if(BUILD_TESTING)
    add_executable(module_plan_tests tests/test_module_plan.cpp)
    target_link_libraries(module_plan_tests PRIVATE module_plan::module_plan)
    if(CMAKE_CXX_COMPILER_ID MATCHES "Clang|GNU|AppleClang")
        target_compile_options(module_plan_tests PRIVATE
            -Wall -Wextra -Wpedantic)
    endif()

    add_test(NAME module_plan_tests COMMAND module_plan_tests)

    add_executable(exhaustive_matrix examples/exhaustive_matrix.cpp)
    target_link_libraries(exhaustive_matrix PRIVATE module_plan::module_plan)
endif()

install(TARGETS module_plan
    ARCHIVE DESTINATION lib
    LIBRARY DESTINATION lib
    RUNTIME DESTINATION bin
)
install(DIRECTORY include/module_plan DESTINATION include)
