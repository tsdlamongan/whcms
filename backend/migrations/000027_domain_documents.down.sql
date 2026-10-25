DROP INDEX IF EXISTS domains_awaiting_verification_idx;

ALTER TABLE domains
    DROP COLUMN IF EXISTS refunded_at,
    DROP COLUMN IF EXISTS refund_amount,
    DROP COLUMN IF EXISTS registrar_refundable,
    DROP COLUMN IF EXISTS document_rejected_at,
    DROP COLUMN IF EXISTS status_reason,
    DROP COLUMN IF EXISTS verification_status,
    DROP COLUMN IF EXISTS document_required;

-- The old state set has no 'suspended'/'rejected': a suspended domain still
-- exists at the registrar (active again), a rejected one is gone (cancelled).
UPDATE domains SET status = 'active' WHERE status = 'suspended';
UPDATE domains SET status = 'cancelled' WHERE status = 'rejected';

DROP INDEX IF EXISTS domains_name_active_uniq;
CREATE UNIQUE INDEX domains_name_active_uniq ON domains (name) WHERE status <> 'cancelled';

ALTER TABLE domains DROP CONSTRAINT IF EXISTS domains_status_check;
ALTER TABLE domains ADD CONSTRAINT domains_status_check
    CHECK (status IN ('pending','active','pending_transfer','expired','cancelled'));
