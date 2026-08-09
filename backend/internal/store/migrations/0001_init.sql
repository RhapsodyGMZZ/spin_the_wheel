-- =============================================================================
--  Schéma initial — Spin the Wheel
--
--  Toutes les clés primaires sont des UUIDv7 générés côté application : leurs
--  48 bits de poids fort portent l'horodatage de création en millisecondes,
--  ce qui donne un ordre chronologique naturel sur la clé primaire.
-- =============================================================================

-- --- Comptes ----------------------------------------------------------------

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    google_sub    text        NOT NULL UNIQUE,
    email         text        NOT NULL UNIQUE,
    display_name  text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CONSTRAINT users_email_lower CHECK (email = lower(email))
);

-- Liste blanche : seules ces adresses peuvent ouvrir une session.
CREATE TABLE allowed_emails (
    id         uuid PRIMARY KEY,
    email      text        NOT NULL UNIQUE,
    note       text        NOT NULL DEFAULT '',
    added_by   uuid        REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT allowed_emails_lower CHECK (email = lower(email))
);

-- --- OAuth ------------------------------------------------------------------

-- État anti-CSRF + vérificateur PKCE, stockés côté serveur et à usage unique.
CREATE TABLE oauth_states (
    id            uuid PRIMARY KEY,
    state_hash    bytea       NOT NULL UNIQUE,
    code_verifier text        NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    consumed_at   timestamptz
);

CREATE INDEX oauth_states_expires_idx ON oauth_states (expires_at);

-- --- Sessions ---------------------------------------------------------------

-- Le cookie porte un jeton aléatoire de 32 octets ; seul son SHA-256 est
-- stocké. Une fuite de la base ne permet donc pas de rejouer une session.
CREATE TABLE sessions (
    id           uuid PRIMARY KEY,
    user_id      uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea       NOT NULL UNIQUE,
    csrf_token   text        NOT NULL,
    ip           text        NOT NULL DEFAULT '',
    user_agent   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz
);

CREATE INDEX sessions_user_idx    ON sessions (user_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- --- Images -----------------------------------------------------------------

-- Le binaire vit sur le volume disque, toujours ré-encodé en PNG par le
-- serveur. La table ne garde que les métadonnées.
CREATE TABLE images (
    id         uuid PRIMARY KEY,
    owner_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    sha256     bytea       NOT NULL,
    byte_size  integer     NOT NULL CHECK (byte_size > 0),
    width      integer     NOT NULL CHECK (width  > 0),
    height     integer     NOT NULL CHECK (height > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX images_owner_idx ON images (owner_id);

-- --- Roues ------------------------------------------------------------------

CREATE TABLE wheels (
    id         uuid PRIMARY KEY,
    owner_id   uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title      text        NOT NULL,
    is_active  boolean     NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz
);

CREATE INDEX wheels_owner_idx ON wheels (owner_id) WHERE deleted_at IS NULL;

CREATE TABLE segments (
    id         uuid PRIMARY KEY,
    wheel_id   uuid        NOT NULL REFERENCES wheels (id) ON DELETE CASCADE,
    position   integer     NOT NULL CHECK (position >= 0),
    label      text        NOT NULL,
    color      text        NOT NULL,
    image_id   uuid        REFERENCES images (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    -- Couleur contrainte jusque dans la base : #rrggbb, rien d'autre.
    CONSTRAINT segments_color_hex CHECK (color ~ '^#[0-9a-f]{6}$'),
    CONSTRAINT segments_wheel_position UNIQUE (wheel_id, position)
);

CREATE INDEX segments_wheel_idx ON segments (wheel_id);

-- --- Tirages ----------------------------------------------------------------

-- segment_id n'a volontairement PAS de clé étrangère : l'historique des
-- tirages doit survivre à la réédition ou à la suppression d'un segment.
-- Le libellé est figé au moment du tirage.
CREATE TABLE spins (
    id            uuid PRIMARY KEY,
    wheel_id      uuid        NOT NULL REFERENCES wheels (id) ON DELETE CASCADE,
    segment_id    uuid,
    segment_index integer     NOT NULL,
    segment_label text        NOT NULL,
    ip_hash       bytea,
    user_agent    text        NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX spins_wheel_idx ON spins (wheel_id, created_at DESC);

-- --- Journal d'audit --------------------------------------------------------

-- Trace applicative de toute action ayant un effet : connexion, déconnexion,
-- création/modification/suppression, upload, tirage, refus d'accès.
CREATE TABLE audit_log (
    id          uuid PRIMARY KEY,
    actor_id    uuid        REFERENCES users (id) ON DELETE SET NULL,
    action      text        NOT NULL,
    entity_type text        NOT NULL DEFAULT '',
    entity_id   uuid,
    ip          text        NOT NULL DEFAULT '',
    user_agent  text        NOT NULL DEFAULT '',
    request_id  text        NOT NULL DEFAULT '',
    details     jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_actor_idx   ON audit_log (actor_id, created_at DESC);
CREATE INDEX audit_log_action_idx  ON audit_log (action, created_at DESC);
