
report: config/ws_tls_sticky.json
	./script/test.sh --build fuzzingclient /config/ws_tls_sticky.json examples/websocket_tls/sticky/server --follow-logs

clean:
	rm -fr report

summary:
	chmod -R 700 $(PWD)/report/
	cat $(PWD)/report/ws_tls_sticky/index.json



.PHONY: clean summary
autobahn_tls_sticky: clean test_wsserver_tls_sticky summary