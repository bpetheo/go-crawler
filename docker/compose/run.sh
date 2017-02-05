#!/bin/sh

#task:Run a command in a temporary web container.

if [ "$1" = "--help" ]; then
    echo "Run a command in a temporary db container as root."
    echo
    echo "Usage:"
    printf "\t%s\n" "$TASKRUNNER $TASK [command]"
    exit 0
fi

# We use /bin/su instead of the -u option because -u doesn't set some enviornment
# variables, such as USER and SHELL.
# /bin/su - www-data would simulate a full login and execute ~/.profile, but
# it would also clear the docker environment variables which we need.
# /bin/su -lm root would preserve USER and HOME, which is bad.
command="su root -c"

container=""

cleanup() {
    trap '' INT
    trap '-' EXIT
    if [ -n "$container" ]; then
        echo "Removing $container"
        docker stop "$container" > /dev/null
        docker rm -v "$container" > /dev/null
        ids=$(docker-compose ps -q db)
        if [ -n "$ids" ]; then
            if ! docker-compose ps -q db | tr -d '\r' | xargs docker inspect -f '{{ .State.Running }}' | grep 'true' > /dev/null; then
                docker-compose stop
            fi
        fi
    fi
}

trap 'cleanup' INT EXIT

# Hack because
# 1. the process runs as PID 1, and won't stop on SIGINT or SIGTERM unless specifically coded to do so
# 2. docker-compose run --rm leaves dangling unnamed volumes around
container=$(docker-compose run -d db sh -c "trap 'exit 0' INT TERM QUIT; while true; do sleep 1; done")
echo "Executing in $container"

params="-it"
if [ ! -t 0 ]; then
    params="-t"
fi

# shellcheck disable=SC2016 disable=SC2086
docker exec $params "$container" env TERM=xterm $command '"$0" "$@"' -- "$@"
