CREATE TABLE IF NOT EXISTS timeActivity (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    name TEXT NOT NULL,
    isArchived BOOLEAN NOT NULL DEFAULT 0,
    createdAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timeCategory (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    name TEXT NOT NULL,
    kind TEXT,
    color TEXT,
    impact TEXT CHECK(impact IS NULL OR impact IN ('useful', 'neutral', 'wasteful')),
    createdAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timeActivityCategory (
    activityId INTEGER NOT NULL,
    categoryId INTEGER NOT NULL,
    PRIMARY KEY (activityId, categoryId),
    FOREIGN KEY (activityId) REFERENCES timeActivity(id) ON DELETE CASCADE,
    FOREIGN KEY (categoryId) REFERENCES timeCategory(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timeEntry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    activityId INTEGER NOT NULL,
    track TEXT NOT NULL CHECK(track IN ('primary', 'secondary')),
    startAt TEXT NOT NULL,
    endAt TEXT NOT NULL,
    createdAt TEXT NOT NULL,
    updatedAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (activityId) REFERENCES timeActivity(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_timeEntry_userId_track_startAt ON timeEntry (userId, track, startAt);

ALTER TABLE settings ADD COLUMN selectedChapterTime BOOLEAN;
