CXX ?= c++
CXXFLAGS ?= -std=c++17 -O2 -Wall -Wextra -pedantic
BIN := tricheck
SRC := src/main.cpp src/geom.cpp src/checker.cpp
OBJ := $(SRC:src/%.cpp=build/%.o)

all: $(BIN)

$(BIN): $(OBJ)
	$(CXX) $(CXXFLAGS) -o $@ $(OBJ)

build/%.o: src/%.cpp | build
	$(CXX) $(CXXFLAGS) -Isrc -c -o $@ $<

build:
	mkdir -p build

test: all
	sh tests/run_tests.sh

clean:
	rm -rf build $(BIN)

.PHONY: all test clean
