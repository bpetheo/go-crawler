#!/bin/sh

#task:Start a shell in the web container.

if [ "$1" = "--help" ]; then
    echo "Start a shell or execute a command in the db container as root."
    echo
    echo "Usage:"
    printf "\t%s\n" "$TASKRUNNER $TASK [command]"
    exit 0
fi

user="root"
shelltype="root "

# We use /bin/su instead of the -u option because -u doesn't set some enviornment
# variables, such as USER and SHELL.
# /bin/su - www-data would simulate a full login and execute ~/.profile, but
# it would also clear the docker environment variables which we need.
# /bin/su -lm root would preserve USER and HOME, which is bad.

title='PROMPT_COMMAND="printf '"'"'\033]0;%s\007'"'"' \"Docker '"$shelltype"'shell: '"$PROJECT_NAME"'\""'
command="env TERM=xterm $title su $user"

container=$(docker-compose ps -q db | tr -d '\r')
command="docker exec -it $container $command"

if [ $# -gt 0 ]; then
    # shellcheck disable=SC2145
    eval "$command -c \"$@\""
else
    eval "$command"
fi
