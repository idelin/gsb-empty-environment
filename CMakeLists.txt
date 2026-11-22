cmake_minimum_required(VERSION 3.16)
project(gsb_redaction_plan VERSION 1.0.0 LANGUAGES CXX)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)
set(CMAKE_CXX_EXTENSIONS OFF)

if(MSVC)
  add_compile_options(/W4 /permissive-)
else()
  add_compile_options(-Wall -Wextra -Wpedantic)
endif()

add_library(gsb_plan OBJECT
  src/json.cpp
  src/sha256.cpp
  src/model.cpp
  src/solver.cpp
  src/evidence.cpp
)

target_include_directories(gsb_plan PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)

add_executable(gsb-plan src/cli.cpp)
target_link_libraries(gsb-plan PRIVATE gsb_plan)

enable_testing()

add_executable(gsb_plan_tests tests/test_main.cpp)
target_link_libraries(gsb_plan_tests PRIVATE gsb_plan)
add_test(NAME gsb_plan_tests COMMAND gsb_plan_tests)
