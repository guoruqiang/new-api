#!/bin/sh
set -eu

# Temporary nw.45 rollback cleanup. Failure stops startup so it can be retried.
/cleanup-custom-risk
exec /new-api "$@"
