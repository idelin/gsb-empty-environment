CXX      ?= clang++
CXXFLAGS ?= -std=c++20 -Wall -Wextra -Werror -g -O1 -pthread
SRC       = src/test_intrusive_queue.cpp
HDR       = src/intrusive_queue.hpp

.PHONY: all asan tsan plain clean

all: asan tsan

plain: build/test_plain
	./build/test_plain

asan: build/test_asan
	./build/test_asan

tsan: build/test_tsan
	./build/test_tsan

build/test_plain: $(SRC) $(HDR)
	@mkdir -p build
	$(CXX) $(CXXFLAGS) -O2 -o $@ $(SRC)

build/test_asan: $(SRC) $(HDR)
	@mkdir -p build
	$(CXX) $(CXXFLAGS) -fsanitize=address,undefined -fno-omit-frame-pointer -o $@ $(SRC)

build/test_tsan: $(SRC) $(HDR)
	@mkdir -p build
	$(CXX) $(CXXFLAGS) -fsanitize=thread -fno-omit-frame-pointer -o $@ $(SRC)

clean:
	rm -rf build
