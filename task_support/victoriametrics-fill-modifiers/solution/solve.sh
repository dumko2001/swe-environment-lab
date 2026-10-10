#!/bin/sh
set -eu
cd /app/victoriametrics
patch -p1 < /solution/fix.patch
