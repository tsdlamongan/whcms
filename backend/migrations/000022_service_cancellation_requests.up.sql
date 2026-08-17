-- Tracks client-submitted service cancellation requests as a first-class,
-- reviewable entity instead of a bare panel_meta flag. Immediate-mode
-- requests are recorded already 'auto_processed' (the cancellation itself
-- already ran synchronously - no admin action needed); end_of_term requests
-- start 'pending' and only take effect once an admin accepts them. The
-- partial unique index caps a service to one outstanding pending request at
-- a time.
CREATE TABLE service_cancellation_requests (
    id           BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    service_id   BIGINT NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    client_id    BIGINT NOT NULL REFERENCES clients(id) ON DELETE CASCADE,
    mode         TEXT NOT NULL CHECK (mode IN ('immediate', 'end_of_term')),
    reason       TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'accepted', 'rejected', 'auto_processed')),
    requested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at   TIMESTAMPTZ NULL,
    decided_by   BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX service_cancellation_requests_service_id_idx ON service_cancellation_requests(service_id);
CREATE INDEX service_cancellation_requests_status_idx ON service_cancellation_requests(status);
CREATE UNIQUE INDEX service_cancellation_requests_pending_unique_idx
    ON service_cancellation_requests(service_id) WHERE status = 'pending';

CREATE TRIGGER service_cancellation_requests_updated_at BEFORE UPDATE ON service_cancellation_requests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
