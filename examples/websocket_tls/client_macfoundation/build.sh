#!/bin/bash

clang -o macws main.mm -framework Foundation -framework Security --std=c++11 -lstdc++

