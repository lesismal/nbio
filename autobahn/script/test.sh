#!/bin/bash
set -x
set -e
FOLLOW_LOGS=1

while [[ $# -gt 0 ]]; do
	key="$1"
	case $key in
		--network)
		NETWORK="$2"
		shift
		;;

		--build)
		case "$2" in
			autobahn)
				mode=$3
				spec=$4
				docker build . --file autobahn/docker/autobahn/Dockerfile --tag nbio-autobahn
				shift
			;;
			server)
				docker build . --file autobahn/docker/server/Dockerfile --tag nbio-server --build-arg testfile=$3
				shift
			;;
			*)
				mode=$2
				spec=$3
				docker build . --file autobahn/docker/autobahn/Dockerfile --tag nbio-autobahn
				docker build . --file autobahn/docker/server/Dockerfile --tag nbio-server --build-arg testfile=$4
			;;
		esac
		;;

		--run)
		docker run \
			--interactive \
			--tty \
			${@:2}
		exit $?
		;;

		--follow-logs)
		FOLLOW_LOGS=1
		shift
		;;
	esac
	shift
done

with_prefix() {
	local p="$1"
	shift
	
	local out=$(mktemp -u nbio.fifo.out.XXXX)
	local err=$(mktemp -u nbio.fifo.err.XXXX)
	mkfifo $out $err
	if [ $? -ne 0 ]; then
		exit 1
	fi
	
	# Start two background sed processes.
	sed "s/^/$p/" <$out &
	sed "s/^/$p/" <$err >&2 &
	
	# Run the program
	"$@" >$out 2>$err
	rm $out $err
}

random=$(xxd -l 4 -p /dev/random)
server="${random}_nbio-server"
autobahn="${random}_nbio-autobahn"

network="nbio-network-$random"
docker network create --driver bridge "$network"
if [ $? -ne 0 ]; then
	exit 1
fi

docker run \
	--interactive \
	--tty \
	--detach \
	--network="host" \
	-v $(pwd)/report:/report \
	--name="$server" \
	"nbio-server"

echo ${mode}
docker run \
	--interactive \
	--tty \
	--detach \
	--network="host" \
	-v $(pwd)/autobahn/config:/config \
	-v $(pwd)/autobahn/report:/report \
   	--name="$autobahn" \
	"nbio-autobahn"\
 	--mode=${mode} --spec=${spec}


if [[ $FOLLOW_LOGS -eq 1 ]]; then
	(with_prefix "$(tput setaf 3)[nbio-autobahn]: $(tput sgr0)" docker logs --follow "$autobahn")&
	(with_prefix "$(tput setaf 5)[nbio-server]:   $(tput sgr0)" docker logs --follow "$server")&
fi

trap ctrl_c INT
ctrl_c () {
	echo "SIGINT received; cleaning up"
	docker kill --signal INT "$autobahn" >/dev/null
	docker kill --signal INT "$server" >/dev/null
	cleanup
	exit 130
} 

cleanup() {
	docker rm "$server" >/dev/null
	docker rm "$autobahn" >/dev/null
	docker network rm "$network"
}

docker wait "$autobahn" >/dev/null
docker stop "$server" >/dev/null

cleanup
set +x