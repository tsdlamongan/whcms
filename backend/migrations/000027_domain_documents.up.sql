-- Domain document / verification lifecycle (docs.dewabiz.co.id "Auto
-- Activation" + "Syarat Domain Indonesia"; PRD FR-DOM-009).
--
-- .co.id/.or.id/.ac.id/.sch.id/.ponpes.id need identity/legal documents. With
-- Auto Activation the registrar activates the domain on payment and the client
-- uploads the documents afterwards (day 1-3), a reminder + suspend warning
-- follows on day 4, the domain is suspended on day 5 and deleted on day 30 -
-- with no refund when the documents were never uploaded or were rejected. The
-- other .id TLDs only need an email verification (suspended after 30 days).
--
-- This migration mirrors the registrar's view of that lifecycle onto the
-- domain row and records the refund the platform issued when the registrar
-- rejected the domain.

-- Two new states: 'suspended' (documents/verification overdue, or an admin
-- suspension at the registrar) and 'rejected' (the registrar refused or
-- deleted the registration - the refund rules hang off this state).
ALTER TABLE domains DROP CONSTRAINT IF EXISTS domains_status_check;
ALTER TABLE domains ADD CONSTRAINT domains_status_check
    CHECK (status IN ('pending','active','pending_transfer','expired','cancelled','suspended','rejected'));

-- A rejected registration frees the name again, exactly like a cancelled one.
DROP INDEX IF EXISTS domains_name_active_uniq;
CREATE UNIQUE INDEX domains_name_active_uniq ON domains (name)
    WHERE status NOT IN ('cancelled','rejected');

ALTER TABLE domains
    -- registrar `is_required_document`: the domain needs documents uploaded.
    ADD COLUMN document_required BOOLEAN NOT NULL DEFAULT FALSE,
    -- registrar `verification_status` (0 Waiting, 1 Verifying, 2 Document
    -- Validating, 3 Active); '' = never reported.
    ADD COLUMN verification_status TEXT NOT NULL DEFAULT ''
        CHECK (verification_status IN ('','waiting','verifying','document_validating','active')),
    -- registrar `status_reason`: why it was suspended/rejected (shown to the client).
    ADD COLUMN status_reason TEXT NOT NULL DEFAULT '',
    -- set when a document review bounced the upload (document_validating ->
    -- waiting); cleared once the client re-submits or the domain verifies.
    ADD COLUMN document_rejected_at TIMESTAMPTZ NULL,
    -- registrar `is_refundable` as last reported.
    ADD COLUMN registrar_refundable BOOLEAN NOT NULL DEFAULT FALSE,
    -- whole IDR credited back to the client for a rejected registration, and when.
    ADD COLUMN refund_amount BIGINT NOT NULL DEFAULT 0 CHECK (refund_amount >= 0),
    ADD COLUMN refunded_at TIMESTAMPTZ NULL;

-- The verification poller (cron:domain_doc_sync) scans only this small set.
CREATE INDEX domains_awaiting_verification_idx ON domains (updated_at)
    WHERE verification_status IN ('waiting','verifying','document_validating')
      AND status IN ('pending','active','pending_transfer','suspended');
