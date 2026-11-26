# Zero-dependency build for the DTLS 1.3 record-layer security library.
CXX ?= clang++
CXXSTD := -std=c++17
WARN := -Wall -Wextra -Wconversion -Werror -Wpedantic
INC := -Iinclude

SRC := \
	src/types.cpp \
	src/crypto.cpp \
	src/record.cpp \
	src/record_layer.cpp \
	src/reassembly.cpp \
	src/cookie.cpp

OBJ := $(SRC:.cpp=.o)
DEPS := $(OBJ:.o=.d)

CXXFLAGS := $(CXXSTD) $(WARN) $(INC) -MMD -MP

.PHONY: all test fuzz clean

all: build/dtls13_tests

build:
	mkdir -p build

%.o: %.cpp
	$(CXX) $(CXXFLAGS) -c $< -o $@

build/dtls13_tests: $(OBJ) tests/test_main.cpp | build
	$(CXX) $(CXXSTD) $(WARN) $(INC) $(OBJ) \
		tests/test_main.cpp tests/test_crypto.cpp tests/test_record.cpp \
		tests/test_record_layer.cpp tests/test_reassembly.cpp \
		tests/test_cookie.cpp -o $@

test: build/dtls13_tests
	./build/dtls13_tests

fuzz: build/dtls13_fuzz_smoke
	./build/dtls13_fuzz_smoke

build/dtls13_fuzz_smoke: $(OBJ) tests/fuzz_smoke.cpp | build
	$(CXX) $(CXXSTD) $(WARN) $(INC) $(OBJ) tests/fuzz_smoke.cpp -o $@

clean:
	rm -f $(OBJ)
	rm -f $(DEPS)
	rm -rf build

-include $(DEPS)
