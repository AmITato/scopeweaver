#!/usr/bin/env bash
# Run the database-backed Go suites (db, server, agent) against throwaway
# PostgreSQL databases created fresh and dropped on exit (even on failure), so
# the live/production database is never touched.
#
# Each package gets its OWN database. Running packages in parallel against one
# database made them interfere (globals, settings rows, shutdown timing), which
# showed up as an intermittent nil-pointer panic in the worker transcript path.
# CI uses a separate disposable database per package for the same reason.
#
# The database is owned by a least-privilege test role so that role can run the
# schema migrations: PostgreSQL 15+ revoked PUBLIC CREATE on the public schema,
# so a role that does not own the database cannot create tables in it. The role
# usually lacks CREATEDB, so createdb runs as the current (owning) user and the
# tests then connect as the test role.
#
# Override the defaults via environment variables to match your local PostgreSQL:
#   TEST_DB_ROLE      (default sw_user_test)  role that owns the DB and runs the tests
#   TEST_DB_HOST      (default /tmp)          unix socket directory (or a TCP host)
#   TEST_DB_NAME      (default scopeweaver_test_$$)  base name; must start with scopeweaver_test_
#   TEST_DB_PACKAGES  (default "./db/ ./server/ ./agent/") packages to run
#
# Any extra arguments are forwarded to `go test` (e.g. -run, -v).
set -euo pipefail

TEST_DB_ROLE="${TEST_DB_ROLE:-sw_user_test}"
TEST_DB_HOST="${TEST_DB_HOST:-/tmp}"
DB="${TEST_DB_NAME:-scopeweaver_test_$$}"
PACKAGES="${TEST_DB_PACKAGES:-./db/ ./server/ ./agent/}"

declare -a CREATED=()
cleanup() {
  for created in "${CREATED[@]}"; do
    dropdb --if-exists "$created" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT

status=0
for pkg in $PACKAGES; do
  suffix="$(basename "$pkg")"
  name="${DB}_${suffix}"
  createdb -O "$TEST_DB_ROLE" "$name"
  CREATED+=("$name")
  echo "test database: ${name} (owner ${TEST_DB_ROLE}, host ${TEST_DB_HOST}) for ${pkg}"
  if ! ARTEX_TEST_DSN="postgres://${TEST_DB_ROLE}@/${name}?host=${TEST_DB_HOST}&sslmode=disable" \
    go test "$pkg" -count=1 "$@"; then
    status=1
  fi
done
exit "$status"
