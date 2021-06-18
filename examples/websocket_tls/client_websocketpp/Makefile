all: client

websocketpp:
	git clone https://github.com/zaphoyd/websocketpp

client: websocketpp client.cpp
	g++  -std=c++11  -L /usr/local/opt/openssl@1.1/lib -l ssl  -lcrypto -lboost_system -lboost_chrono -lboost_random -I /usr/local/opt/openssl@1.1/include -I websocketpp -o $@ client.cpp

test: client
	./client wss://echo.websocket.org

test_local: client
	./client

.PHONY: all test test_local