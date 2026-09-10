DO $$ BEGIN
  CREATE TYPE user_role AS ENUM ('agent', 'supervisor', 'administrator');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;
DO $$ BEGIN
  CREATE TYPE user_status AS ENUM ('active', 'disabled');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE users (
  id uuid PRIMARY KEY,
  login text NOT NULL,
  email text NOT NULL,
  name text NOT NULL,
  password_hash text NOT NULL,
  role user_role NOT NULL,
  status user_status NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT users_login_not_blank CHECK (length(btrim(login)) > 0),
  CONSTRAINT users_email_not_blank CHECK (length(btrim(email)) > 0),
  CONSTRAINT users_name_not_blank CHECK (length(btrim(name)) > 0)
);
CREATE UNIQUE INDEX users_login_lower_uq ON users (lower(login));
CREATE UNIQUE INDEX users_email_lower_uq ON users (lower(email));

CREATE TABLE permission_bundles (
  id uuid PRIMARY KEY,
  name text NOT NULL,
  permissions text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT permission_bundles_name_not_blank CHECK (length(btrim(name)) > 0)
);
CREATE UNIQUE INDEX permission_bundles_name_lower_uq ON permission_bundles (lower(name));

CREATE TABLE user_permission_bundles (
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  bundle_id uuid NOT NULL REFERENCES permission_bundles(id) ON DELETE RESTRICT,
  PRIMARY KEY (user_id, bundle_id)
);

CREATE TABLE sessions (
  token_hash text PRIMARY KEY,
  csrf_hash text NOT NULL,
  user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at timestamptz NOT NULL,
  revoked_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX sessions_user_id_active_idx ON sessions (user_id, expires_at) WHERE revoked_at IS NULL;
