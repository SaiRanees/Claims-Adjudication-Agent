#!/bin/sh
# Checks the guarantees the schema makes, against a migrated and seeded database:
#   - claims_app has exactly the privileges listed below, down to the column;
#   - the audit chain rejects gaps, forks and wrong links, and no role can rewrite it;
#   - the constraints the services rely on hold.
# Each check runs in a transaction that is rolled back, so the data is left as it was.
#
# Usage:
#   go run ./cmd/dev db-check   runs this inside the Compose Postgres container
#   sh scripts/db-check.sh      with OWNER_URL and APP_URL set, as CI does
set -eu

OWNER_URL=${OWNER_URL:-postgresql:///claims?user=claims_owner}
APP_URL=${APP_URL:-postgresql:///claims?user=claims_app}

# From db/seed/dev.sql.
SEED_POLICY=01930000-0000-7000-8000-000000000001
SEED_MEMBER=01930000-0000-7000-8000-000000000201
# Created and rolled back by the checks.
CLAIM=ffffffff-0000-7000-8000-000000000001
DOC1=ffffffff-0000-7000-8000-0000000000d1
DOC2=ffffffff-0000-7000-8000-0000000000d2

# claims_app's privileges, one table per line, sorted by name. The letters are Postgres ACL
# letters: r SELECT, a INSERT, w UPDATE, d DELETE, D TRUNCATE, x REFERENCES, t TRIGGER.
# +w(...) lists the only columns it may UPDATE. Change this with the GRANTs in a migration.
EXPECTED_GRANTS='audit_events=ra
claims=ra+w(decided_at,status,submitted_at)
documents=ra+w(page_count,rejection_reason,status,uploaded_at)
idempotency_keys=rad
members=r
model_calls=ra+w(attempts,cache_read_tokens,cache_write_tokens,cost_usd_micros,error_code,finished_at,input_tokens,latency_ms,output_tokens,response_object_key,status)
outbox=rad+w(attempts,last_error,next_attempt_at,published_at)
policies=r
policy_periods=r
schema_migrations='

failures=0
pass() { printf 'ok    %s\n' "$1"; }
fail() { printf 'FAIL  %s\n' "$1"; failures=$((failures + 1)); }

# query URL SQL: prints the result rows, unaligned and without headers.
query() { psql "$1" -X -q -A -t -v ON_ERROR_STOP=1 -c "$2" </dev/null; }

# must_pass NAME URL SQL: the SQL must succeed.
must_pass() {
	if out=$(psql "$2" -X -q -v ON_ERROR_STOP=1 -c "BEGIN; $3; ROLLBACK;" </dev/null 2>&1); then
		pass "$1"
	else
		fail "$1: $(printf '%s\n' "$out" | head -n 1)"
	fi
}

# must_fail NAME URL SQLSTATE SQL: the SQL must fail with exactly this SQLSTATE.
must_fail() {
	if out=$(psql "$2" -X -q -v ON_ERROR_STOP=1 -v VERBOSITY=verbose \
		-c "BEGIN; $4; ROLLBACK;" </dev/null 2>&1); then
		fail "$1: it succeeded"
	elif printf '%s\n' "$out" | grep -q "ERROR:  $3:"; then
		pass "$1"
	else
		fail "$1: expected SQLSTATE $3, got: $(printf '%s\n' "$out" | grep -m 1 'ERROR' || true)"
	fi
}

new_claim() { # new_claim [AMOUNT_PAISE]
	printf "INSERT INTO claims (id, member_id, admitted_on, discharged_on, hospital_name,
		claimed_amount_paise, workflow_id, created_by)
		VALUES ('%s', '%s', DATE '2026-05-01', DATE '2026-05-04', 'Synthetic General Hospital',
		%s, 'claim-%s', 'db-check')" "$CLAIM" "$SEED_MEMBER" "${1:-1250000}" "$CLAIM"
}

event() { # event SEQ PREV_HASH HASH
	printf "INSERT INTO audit_events (claim_id, seq, actor_type, actor_id, action, prev_hash, hash)
		VALUES ('%s', %s, 'service', 'db-check', 'claim.checked', %s, %s)" "$CLAIM" "$1" "$2" "$3"
}

document() { # document ID FILE_CONTENT
	printf "INSERT INTO documents (id, claim_id, file_name, content_type, size_bytes, sha256, object_key)
		VALUES ('%s', '%s', 'bill.pdf', 'application/pdf', 1024, sha256('%s'), 'claims/%s/%s')" \
		"$1" "$CLAIM" "$2" "$CLAIM" "$1"
}

outbox() { # outbox PAYLOAD
	printf "INSERT INTO outbox (event_id, claim_id, kind, workflow_id, name, payload)
		VALUES (gen_random_uuid(), '%s', 'workflow_start', 'claim-%s', 'ClaimWorkflow', %s)" \
		"$CLAIM" "$CLAIM" "$1"
}

idempotency_key() { # idempotency_key REQUEST
	printf "INSERT INTO idempotency_keys (principal, idempotency_key, request_hash,
		response_status, response_body, expires_at)
		VALUES ('db-check', 'key-1', sha256('%s'), 201, '{}', now() + interval '1 day')" "$1"
}

# Preconditions: both roles can connect, and the seed is loaded.
query "$OWNER_URL" 'SELECT 1' >/dev/null || { echo "cannot connect as claims_owner" >&2; exit 2; }
query "$APP_URL" 'SELECT 1' >/dev/null || { echo "cannot connect as claims_app" >&2; exit 2; }
if [ "$(query "$OWNER_URL" "SELECT count(*) FROM members WHERE id = '$SEED_MEMBER'")" != 1 ]; then
	echo "the seed data is missing: run go run ./cmd/dev seed first" >&2
	exit 2
fi

echo "privileges"
actual=$(query "$OWNER_URL" "
	SELECT c.relname || '=' || concat(
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'SELECT') THEN 'r' END,
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'INSERT') THEN 'a' END,
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'UPDATE') THEN 'w' END,
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'DELETE') THEN 'd' END,
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'TRUNCATE') THEN 'D' END,
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'REFERENCES') THEN 'x' END,
	         CASE WHEN has_table_privilege('claims_app', c.oid, 'TRIGGER') THEN 't' END)
	       || coalesce((
	         SELECT '+w(' || string_agg(a.attname, ',' ORDER BY a.attname) || ')'
	         FROM pg_attribute a
	         WHERE a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
	           AND NOT has_table_privilege('claims_app', c.oid, 'UPDATE')
	           AND has_column_privilege('claims_app', c.oid, a.attnum, 'UPDATE')), '')
	FROM pg_class c
	JOIN pg_namespace n ON n.oid = c.relnamespace
	WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p')
	ORDER BY c.relname")
if [ "$actual" = "$EXPECTED_GRANTS" ]; then
	pass "claims_app has exactly the expected privileges"
else
	fail "claims_app's privileges differ from the list in this script"
	printf '      expected:\n%s\n      actual:\n%s\n' "$EXPECTED_GRANTS" "$actual"
fi
must_fail "claims_app cannot create tables" "$APP_URL" 42501 "CREATE TABLE db_check_probe (id int)"
must_fail "claims_app cannot change policies" "$APP_URL" 42501 "UPDATE policies SET wording_uin = wording_uin"
must_fail "claims_app cannot delete claims" "$APP_URL" 42501 "DELETE FROM claims"
must_fail "claims_app cannot change who created a claim" "$APP_URL" 42501 \
	"UPDATE claims SET created_by = 'someone-else'"
if [ "$(query "$APP_URL" 'SHOW statement_timeout')" = 10s ]; then
	pass "claims_app sessions have a statement timeout"
else
	fail "claims_app sessions have no 10s statement timeout"
fi

echo "audit chain"
E1=$(event 1 NULL "sha256('e1')")
E2=$(event 2 "sha256('e1')" "sha256('e2')")
must_pass "an app can append linked events" "$APP_URL" "$(new_claim); $E1; $E2"
must_fail "the first event has no previous hash" "$APP_URL" 23514 \
	"$(new_claim); $(event 1 "sha256('e0')" "sha256('e1')")"
must_fail "a wrong previous hash is rejected" "$APP_URL" 23503 \
	"$(new_claim); $E1; $(event 2 "sha256('forged')" "sha256('e2')")"
must_fail "a gap in the chain is rejected" "$APP_URL" 23503 \
	"$(new_claim); $E1; $(event 3 "sha256('e1')" "sha256('e3')")"
must_fail "a fork in the chain is rejected" "$APP_URL" 23505 \
	"$(new_claim); $E1; $E2; $(event 2 "sha256('e1')" "sha256('e2-fork')")"
must_fail "claims_app cannot UPDATE audit events" "$APP_URL" 42501 "UPDATE audit_events SET actor_id = actor_id"
must_fail "claims_app cannot DELETE audit events" "$APP_URL" 42501 "DELETE FROM audit_events"
must_fail "claims_app cannot TRUNCATE audit events" "$APP_URL" 42501 "TRUNCATE audit_events"
must_fail "the owner cannot UPDATE audit events either" "$OWNER_URL" 42501 "UPDATE audit_events SET actor_id = actor_id"
must_fail "the owner cannot DELETE audit events either" "$OWNER_URL" 42501 "DELETE FROM audit_events"
must_fail "the owner cannot TRUNCATE audit events either" "$OWNER_URL" 42501 "TRUNCATE audit_events"

echo "claims"
must_fail "a claimed amount must be positive" "$APP_URL" 23514 "$(new_claim 0)"
must_fail "a submitted claim needs its submission time" "$APP_URL" 23514 \
	"$(new_claim); UPDATE claims SET status = 'submitted' WHERE id = '$CLAIM'"
must_fail "a decided claim needs its decision time" "$APP_URL" 23514 \
	"$(new_claim); UPDATE claims SET status = 'decided', submitted_at = now() WHERE id = '$CLAIM'"
must_pass "every update bumps the claim's version" "$APP_URL" \
	"$(new_claim); UPDATE claims SET status = 'submitted', submitted_at = now() WHERE id = '$CLAIM';
	 DO \$\$ BEGIN
	   IF (SELECT version FROM claims WHERE id = '$CLAIM') <> 2 THEN
	     RAISE EXCEPTION 'version was not bumped';
	   END IF;
	 END \$\$"
must_fail "an idempotency key is single-use per caller" "$APP_URL" 23505 \
	"$(idempotency_key first); $(idempotency_key second)"

echo "policies"
must_fail "policy years cannot overlap" "$OWNER_URL" 23P01 \
	"INSERT INTO policy_periods (id, policy_id, starts_on, ends_on, sum_insured_paise)
	 VALUES (gen_random_uuid(), '$SEED_POLICY', DATE '2026-10-01', DATE '2027-09-30', 50000000)"
must_pass "the next policy year can follow the current one" "$OWNER_URL" \
	"INSERT INTO policy_periods (id, policy_id, starts_on, ends_on, sum_insured_paise)
	 VALUES (gen_random_uuid(), '$SEED_POLICY', DATE '2027-04-01', DATE '2028-03-31', 50000000)"

echo "documents and outbox"
must_fail "the same file cannot be attached to a claim twice" "$APP_URL" 23505 \
	"$(new_claim); $(document "$DOC1" file-a); $(document "$DOC2" file-a)"
must_pass "a rejected upload can be retried" "$APP_URL" \
	"$(new_claim); $(document "$DOC1" file-a);
	 UPDATE documents SET status = 'rejected', rejection_reason = 'checksum_mismatch' WHERE id = '$DOC1';
	 $(document "$DOC2" file-a)"
must_fail "an uploaded document needs its upload time" "$APP_URL" 23514 \
	"$(new_claim); $(document "$DOC1" file-a); UPDATE documents SET status = 'uploaded' WHERE id = '$DOC1'"
must_pass "claims_app can queue a workflow message" "$APP_URL" \
	"$(new_claim); $(outbox "jsonb_build_object('claimId', '$CLAIM')")"
must_fail "outbox payloads carry IDs, not documents" "$APP_URL" 23514 \
	"$(new_claim); $(outbox "jsonb_build_object('text', repeat('x', 4096))")"

if [ "$failures" -gt 0 ]; then
	printf '\n%s check(s) failed\n' "$failures"
	exit 1
fi
printf '\nall checks passed\n'
