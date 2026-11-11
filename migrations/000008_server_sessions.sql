CREATE TABLE IF NOT EXISTS auth_sessions (
    id TEXT PRIMARY KEY,
    secretHash BLOB NOT NULL,
    userId INTEGER NOT NULL,
    createdAt INTEGER NOT NULL,
    expiresAt INTEGER NOT NULL,
    renewedAt INTEGER NOT NULL,
    revokedAt INTEGER DEFAULT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_auth_sessions_userId ON auth_sessions(userId);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_expiresAt ON auth_sessions(expiresAt);
