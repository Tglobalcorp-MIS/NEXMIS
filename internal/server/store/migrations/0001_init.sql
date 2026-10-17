-- NEXMIS initial schema

CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE settings (
    key        text PRIMARY KEY,
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    username        text NOT NULL,
    display_name    text NOT NULL DEFAULT '',
    email           text NOT NULL DEFAULT '',
    source          text NOT NULL CHECK (source IN ('local', 'ad')),
    password_hash   text NOT NULL DEFAULT '',
    role            text NOT NULL CHECK (role IN ('admin', 'operator', 'helpdesk', 'viewer')),
    disabled        boolean NOT NULL DEFAULT false,
    failed_logins   int NOT NULL DEFAULT 0,
    locked_until    timestamptz,
    last_login_at   timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_uq ON users (lower(username));

CREATE TABLE sessions (
    id           text PRIMARY KEY, -- sha256(token), hex
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    ip           text NOT NULL DEFAULT '',
    user_agent   text NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

CREATE TABLE device_groups (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text NOT NULL UNIQUE,
    description    text NOT NULL DEFAULT '',
    consent_policy text NOT NULL DEFAULT 'always_ask'
                   CHECK (consent_policy IN ('always_ask', 'notify_only', 'deny')),
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE enrollment_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    token_hash   text NOT NULL UNIQUE,
    token_prefix text NOT NULL,
    expires_at   timestamptz,
    max_uses     int,
    uses         int NOT NULL DEFAULT 0,
    group_id     uuid REFERENCES device_groups(id) ON DELETE SET NULL,
    tags         text[] NOT NULL DEFAULT '{}',
    revoked      boolean NOT NULL DEFAULT false,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE devices (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name             text NOT NULL,
    hostname         text NOT NULL DEFAULT '',
    domain           text NOT NULL DEFAULT '',
    machine_id       text UNIQUE,
    agent_id         uuid UNIQUE,
    agent_public_key bytea,
    agent_version    text NOT NULL DEFAULT '',
    device_type      text NOT NULL DEFAULT 'unknown'
                     CHECK (device_type IN ('windows', 'server', 'printer', 'network', 'storage', 'other', 'unknown')),
    sources          text[] NOT NULL DEFAULT '{}',
    os_name          text NOT NULL DEFAULT '',
    os_version       text NOT NULL DEFAULT '',
    os_edition       text NOT NULL DEFAULT '',
    manufacturer     text NOT NULL DEFAULT '',
    model            text NOT NULL DEFAULT '',
    serial_number    text NOT NULL DEFAULT '',
    chassis_type     text NOT NULL DEFAULT '',
    ip_addresses     text[] NOT NULL DEFAULT '{}',
    mac_addresses    text[] NOT NULL DEFAULT '{}',
    logged_in_user   text NOT NULL DEFAULT '',
    session_state    text NOT NULL DEFAULT '',
    helper_online    boolean NOT NULL DEFAULT false,
    online           boolean NOT NULL DEFAULT false,
    last_seen_at     timestamptz,
    first_seen_at    timestamptz NOT NULL DEFAULT now(),
    enrolled_at      timestamptz,
    group_id         uuid REFERENCES device_groups(id) ON DELETE SET NULL,
    tags             text[] NOT NULL DEFAULT '{}',
    lifecycle_state  text NOT NULL DEFAULT 'in_use'
                     CHECK (lifecycle_state IN ('procurement', 'in_stock', 'in_use', 'repair', 'idle', 'retired')),
    asset_tag        text NOT NULL DEFAULT '',
    owner_name       text NOT NULL DEFAULT '',
    department       text NOT NULL DEFAULT '',
    location         text NOT NULL DEFAULT '',
    purchase_date    date,
    warranty_end     date,
    notes            text NOT NULL DEFAULT '',
    ad_dn            text NOT NULL DEFAULT '',
    ad_enabled       boolean,
    ad_last_logon    timestamptz,
    cpu_percent      real,
    mem_percent      real,
    uptime_sec       bigint,
    pending_reboot   boolean NOT NULL DEFAULT false,
    battery_health   real,
    health_status    text NOT NULL DEFAULT 'unknown'
                     CHECK (health_status IN ('ok', 'warning', 'critical', 'unknown')),
    issues           jsonb NOT NULL DEFAULT '[]',
    issue_count      int NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX devices_name_idx ON devices (lower(name));
CREATE INDEX devices_group_idx ON devices (group_id);
CREATE INDEX devices_tags_idx ON devices USING gin (tags);
CREATE INDEX devices_ip_idx ON devices USING gin (ip_addresses);
CREATE INDEX devices_mac_idx ON devices USING gin (mac_addresses);
CREATE INDEX devices_hostname_idx ON devices (lower(hostname));

CREATE TABLE device_inventory (
    device_id    uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('hardware', 'software')),
    hash         text NOT NULL,
    data         jsonb NOT NULL,
    collected_at timestamptz NOT NULL,
    PRIMARY KEY (device_id, kind)
);

CREATE TABLE device_software (
    device_id    uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    name         text NOT NULL,
    version      text NOT NULL DEFAULT '',
    publisher    text NOT NULL DEFAULT '',
    install_date text NOT NULL DEFAULT '',
    arch         text NOT NULL DEFAULT '',
    scope        text NOT NULL DEFAULT '',
    uninstall_id text NOT NULL DEFAULT ''
);
CREATE INDEX device_software_device_idx ON device_software (device_id);
CREATE INDEX device_software_name_idx ON device_software (lower(name));

CREATE TABLE inventory_changes (
    id        bigserial PRIMARY KEY,
    device_id uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind      text NOT NULL,
    change    text NOT NULL CHECK (change IN ('added', 'removed', 'changed')),
    item      text NOT NULL,
    old_value text NOT NULL DEFAULT '',
    new_value text NOT NULL DEFAULT '',
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX inventory_changes_device_idx ON inventory_changes (device_id, at DESC);

CREATE TABLE health_snapshots (
    id           bigserial PRIMARY KEY,
    device_id    uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    collected_at timestamptz NOT NULL,
    status       text NOT NULL,
    cpu_percent  real,
    mem_percent  real,
    data         jsonb NOT NULL
);
CREATE INDEX health_snapshots_device_idx ON health_snapshots (device_id, collected_at DESC);

CREATE TABLE battery_snapshots (
    id                   bigserial PRIMARY KEY,
    device_id            uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    collected_at         timestamptz NOT NULL,
    design_capacity      bigint,
    full_charge_capacity bigint,
    cycle_count          int,
    health_percent       real,
    data                 jsonb NOT NULL
);
CREATE INDEX battery_snapshots_device_idx ON battery_snapshots (device_id, collected_at DESC);

CREATE TABLE lifecycle_events (
    id         bigserial PRIMARY KEY,
    device_id  uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    from_state text NOT NULL,
    to_state   text NOT NULL,
    reason     text NOT NULL DEFAULT '',
    actor_name text NOT NULL DEFAULT '',
    at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX lifecycle_events_device_idx ON lifecycle_events (device_id, at DESC);

CREATE TABLE packages (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name           text NOT NULL,
    version        text NOT NULL DEFAULT '',
    publisher      text NOT NULL DEFAULT '',
    file_name      text NOT NULL,
    file_size      bigint NOT NULL,
    sha256         text NOT NULL,
    type           text NOT NULL CHECK (type IN ('msi', 'exe', 'msix', 'ps1')),
    install_args   text NOT NULL DEFAULT '',
    uninstall_args text NOT NULL DEFAULT '',
    success_codes  int[] NOT NULL DEFAULT '{0,3010,1641}',
    storage_path   text NOT NULL,
    created_by     uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE deployments (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL,
    package_id   uuid NOT NULL REFERENCES packages(id) ON DELETE RESTRICT,
    target       jsonb NOT NULL,
    status       text NOT NULL CHECK (status IN ('scheduled', 'running', 'completed', 'canceled')),
    scheduled_at timestamptz,
    created_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE schedules (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    cron        text NOT NULL,
    action      jsonb NOT NULL,
    target      jsonb NOT NULL,
    enabled     boolean NOT NULL DEFAULT true,
    last_run_at timestamptz,
    next_run_at timestamptz,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE commands (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id      uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    kind           text NOT NULL,
    args           jsonb NOT NULL DEFAULT '{}',
    status         text NOT NULL,
    timeout_sec    int NOT NULL,
    issued_by      uuid REFERENCES users(id) ON DELETE SET NULL,
    issued_by_name text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    sent_at        timestamptz,
    started_at     timestamptz,
    finished_at    timestamptz,
    expires_at     timestamptz NOT NULL,
    exit_code      int,
    stdout         text NOT NULL DEFAULT '',
    stderr         text NOT NULL DEFAULT '',
    truncated      boolean NOT NULL DEFAULT false,
    data           jsonb,
    deployment_id  uuid REFERENCES deployments(id) ON DELETE SET NULL,
    schedule_id    uuid REFERENCES schedules(id) ON DELETE SET NULL
);
CREATE INDEX commands_device_idx ON commands (device_id, created_at DESC);
CREATE INDEX commands_pending_idx ON commands (device_id) WHERE status IN ('queued', 'sent');
CREATE INDEX commands_deployment_idx ON commands (deployment_id) WHERE deployment_id IS NOT NULL;
CREATE INDEX commands_created_idx ON commands (created_at DESC);

CREATE TABLE scripts (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    shell       text NOT NULL CHECK (shell IN ('powershell', 'cmd')),
    body        text NOT NULL,
    timeout_sec int NOT NULL DEFAULT 300,
    created_by  uuid REFERENCES users(id) ON DELETE SET NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE chat_messages (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id    uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    direction    text NOT NULL CHECK (direction IN ('mis', 'user')),
    sender       text NOT NULL,
    body         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    delivered_at timestamptz,
    read_at      timestamptz
);
CREATE INDEX chat_messages_device_idx ON chat_messages (device_id, created_at DESC);
CREATE INDEX chat_messages_undelivered_idx ON chat_messages (device_id) WHERE direction = 'mis' AND delivered_at IS NULL;

CREATE TABLE remote_sessions (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id      uuid NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    mode           text NOT NULL CHECK (mode IN ('view', 'control')),
    status         text NOT NULL CHECK (status IN ('pending', 'active', 'denied', 'ended', 'failed')),
    requested_by   uuid REFERENCES users(id) ON DELETE SET NULL,
    requester_name text NOT NULL,
    reason         text NOT NULL DEFAULT '',
    consent_user   text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    ended_at       timestamptz,
    end_reason     text NOT NULL DEFAULT ''
);
CREATE INDEX remote_sessions_device_idx ON remote_sessions (device_id, created_at DESC);

CREATE TABLE discovery_scans (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    cidrs           text[] NOT NULL,
    ports           int[] NOT NULL,
    status          text NOT NULL CHECK (status IN ('running', 'completed', 'failed')),
    requested_by    uuid REFERENCES users(id) ON DELETE SET NULL,
    requested_by_name text NOT NULL DEFAULT '',
    probe_device_id uuid REFERENCES devices(id) ON DELETE SET NULL,
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    found           int NOT NULL DEFAULT 0,
    error           text NOT NULL DEFAULT ''
);

CREATE TABLE discovered_hosts (
    scan_id     uuid NOT NULL REFERENCES discovery_scans(id) ON DELETE CASCADE,
    ip          text NOT NULL,
    mac         text NOT NULL DEFAULT '',
    hostname    text NOT NULL DEFAULT '',
    open_ports  int[] NOT NULL DEFAULT '{}',
    vendor      text NOT NULL DEFAULT '',
    device_type text NOT NULL DEFAULT '',
    rtt_ms      int NOT NULL DEFAULT 0,
    device_id   uuid REFERENCES devices(id) ON DELETE SET NULL,
    PRIMARY KEY (scan_id, ip)
);

-- Append-only audit log.
CREATE TABLE audit_log (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor_id    uuid,
    actor_name  text NOT NULL,
    actor_ip    text NOT NULL DEFAULT '',
    action      text NOT NULL,
    target_type text NOT NULL DEFAULT '',
    target_id   text NOT NULL DEFAULT '',
    target_name text NOT NULL DEFAULT '',
    details     jsonb NOT NULL DEFAULT '{}',
    result      text NOT NULL DEFAULT 'success' CHECK (result IN ('success', 'failure'))
);
CREATE INDEX audit_log_at_idx ON audit_log (at DESC);
CREATE INDEX audit_log_target_idx ON audit_log (target_id);
CREATE INDEX audit_log_action_idx ON audit_log (action);

CREATE FUNCTION audit_log_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    -- Retention purges run with nexmis.audit_purge = 'on'; everything else is rejected.
    IF TG_OP = 'DELETE' AND current_setting('nexmis.audit_purge', true) = 'on' THEN
        RETURN OLD;
    END IF;
    RAISE EXCEPTION 'audit_log is append-only';
END $$;
CREATE TRIGGER audit_log_no_update BEFORE UPDATE OR DELETE ON audit_log
    FOR EACH ROW EXECUTE FUNCTION audit_log_immutable();
