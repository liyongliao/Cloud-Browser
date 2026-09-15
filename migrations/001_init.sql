CREATE TABLE IF NOT EXISTS users (
 id text PRIMARY KEY, email text UNIQUE NOT NULL, password_hash text NOT NULL,
 admin boolean NOT NULL DEFAULT false, disabled boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS auth_sessions (
 token_hash text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), csrf text NOT NULL,
 expires_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS invites (
 token_hash text PRIMARY KEY, created_by text NOT NULL REFERENCES users(id),
 expires_at timestamptz NOT NULL, used_at timestamptz
);
CREATE TABLE IF NOT EXISTS profiles (
 id text PRIMARY KEY, user_id text UNIQUE NOT NULL REFERENCES users(id), created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS sessions (
 id text PRIMARY KEY, profile_id text UNIQUE NOT NULL REFERENCES profiles(id),
 state text NOT NULL DEFAULT 'STOPPED' CHECK(state IN ('STOPPED','STARTING','RUNNING','STOPPING','FAILED')),
 last_connected_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 error text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS operations (
 id text PRIMARY KEY, user_id text NOT NULL REFERENCES users(id), session_id text NOT NULL REFERENCES sessions(id),
 idem_key text NOT NULL, kind text NOT NULL CHECK(kind IN ('start','open','stop')),
 url text NOT NULL DEFAULT '', request_hash text NOT NULL DEFAULT '', state text NOT NULL DEFAULT 'QUEUED' CHECK(state IN ('QUEUED','RUNNING','SUCCEEDED','FAILED','UNKNOWN')),
 error text NOT NULL DEFAULT '', created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(user_id,idem_key)
);
CREATE TABLE IF NOT EXISTS controller_leases (
 session_id text PRIMARY KEY REFERENCES sessions(id), auth_hash text NOT NULL,
 token_hash text NOT NULL, generation bigint NOT NULL DEFAULT 1, updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS audit_events (
 id bigserial PRIMARY KEY, user_id text REFERENCES users(id), event text NOT NULL,
 created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS operations_pending ON operations(created_at) WHERE state IN ('QUEUED','RUNNING');
