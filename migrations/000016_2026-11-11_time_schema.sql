CREATE TABLE IF NOT EXISTS timeActivityKind (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    name TEXT NOT NULL,
    groupBindingsJson TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(groupBindingsJson) = 1 AND json_type(groupBindingsJson) = 'array'),
    isArchived BOOLEAN NOT NULL DEFAULT 0,
    createdAt TEXT NOT NULL,
    updatedAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timeCategoryGroup (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    name TEXT NOT NULL,
    kind TEXT CHECK(kind IS NULL OR kind = 'area'),
    isArchived BOOLEAN NOT NULL DEFAULT 0,
    createdAt TEXT NOT NULL,
    updatedAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS timeCategoryOption (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    groupId INTEGER NOT NULL,
    name TEXT NOT NULL,
    color TEXT,
    isArchived BOOLEAN NOT NULL DEFAULT 0,
    createdAt TEXT NOT NULL,
    updatedAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (groupId) REFERENCES timeCategoryGroup(id) ON DELETE RESTRICT
);

CREATE TABLE IF NOT EXISTS timeEntry (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    userId INTEGER NOT NULL,
    activityKindId INTEGER NOT NULL,
    track TEXT NOT NULL CHECK(track IN ('primary', 'secondary')),
    startAt TEXT NOT NULL,
    endAt TEXT NOT NULL,
    createdAt TEXT NOT NULL,
    updatedAt TEXT NOT NULL,
    FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (activityKindId) REFERENCES timeActivityKind(id) ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS idx_timeEntry_userId_track_startAt ON timeEntry (userId, track, startAt);

CREATE TABLE IF NOT EXISTS timeEntryOption (
    entryId INTEGER NOT NULL,
    groupId INTEGER NOT NULL,
    optionId INTEGER NOT NULL,
    PRIMARY KEY (entryId, groupId),
    FOREIGN KEY (entryId) REFERENCES timeEntry(id) ON DELETE CASCADE,
    FOREIGN KEY (groupId) REFERENCES timeCategoryGroup(id) ON DELETE RESTRICT,
    FOREIGN KEY (optionId) REFERENCES timeCategoryOption(id) ON DELETE RESTRICT
);
