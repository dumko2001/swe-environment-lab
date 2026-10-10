#!/bin/sh
set -eu
cd /app/thanos
patch -p1 < /solution/fix.patch
