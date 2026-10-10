#!/bin/sh
set -eu
/usr/local/bin/task-env start
exec "$@"
