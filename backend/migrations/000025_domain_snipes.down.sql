DELETE FROM settings WHERE key LIKE 'snipe.%';

-- Snipe invoice lines lose their activation hook once the type is gone; keep
-- the historical invoice rows as plain manual lines.
UPDATE invoice_items SET related_type = 'manual', related_id = NULL WHERE related_type = 'domain_snipe';
ALTER TABLE invoice_items DROP CONSTRAINT IF EXISTS invoice_items_related_type_check;
ALTER TABLE invoice_items ADD CONSTRAINT invoice_items_related_type_check
    CHECK (related_type IN ('order_item','service_renewal','domain_renewal','service_upgrade','late_fee','deposit','manual'));

DROP TABLE IF EXISTS domain_snipe_events;
DROP TABLE IF EXISTS domain_snipes;
DROP TABLE IF EXISTS snipe_plans;
