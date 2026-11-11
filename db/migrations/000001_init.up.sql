-- 000001_init: the tables week 1 needs.
--
-- Conventions (docs/adr/0005-pure-rules-engine-and-paise.md):
--   * Money is bigint paise. Percentages are integer basis points.
--   * IDs are UUIDs the application generates (UUIDv7), so an ID exists before its row does.
--   * Instants are timestamptz; calendar dates (admission, policy year) are date.
--   * Statuses are text with a CHECK list. Widen a list in a new migration.
--   * claims_app gets explicit grants, down to the column. scripts/db-check.sh holds the same
--     list and fails on any difference, so every grant change is a reviewed change.
--
-- Migrations are forward-only: fix a mistake with a new migration, never by editing this one.

BEGIN;

-- For the "policy years can't overlap" exclusion constraint. A trusted extension, so the
-- database owner can create it without superuser rights.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Seeded reference data. The services read it and never change it.
CREATE TABLE policies (
    id                   uuid        PRIMARY KEY,
    insurer_code         text        NOT NULL CHECK (insurer_code ~ '^[A-Z][A-Z0-9_]{1,31}$'),
    policy_number        text        NOT NULL CHECK (policy_number ~ '^[A-Za-z0-9/_-]{1,64}$'),
    product_code         text        NOT NULL CHECK (product_code ~ '^[a-z][a-z0-9-]{1,63}$'),
    wording_uin          text        NOT NULL CHECK (wording_uin ~ '^[A-Z0-9]{8,32}$'),
    cover_type           text        NOT NULL CHECK (cover_type IN ('individual', 'floater')),
    first_inception_date date        NOT NULL,
    created_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (insurer_code, policy_number)
);

-- One row per policy year. Cover can change at renewal, and the year that contains the
-- admission date decides which cover applies, so two years of one policy can't overlap.
CREATE TABLE policy_periods (
    id                     uuid        PRIMARY KEY,
    policy_id              uuid        NOT NULL REFERENCES policies (id),
    starts_on              date        NOT NULL,
    ends_on                date        NOT NULL,
    sum_insured_paise      bigint      NOT NULL CHECK (sum_insured_paise > 0),
    cumulative_bonus_paise bigint      NOT NULL DEFAULT 0 CHECK (cumulative_bonus_paise >= 0),
    created_at             timestamptz NOT NULL DEFAULT now(),
    CHECK (ends_on >= starts_on),
    CONSTRAINT policy_periods_no_overlap
        EXCLUDE USING gist (policy_id WITH =, daterange(starts_on, ends_on, '[]') WITH &&)
);

CREATE TABLE members (
    id            uuid        PRIMARY KEY,
    policy_id     uuid        NOT NULL REFERENCES policies (id),
    member_code   text        NOT NULL CHECK (member_code ~ '^[A-Z0-9]{1,16}$'),
    full_name     text        NOT NULL CHECK (length(full_name) BETWEEN 1 AND 200),
    date_of_birth date        NOT NULL,
    relationship  text        NOT NULL
                  CHECK (relationship IN ('self', 'spouse', 'child', 'parent', 'parent_in_law')),
    -- First day of continuous cover for this person. Waiting periods count from here.
    covered_since date        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (policy_id, member_code),
    CHECK (covered_since >= date_of_birth)
);

-- A claim starts in draft (POST /v1/claims), collects documents, and is submitted.
-- Its workflow (claim-<id>) is started through the outbox in the same transaction.
-- Statuses follow docs/plan/architecture.md: rejected means the packet can't be used, and
-- failed means retries ran out and an operator must step in. A denial is a decision that
-- people make, recorded as decided.
CREATE TABLE claims (
    id                   uuid        PRIMARY KEY,
    member_id            uuid        NOT NULL REFERENCES members (id),
    status               text        NOT NULL DEFAULT 'draft' CHECK (status IN (
                             'draft', 'submitted', 'validating', 'awaiting_documents',
                             'extracting', 'adjudicating', 'in_review', 'decided', 'closed',
                             'rejected', 'failed')),
    admitted_on          date        NOT NULL,
    discharged_on        date        NOT NULL,
    hospital_name        text        NOT NULL CHECK (length(hospital_name) BETWEEN 1 AND 200),
    claimed_amount_paise bigint      NOT NULL CHECK (claimed_amount_paise > 0),
    workflow_id          text        NOT NULL UNIQUE,
    created_by           text        NOT NULL CHECK (length(created_by) BETWEEN 1 AND 200),
    -- Optimistic lock: UPDATE ... WHERE id = $1 AND version = $2. The trigger below bumps it.
    version              integer     NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    submitted_at         timestamptz,
    decided_at           timestamptz,
    CHECK (discharged_on >= admitted_on),
    -- Exactly the drafts have no submission time.
    CHECK ((status = 'draft') = (submitted_at IS NULL)),
    -- Exactly the decided and closed claims have a decision time.
    CHECK ((status IN ('decided', 'closed')) = (decided_at IS NOT NULL)),
    CHECK (submitted_at >= created_at),
    CHECK (decided_at >= submitted_at)
);
CREATE INDEX claims_member_id_idx ON claims (member_id);

-- Every update bumps version and updated_at, so a writer can't skip the optimistic lock
-- by forgetting to bump it.
CREATE FUNCTION claims_bump_version() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.version := OLD.version + 1;
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;

CREATE TRIGGER claims_bump_version
    BEFORE UPDATE ON claims
    FOR EACH ROW EXECUTE FUNCTION claims_bump_version();

-- POST /v1/claims with a repeated Idempotency-Key returns the first response. Keys are
-- scoped to the caller; reusing a key with a different request is a 422.
CREATE TABLE idempotency_keys (
    principal       text        NOT NULL CHECK (length(principal) BETWEEN 1 AND 200),
    idempotency_key text        NOT NULL CHECK (idempotency_key ~ '^[\x21-\x7E]{1,255}$'),
    -- SHA-256 of the operation and the request body.
    request_hash    bytea       NOT NULL CHECK (length(request_hash) = 32),
    response_status smallint    NOT NULL CHECK (response_status BETWEEN 200 AND 599),
    response_body   jsonb       NOT NULL,
    claim_id        uuid        REFERENCES claims (id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    expires_at      timestamptz NOT NULL,
    PRIMARY KEY (principal, idempotency_key),
    CHECK (expires_at > created_at)
);
CREATE INDEX idempotency_keys_expires_at_idx ON idempotency_keys (expires_at);

-- One row per file. The client declares size and SHA-256 up front; the complete step checks
-- the stored object against them before the document is marked uploaded.
CREATE TABLE documents (
    id               uuid        PRIMARY KEY,
    claim_id         uuid        NOT NULL REFERENCES claims (id),
    status           text        NOT NULL DEFAULT 'pending_upload'
                     CHECK (status IN ('pending_upload', 'uploaded', 'rejected')),
    file_name        text        NOT NULL CHECK (length(file_name) BETWEEN 1 AND 255),
    content_type     text        NOT NULL
                     CHECK (content_type IN ('application/pdf', 'image/jpeg', 'image/png')),
    size_bytes       bigint      NOT NULL CHECK (size_bytes BETWEEN 1 AND 26214400),
    sha256           bytea       NOT NULL CHECK (length(sha256) = 32),
    object_key       text        NOT NULL UNIQUE,
    page_count       integer     CHECK (page_count BETWEEN 1 AND 500),
    rejection_reason text        CHECK (rejection_reason IN (
                         'missing_object', 'size_mismatch', 'checksum_mismatch',
                         'unreadable', 'encrypted', 'too_many_pages')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    uploaded_at      timestamptz,
    CHECK ((status = 'rejected') = (rejection_reason IS NOT NULL)),
    CHECK ((status = 'uploaded') = (uploaded_at IS NOT NULL))
);
CREATE INDEX documents_claim_id_idx ON documents (claim_id);
-- The same file can't be attached to a claim twice, but a rejected upload can be retried.
CREATE UNIQUE INDEX documents_claim_sha256_key ON documents (claim_id, sha256)
    WHERE status <> 'rejected';

-- Every message to a workflow is written here in the same transaction as the change that
-- causes it (ADR-0003), and the relay delivers it. Payloads carry IDs only (ADR-0004).
-- The relay reads unpublished rows in id order; event_id lets the workflow drop duplicates.
CREATE TABLE outbox (
    id              bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    event_id        uuid        NOT NULL UNIQUE,
    claim_id        uuid        NOT NULL REFERENCES claims (id),
    kind            text        NOT NULL CHECK (kind IN ('workflow_start', 'workflow_signal')),
    workflow_id     text        NOT NULL,
    -- The workflow type to start, or the signal to send.
    name            text        NOT NULL CHECK (name ~ '^[A-Za-z][A-Za-z0-9_]{0,63}$'),
    payload         jsonb       NOT NULL DEFAULT '{}'::jsonb,
    -- W3C trace context of the request that wrote the row, so the trace continues in the relay.
    traceparent     text        CHECK (traceparent ~ '^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$'),
    tracestate      text        CHECK (length(tracestate) <= 512),
    created_at      timestamptz NOT NULL DEFAULT now(),
    attempts        integer     NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    last_error      text,
    published_at    timestamptz,
    CHECK (jsonb_typeof(payload) = 'object'),
    -- IDs, not documents: a payload this size can't carry extracted text or files.
    CHECK (octet_length(payload::text) <= 2048)
);
CREATE INDEX outbox_unpublished_idx ON outbox (id) WHERE published_at IS NULL;
CREATE INDEX outbox_claim_id_idx ON outbox (claim_id);

-- Append-only and hash-chained per claim. Each event stores the hash of the one before it,
-- and the foreign key below makes the database check that link on every insert: no gaps,
-- no forks, no event pointing at a hash that isn't there. Recomputing each hash from the
-- event's content is the verifier job's work.
CREATE TABLE audit_events (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    claim_id    uuid        NOT NULL REFERENCES claims (id),
    seq         integer     NOT NULL CHECK (seq >= 1),
    prev_seq    integer     GENERATED ALWAYS AS (NULLIF(seq - 1, 0)) STORED,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_type  text        NOT NULL CHECK (actor_type IN ('user', 'service', 'model')),
    actor_id    text        NOT NULL CHECK (length(actor_id) BETWEEN 1 AND 200),
    action      text        NOT NULL CHECK (action ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'),
    -- IDs and amounts only: no names and no document text, so deleting a document later
    -- doesn't break the chain.
    details     jsonb       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(details) = 'object'),
    prev_hash   bytea       CHECK (length(prev_hash) = 32),
    hash        bytea       NOT NULL CHECK (length(hash) = 32),
    CONSTRAINT audit_events_claim_seq_key UNIQUE (claim_id, seq),
    CONSTRAINT audit_events_link_target_key UNIQUE (claim_id, seq, hash),
    CONSTRAINT audit_events_chain_link FOREIGN KEY (claim_id, prev_seq, prev_hash)
        REFERENCES audit_events (claim_id, seq, hash),
    CHECK ((seq = 1) = (prev_hash IS NULL))
);

-- Grants already keep claims_app to INSERT and SELECT. This trigger stops UPDATE, DELETE and
-- TRUNCATE for every role, the owner included. Disabling it takes an explicit ALTER TABLE.
CREATE FUNCTION audit_events_append_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only: % is not allowed', TG_OP
        USING ERRCODE = 'insufficient_privilege';
END;
$$;

CREATE TRIGGER audit_events_append_only
    BEFORE UPDATE OR DELETE OR TRUNCATE ON audit_events
    FOR EACH STATEMENT EXECUTE FUNCTION audit_events_append_only();

-- One row per model request a workflow step makes. Unique (claim, step, request hash) lets
-- a retried activity find the stored result instead of paying for the call again.
CREATE TABLE model_calls (
    id                  uuid        PRIMARY KEY,
    claim_id            uuid        NOT NULL REFERENCES claims (id),
    step                text        NOT NULL CHECK (step ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$'),
    request_hash        bytea       NOT NULL CHECK (length(request_hash) = 32),
    provider            text        NOT NULL CHECK (provider ~ '^[a-z][a-z0-9_-]{1,31}$'),
    model               text        NOT NULL CHECK (length(model) BETWEEN 1 AND 100),
    prompt_hash         bytea       NOT NULL CHECK (length(prompt_hash) = 32),
    status              text        NOT NULL DEFAULT 'started'
                        CHECK (status IN ('started', 'succeeded', 'failed')),
    attempts            integer     NOT NULL DEFAULT 1 CHECK (attempts >= 1),
    input_tokens        integer     CHECK (input_tokens >= 0),
    output_tokens       integer     CHECK (output_tokens >= 0),
    cache_read_tokens   integer     CHECK (cache_read_tokens >= 0),
    cache_write_tokens  integer     CHECK (cache_write_tokens >= 0),
    cost_usd_micros     bigint      CHECK (cost_usd_micros >= 0),
    latency_ms          integer     CHECK (latency_ms >= 0),
    -- The raw response lives in the object store; only its key is kept here.
    response_object_key text,
    error_code          text,
    started_at          timestamptz NOT NULL DEFAULT now(),
    finished_at         timestamptz,
    UNIQUE (claim_id, step, request_hash),
    CHECK ((status = 'started') = (finished_at IS NULL)),
    CHECK ((status = 'succeeded') = (response_object_key IS NOT NULL)),
    CHECK ((status = 'failed') = (error_code IS NOT NULL)),
    CHECK (finished_at >= started_at)
);

-- What the services may do. Keep scripts/db-check.sh in step with this list.
GRANT SELECT ON policies, policy_periods, members TO claims_app;
GRANT SELECT, INSERT ON claims, documents, audit_events, model_calls TO claims_app;
GRANT UPDATE (status, submitted_at, decided_at) ON claims TO claims_app;
GRANT UPDATE (status, page_count, rejection_reason, uploaded_at) ON documents TO claims_app;
GRANT UPDATE (status, attempts, input_tokens, output_tokens, cache_read_tokens,
              cache_write_tokens, cost_usd_micros, latency_ms, response_object_key,
              error_code, finished_at) ON model_calls TO claims_app;
GRANT SELECT, INSERT, DELETE ON idempotency_keys, outbox TO claims_app;
GRANT UPDATE (attempts, next_attempt_at, last_error, published_at) ON outbox TO claims_app;

COMMIT;
