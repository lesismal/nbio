#!/bin/bash

echo "pwd:" $(pwd)
./autobahn/bin/nbio_autobahn_server &

# mkdir reports
rm -rf ${PWD}/autobahn/reports
mkdir ${PWD}/autobahn/reports

docker run -it --rm \
    -v ${PWD}/autobahn/config:/config \
    -v ${PWD}/autobahn/reports:/reports \
    --network host \
    --name=nbio_autobahn \
    crossbario/autobahn-testsuite \
    wstest -m fuzzingclient -s /config/fuzzingclient.json


trap ctrl_c INT
ctrl_c () {
	echo "SIGINT received; cleaning up"
	docker kill --signal INT "autobahn" >/dev/null
	rm -rf ${PWD}/autobahn/bin
	rm -rf ${PWD}/autobahn/reports
	cleanup
	exit 130
} 

cleanup() {
	killall nbio_autobahn_server
	#docker rm nbio_autobahn >/dev/null
}

./autobahn/bin/nbio_autobahn_reporter ${PWD}/autobahn/reports/index.json

cleanup

