CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS searches (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    niche        VARCHAR(255) NOT NULL,
    location     VARCHAR(255) NOT NULL,
    quantity     INT          NOT NULL,
    status       VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED', 'FAILED')),
    progress     INT          NOT NULL DEFAULT 0,
    error_msg    TEXT,
    started_at   TIMESTAMP,
    completed_at TIMESTAMP,
    created_at   TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS companies (
    id          UUID      PRIMARY KEY DEFAULT gen_random_uuid(),
    search_id   UUID      NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
    name        TEXT,
    location    TEXT,
    phone       VARCHAR(50),
    website     TEXT,
    created_at  TIMESTAMP NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_companies_search_id  ON companies(search_id);
CREATE INDEX IF NOT EXISTS idx_searches_created_at  ON searches(created_at);
CREATE INDEX IF NOT EXISTS idx_companies_created_at ON companies(created_at);

-- ---------------------------------------------------------------------------
-- WhatsApp multi-device sessions (whatsmeow integration)
--
-- App-level metadata for each WhatsApp number paired via whatsmeow. The actual
-- encrypted device/session material is NOT stored here: whatsmeow manages it in
-- its own tables, created at runtime by container.Upgrade() (whatsmeow_device,
-- whatsmeow_identity_keys, whatsmeow_sessions, ...). session_data only holds a
-- JSON pointer back to that device — primarily the canonical JID, e.g.
-- {"jid":"5511999999999:3@s.whatsapp.net"} — so a CONNECTED row can be matched
-- to its stored device and reconnected on startup.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS whatsapp_sessions (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    session_data JSONB        NOT NULL DEFAULT '{}'::jsonb,
    phone_number VARCHAR(30),
    status       VARCHAR(20)  NOT NULL DEFAULT 'DISCONNECTED'
                 CHECK (status IN ('CONNECTING', 'CONNECTED', 'DISCONNECTED')),
    created_at   TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_whatsapp_sessions_status     ON whatsapp_sessions(status);
CREATE INDEX IF NOT EXISTS idx_whatsapp_sessions_created_at ON whatsapp_sessions(created_at);
CREATE INDEX IF NOT EXISTS idx_whatsapp_sessions_jid        ON whatsapp_sessions((session_data->>'jid'));

-- ---------------------------------------------------------------------------
-- Messaging campaigns (intelligent WhatsApp dispatch)
--
-- A campaign blasts one message_body to every phone discovered by a search,
-- through a single connected WhatsApp number, paced by the anti-ban dispatch
-- engine (random delay + per-number hourly rate limit + number validation).
--
-- whatsapp_session_id is intentionally NOT a foreign key: a number can be
-- disconnected/removed while its past campaigns must remain auditable. search_id
-- cascades, so deleting a search reaps its campaigns too. Live progress is the
-- running (sent_count, failed_count) against total_count.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS campaigns (
    id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    search_id           UUID         NOT NULL REFERENCES searches(id) ON DELETE CASCADE,
    whatsapp_session_id UUID         NOT NULL,
    message_body        TEXT         NOT NULL,
    status              VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                        CHECK (status IN ('PENDING', 'RUNNING', 'COMPLETED')),
    total_count         INT          NOT NULL DEFAULT 0,
    sent_count          INT          NOT NULL DEFAULT 0,
    failed_count        INT          NOT NULL DEFAULT 0,
    created_at          TIMESTAMP    NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_campaigns_search_id  ON campaigns(search_id);
CREATE INDEX IF NOT EXISTS idx_campaigns_created_at ON campaigns(created_at);
CREATE INDEX IF NOT EXISTS idx_campaigns_session_status ON campaigns(whatsapp_session_id, status);

-- ---------------------------------------------------------------------------
-- Per-recipient durable queue for campaign dispatch.
--
-- Every lead is persisted as a row so multi-day / multi-week sends survive API
-- restarts. The worker claims PENDING rows with FOR UPDATE SKIP LOCKED, marks
-- them SENDING while in flight, then SENT / FAILED / SKIPPED. scheduled_at
-- spaces sends (jitter + rate limit) and requeues when the session is offline.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS campaign_messages (
    id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    campaign_id  UUID         NOT NULL REFERENCES campaigns(id) ON DELETE CASCADE,
    phone        VARCHAR(30)  NOT NULL,
    status       VARCHAR(20)  NOT NULL DEFAULT 'PENDING'
                 CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED', 'SKIPPED')),
    attempts     INT          NOT NULL DEFAULT 0,
    last_error   TEXT,
    scheduled_at TIMESTAMP    NOT NULL DEFAULT NOW(),
    sent_at      TIMESTAMP,
    created_at   TIMESTAMP    NOT NULL DEFAULT NOW(),
    UNIQUE (campaign_id, phone)
);

CREATE INDEX IF NOT EXISTS idx_campaign_messages_claim
    ON campaign_messages (status, scheduled_at)
    WHERE status = 'PENDING';
CREATE INDEX IF NOT EXISTS idx_campaign_messages_campaign
    ON campaign_messages (campaign_id, status);

-- Cleanup function called periodically by the Go service (internal/usecase/cleanup).
-- Deletes searches older than 45 days; companies and campaigns are removed via
-- ON DELETE CASCADE. Incomplete campaigns (still PENDING/RUNNING with work left)
-- are never pruned by age so long AFK blasts are not wiped mid-flight.
-- ---------------------------------------------------------------------------
-- Migration: async search progress tracking (idempotent ALTER TABLE stmts)
-- These are no-ops on fresh installs where the CREATE TABLE above already
-- includes the columns. On existing databases they add the columns safely.
-- ---------------------------------------------------------------------------
ALTER TABLE searches ADD COLUMN IF NOT EXISTS status       VARCHAR(20) NOT NULL DEFAULT 'COMPLETED';
ALTER TABLE searches ADD COLUMN IF NOT EXISTS progress     INT         NOT NULL DEFAULT 0;
ALTER TABLE searches ADD COLUMN IF NOT EXISTS error_msg    TEXT;
ALTER TABLE searches ADD COLUMN IF NOT EXISTS started_at   TIMESTAMP;
ALTER TABLE searches ADD COLUMN IF NOT EXISTS completed_at TIMESTAMP;

-- Backfill pre-existing rows: they were all run synchronously → COMPLETED.
UPDATE searches SET progress = quantity WHERE progress = 0 AND status = 'COMPLETED';

-- Fix: company fields can exceed 255 chars (long URLs/addresses).
ALTER TABLE companies ALTER COLUMN name     TYPE TEXT;
ALTER TABLE companies ALTER COLUMN location TYPE TEXT;
ALTER TABLE companies ALTER COLUMN website  TYPE TEXT;

-- ---------------------------------------------------------------------------

CREATE OR REPLACE FUNCTION cleanup_old_searches() RETURNS void AS $$
BEGIN
    -- Only drop finished campaigns past retention; leave in-flight queues alone.
    DELETE FROM campaigns c
     WHERE c.created_at < NOW() - INTERVAL '45 days'
       AND c.status = 'COMPLETED'
       AND NOT EXISTS (
           SELECT 1 FROM campaign_messages m
            WHERE m.campaign_id = c.id
              AND m.status IN ('PENDING', 'SENDING')
       );
    DELETE FROM searches  WHERE created_at < NOW() - INTERVAL '45 days';
END;
$$ LANGUAGE plpgsql;
