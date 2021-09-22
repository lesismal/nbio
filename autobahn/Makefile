
report: config/client_tests.json
	./script/test.sh --build fuzzingclient /config/client_tests.json examples/websocket/server_autobahn -o server.test --follow-logs


clean:
	rm -fr report

summary:
	chmod -R 700 $(PWD)/report/
	cat $(PWD)/report/output/index.json

.PHONY: clean summary