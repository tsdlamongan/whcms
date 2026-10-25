-- Domain sniper: hunt a dropping/expired domain in the background until the
-- registrar accepts the registration.
--
--   snipe_plans          admin-priced hunt packages (duration + fee)
--   domain_snipes        one hunt per row; the worker supervisor claims
--                        'active' rows under a short SQL lease
--   domain_snipe_events  small, deduplicated activity trail (phase changes,
--                        errors, win, refund) - never one row per attempt
--
-- Money is whole-rupiah BIGINT (CONTRACTS §0). A client snipe is billed up
-- front on one invoice (fee + domain registration price); refundable_* hold
-- the pro-rata split of invoice.total that is credited back when the hunt
-- ends without a win (see the snipe module's refund rules).

CREATE TABLE snipe_plans (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name        TEXT NOT NULL,
    window_days INT NOT NULL CHECK (window_days BETWEEN 1 AND 365),
    fee         BIGINT NOT NULL DEFAULT 0 CHECK (fee >= 0),
    success_fee BIGINT NOT NULL DEFAULT 0 CHECK (success_fee >= 0),
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    sort        INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER snipe_plans_updated_at BEFORE UPDATE ON snipe_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE domain_snipes (
    id                    BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id             BIGINT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    plan_id               BIGINT NULL REFERENCES snipe_plans(id) ON DELETE SET NULL,
    source                TEXT NOT NULL DEFAULT 'client' CHECK (source IN ('client', 'admin')),
    domain_name           TEXT NOT NULL,
    years                 INT NOT NULL DEFAULT 1 CHECK (years BETWEEN 1 AND 10),
    status                TEXT NOT NULL DEFAULT 'pending_payment'
                          CHECK (status IN ('pending_payment', 'active', 'won', 'expired', 'cancelled', 'failed')),
    phase                 TEXT NOT NULL DEFAULT 'patient' CHECK (phase IN ('patient', 'burst')),
    hunt_from             TIMESTAMPTZ NULL,
    hunt_until            TIMESTAMPTZ NULL,
    nameservers           JSONB NOT NULL DEFAULT '[]',
    invoice_id            BIGINT NULL REFERENCES invoices(id) ON DELETE SET NULL,
    fee_amount            BIGINT NOT NULL DEFAULT 0,
    domain_amount         BIGINT NOT NULL DEFAULT 0,
    renew_per_year        BIGINT NOT NULL DEFAULT 0,
    refundable_domain     BIGINT NOT NULL DEFAULT 0,
    refundable_fee        BIGINT NOT NULL DEFAULT 0,
    refund_amount         BIGINT NOT NULL DEFAULT 0,
    refunded_at           TIMESTAMPTZ NULL,
    registrar_customer_id BIGINT NULL,
    attempts              BIGINT NOT NULL DEFAULT 0,
    last_attempt_at       TIMESTAMPTZ NULL,
    last_error            TEXT NOT NULL DEFAULT '',
    lease_owner           TEXT NULL,
    lease_until           TIMESTAMPTZ NULL,
    paid_at               TIMESTAMPTZ NULL,
    deadline_at           TIMESTAMPTZ NULL,
    won_at                TIMESTAMPTZ NULL,
    won_domain_id         BIGINT NULL REFERENCES domains(id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (hunt_from IS NULL OR hunt_until IS NULL OR hunt_from < hunt_until),
    CHECK (fee_amount >= 0 AND domain_amount >= 0 AND renew_per_year >= 0
           AND refundable_domain >= 0 AND refundable_fee >= 0 AND refund_amount >= 0)
);

-- One open hunt per name: closes the race of two clients paying for the same
-- domain (pending_payment rows are swept once their invoice lapses).
CREATE UNIQUE INDEX domain_snipes_name_open_uniq
    ON domain_snipes (domain_name) WHERE status IN ('pending_payment', 'active');
CREATE INDEX domain_snipes_claim_idx ON domain_snipes (status, lease_until);
CREATE INDEX domain_snipes_client_idx ON domain_snipes (client_id, created_at DESC);
CREATE INDEX domain_snipes_invoice_idx ON domain_snipes (invoice_id);
CREATE INDEX domain_snipes_refund_idx ON domain_snipes (refunded_at) WHERE refund_amount > 0 AND refunded_at IS NULL;

CREATE TRIGGER domain_snipes_updated_at BEFORE UPDATE ON domain_snipes
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE domain_snipe_events (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    snipe_id   BIGINT NOT NULL REFERENCES domain_snipes(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    message    TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX domain_snipe_events_snipe_idx ON domain_snipe_events (snipe_id, created_at DESC);

-- The fee line of a snipe invoice activates the hunt once paid.
ALTER TABLE invoice_items DROP CONSTRAINT IF EXISTS invoice_items_related_type_check;
ALTER TABLE invoice_items ADD CONSTRAINT invoice_items_related_type_check
    CHECK (related_type IN ('order_item','service_renewal','domain_renewal','service_upgrade','late_fee','deposit','manual','domain_snipe'));

-- Operational knobs (admin-editable, no restart). Defaults are deliberately
-- conservative so a fleet of hunts cannot get the reseller API key blocked.
INSERT INTO settings (key, value) VALUES
    ('snipe.enabled',                  'true'),
    ('snipe.max_active_global',        '50'),
    ('snipe.max_in_flight',            '2'),
    ('snipe.burst_interval_ms',        '200'),
    ('snipe.burst_jitter_ms',          '100'),
    ('snipe.patient_interval_seconds', '10'),
    ('snipe.burst_lead_seconds',       '60'),
    ('snipe.rate_limit_backoff_ms',    '2000'),
    ('snipe.request_timeout_ms',       '3000'),
    ('snipe.max_active_per_client',    '5'),
    ('snipe.invoice_due_hours',        '24'),
    ('snipe.min_reseller_balance',     '0'),
    ('snipe.pause_probe_seconds',      '60')
ON CONFLICT (key) DO NOTHING;
