-- +goose Up

-- bt_bill_definitions holds the recurring/one-off rule for a bill (name,
-- amount, schedule). Actual per-cycle due dates and paid status live in
-- bt_bill_instances, mirroring hhq's chore_definitions/chore_instances
-- split - a monthly bill's "paid" status must reset every cycle, not stay
-- permanently set the first time it's marked paid.
CREATE TABLE bt_bill_definitions (
    id                    SERIAL PRIMARY KEY,
    name                  TEXT NOT NULL,
    amount_cents          INTEGER NOT NULL,
    -- 'monthly' and 'quarterly' use day_of_month; 'quarterly' additionally
    -- uses quarter_start_month to pick which 3-month rotation it falls on
    -- (1 = Jan/Apr/Jul/Oct, 2 = Feb/May/Aug/Nov, 3 = Mar/Jun/Sep/Dec);
    -- 'one_off' uses one_off_date. 'vendor' uses none of them - its due
    -- date/amount are fully driven by a linked bt_vendor_connections row
    -- instead (see internal/connectors). Exactly one schedule shape may be
    -- set, enforced by the CHECK below.
    schedule_type         TEXT NOT NULL CHECK (schedule_type IN ('monthly', 'quarterly', 'one_off', 'vendor')),
    day_of_month          SMALLINT CHECK (day_of_month BETWEEN 1 AND 31),
    quarter_start_month   SMALLINT CHECK (quarter_start_month BETWEEN 1 AND 3),
    one_off_date          DATE,
    CONSTRAINT bt_bill_definitions_schedule_shape_check CHECK (
        (schedule_type = 'monthly' AND day_of_month IS NOT NULL AND one_off_date IS NULL AND quarter_start_month IS NULL)
        OR
        (schedule_type = 'quarterly' AND day_of_month IS NOT NULL AND quarter_start_month IS NOT NULL AND one_off_date IS NULL)
        OR
        (schedule_type = 'one_off' AND one_off_date IS NOT NULL AND day_of_month IS NULL AND quarter_start_month IS NULL)
        OR
        (schedule_type = 'vendor' AND day_of_month IS NULL AND quarter_start_month IS NULL AND one_off_date IS NULL)
    ),
    -- Placeholder for a future "pay this bill directly with the vendor"
    -- integration - not implemented yet, just a link shown on the settings
    -- page (see README's Known Gaps).
    vendor_url            TEXT,
    -- Optional hook for a future "derive amount/paid status from a linked
    -- SimpleFIN credit-card account" feature - not implemented yet. FK added
    -- below (via ALTER TABLE) since bt_accounts is defined after this table.
    simplefin_account_id  INTEGER,
    -- True if this row is created/kept in sync by the CONFIG_DIR/bills.json
    -- bootstrap file rather than through the settings UI.
    bootstrap_managed     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- bt_accounts holds bank/card accounts synced from a SimpleFIN Bridge
-- connection (see bt_simplefin_connection). Referenced above by
-- bt_bill_definitions.simplefin_account_id, so it must be created first.
CREATE TABLE bt_accounts (
    id                        SERIAL PRIMARY KEY,
    simplefin_id              TEXT NOT NULL UNIQUE,
    org_name                  TEXT,
    name                      TEXT NOT NULL,
    -- Optional parent-set label shown instead of the raw SimpleFIN-reported
    -- name. NULL/empty means "use name". Preserved across syncs (excluded
    -- from Upsert's ON CONFLICT overwrite), same pattern as `visible`.
    display_name              TEXT,
    currency                  TEXT NOT NULL DEFAULT 'USD',
    balance_cents             BIGINT NOT NULL,
    available_balance_cents   BIGINT,
    balance_date              TIMESTAMPTZ,
    -- Lets a parent hide an account (e.g. a closed/irrelevant one) from the
    -- kiosk balances panel without disconnecting SimpleFIN entirely.
    visible                   BOOLEAN NOT NULL DEFAULT TRUE,
    last_synced_at            TIMESTAMPTZ,
    last_sync_error           TEXT,
    created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE bt_bill_definitions
    ADD CONSTRAINT bt_bill_definitions_simplefin_account_id_fkey
    FOREIGN KEY (simplefin_account_id) REFERENCES bt_accounts(id) ON DELETE SET NULL;

-- bt_bill_instances is one row per due-date occurrence of a bill
-- definition - see the comment on bt_bill_definitions above.
CREATE TABLE bt_bill_instances (
    id                    SERIAL PRIMARY KEY,
    bill_definition_id    INTEGER NOT NULL REFERENCES bt_bill_definitions(id) ON DELETE CASCADE,
    due_date              DATE NOT NULL,
    paid                  BOOLEAN NOT NULL DEFAULT FALSE,
    paid_at               TIMESTAMPTZ,
    UNIQUE (bill_definition_id, due_date)
);
CREATE INDEX idx_bt_bill_instances_due_date ON bt_bill_instances (due_date);

-- bt_simplefin_connection is a singleton row (the app enforces at most one)
-- holding the AES-GCM-encrypted SimpleFIN access URL exchanged for a
-- parent-pasted setup token (see internal/simplefin and the settings page).
CREATE TABLE bt_simplefin_connection (
    id                      SERIAL PRIMARY KEY,
    encrypted_access_url    BYTEA NOT NULL,
    connected_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_synced_at          TIMESTAMPTZ,
    last_sync_error         TEXT
);

-- bt_settings is a plain key/value table, mirroring hhq's own settings
-- table, for future parent-editable plugin config.
CREATE TABLE bt_settings (
    key    TEXT PRIMARY KEY,
    value  TEXT NOT NULL
);

-- bt_vendor_connections links a bill definition to a vendor bill-pay portal
-- login (e.g. a billeriq.com-hosted utility) so the scheduler can log in on
-- the bill's behalf and pull the current due date/amount/account number
-- straight from the vendor, instead of relying on a hand-maintained amount.
-- One connection per bill (UNIQUE bill_definition_id) - a bill either has a
-- vendor connection or it doesn't; there's no case for multiple logins
-- feeding the same bill.
CREATE TABLE bt_vendor_connections (
    id                    SERIAL PRIMARY KEY,
    bill_definition_id    INTEGER NOT NULL UNIQUE REFERENCES bt_bill_definitions(id) ON DELETE CASCADE,
    -- Registry key for internal/connectors (e.g. "billeriq") - dispatches to
    -- the right Connector implementation.
    connector             TEXT NOT NULL,
    -- Connector-specific identifier for which instance of the vendor's
    -- platform to use (e.g. billeriq's per-utility path segment,
    -- "WVWAuthority"). Opaque to everything outside the connector itself.
    tenant                TEXT NOT NULL,
    username              TEXT NOT NULL,
    encrypted_password    BYTEA NOT NULL,
    -- Last account number the vendor reported, shown on the settings page so
    -- a parent can confirm the connection is pointed at the right account.
    -- Not used for anything structural.
    last_account_number   TEXT,
    last_synced_at        TIMESTAMPTZ,
    last_sync_error       TEXT,
    -- True if this row is created/kept in sync by a VENDOR_CONNECTIONS
    -- bootstrap file rather than through the settings UI - same convention
    -- as bt_bill_definitions.bootstrap_managed.
    bootstrap_managed     BOOLEAN NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE bt_vendor_connections;
DROP TABLE bt_settings;
DROP TABLE bt_simplefin_connection;
DROP TABLE bt_bill_instances;
ALTER TABLE bt_bill_definitions DROP CONSTRAINT bt_bill_definitions_simplefin_account_id_fkey;
DROP TABLE bt_accounts;
DROP TABLE bt_bill_definitions;
