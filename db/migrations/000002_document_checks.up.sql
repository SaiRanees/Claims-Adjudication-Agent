-- The worker's verdict on each file (docs/plan/week-3.md, "Data"). A file is pending_upload
-- until complete checks the stored object, uploaded until the worker checks its bytes, and
-- then accepted or rejected. Complete can reject before upload; the worker rejects after.
BEGIN;

ALTER TABLE documents
    ADD COLUMN checked_at timestamptz,
    -- The SHA-256 of the PDF later steps read: the upload itself, or a photo's PDF.
    ADD COLUMN pdf_sha256 bytea CHECK (length(pdf_sha256) = 32);

-- 000001's table-level CHECKs have generated names, read from \d documents:
-- documents_check1 is (status = 'uploaded') = (uploaded_at IS NOT NULL), which would stop an
-- accepted file keeping its upload time.
ALTER TABLE documents
    DROP CONSTRAINT documents_status_check,
    DROP CONSTRAINT documents_rejection_reason_check,
    DROP CONSTRAINT documents_check1;

ALTER TABLE documents
    ADD CONSTRAINT documents_status_check
        CHECK (status IN ('pending_upload', 'uploaded', 'accepted', 'rejected')),
    ADD CONSTRAINT documents_rejection_reason_check CHECK (rejection_reason IN (
        'missing_object', 'size_mismatch', 'checksum_mismatch', 'type_mismatch',
        'unreadable', 'encrypted', 'too_many_pages', 'image_too_large', 'check_failed')),
    -- Which times a file has follows from its status. A rejected file was checked exactly
    -- when it was uploaded: complete rejects before upload, the worker after.
    ADD CONSTRAINT documents_times_check CHECK (CASE status
        WHEN 'pending_upload' THEN uploaded_at IS NULL AND checked_at IS NULL
        WHEN 'uploaded'       THEN uploaded_at IS NOT NULL AND checked_at IS NULL
        WHEN 'accepted'       THEN uploaded_at IS NOT NULL AND checked_at IS NOT NULL
        WHEN 'rejected'       THEN (checked_at IS NOT NULL) = (uploaded_at IS NOT NULL)
    END),
    -- An accepted file has what later steps need, and only an accepted file has a PDF.
    ADD CONSTRAINT documents_accepted_check CHECK (
        (status = 'accepted') = (pdf_sha256 IS NOT NULL)
        AND (status <> 'accepted' OR page_count IS NOT NULL)),
    ADD CONSTRAINT documents_checked_after_upload_check CHECK (checked_at >= uploaded_at);

-- Status only moves forward: pending_upload to uploaded or rejected, uploaded to accepted or
-- rejected. An accepted or rejected file never changes again.
CREATE FUNCTION documents_forward_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('accepted', 'rejected')
       OR (OLD.status = 'uploaded' AND NEW.status = 'pending_upload')
       OR (OLD.status = 'pending_upload' AND NEW.status = 'accepted') THEN
        RAISE EXCEPTION 'documents: % to % is not allowed', OLD.status, NEW.status
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER documents_forward_only
    BEFORE UPDATE ON documents
    FOR EACH ROW EXECUTE FUNCTION documents_forward_only();

GRANT UPDATE (checked_at, pdf_sha256) ON documents TO claims_app;

COMMIT;
