CREATE TABLE IF NOT EXISTS syncOperations (
    id TEXT PRIMARY KEY,
    userId INTEGER NOT NULL,
    createdAt TEXT NOT NULL,
    resultJSON TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_syncOperations_userId ON syncOperations(userId);

ALTER TABLE moneyTransaction ADD COLUMN ver INTEGER NOT NULL DEFAULT 1;
