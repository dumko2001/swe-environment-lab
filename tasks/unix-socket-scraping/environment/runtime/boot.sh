#!/bin/sh
set -eu
task-env start
exec "$@"
