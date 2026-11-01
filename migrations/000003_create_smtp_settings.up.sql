CREATE TABLE smtp_settings (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    host VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL,
    username VARCHAR(320) NOT NULL,
    password_ciphertext TEXT NOT NULL,
    sender VARCHAR(320) NOT NULL,
    updated_at TIMESTAMP(0) WITH TIME ZONE NOT NULL DEFAULT NOW()
);
