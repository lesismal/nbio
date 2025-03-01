#!/bin/bash

# 切换到项目根目录
cd ../../
ROOT_DIR=$(pwd)

mkdir -p ./autobahn/bin
go build -o ./autobahn/bin/autobahn_server ./autobahn/server/
go build -o ./autobahn/bin/autobahn_reporter ./autobahn/reporter/

echo "pwd:" $(pwd)
./autobahn/bin/autobahn_server &

rm -rf ${ROOT_DIR}/autobahn/report
mkdir -p ${ROOT_DIR}/autobahn/report/

docker pull crossbario/autobahn-testsuite

docker run -i --rm \
	-v ${ROOT_DIR}/autobahn/config:/config \
	-v ${ROOT_DIR}/autobahn/report:/report \
	--network host \
	--name=autobahn \
	crossbario/autobahn-testsuite \
	wstest -m fuzzingclient -s /config/fuzzingclient.json

trap ctrl_c INT
ctrl_c() {
	echo "SIGINT received; cleaning up"
	docker kill --signal INT "autobahn" >/dev/null
	rm -rf ${ROOT_DIR}/autobahn/bin
	rm -rf ${ROOT_DIR}/autobahn/report
	cleanup
	exit 130
}

cleanup() {
	killall autobahn_server
}

./autobahn/bin/autobahn_reporter ${ROOT_DIR}/autobahn/report/index.json

cleanup
