CREATE TABLE userSettings (
    usersId INTEGER NOT NULL,
    namespace TEXT NOT NULL,
    payload TEXT NOT NULL,
    updatedAt TEXT NOT NULL,
    PRIMARY KEY (usersId, namespace)
);

INSERT INTO userSettings (usersId, namespace, payload, updatedAt)
SELECT usersId,
    'core',
    json_object(
        'selectedChapterFood', json(CASE WHEN selectedChapterFood THEN 'true' ELSE 'false' END),
        'selectedChapterMoney', json(CASE WHEN selectedChapterMoney THEN 'true' ELSE 'false' END)
    ),
    datetime('now')
FROM settings;

INSERT INTO userSettings (usersId, namespace, payload, updatedAt)
SELECT usersId,
    'food',
    json_object('height', height),
    datetime('now')
FROM settings;

INSERT INTO userSettings (usersId, namespace, payload, updatedAt)
SELECT usersId,
    'metrics',
    COALESCE(NULLIF(metricsSettings, ''), '{}'),
    datetime('now')
FROM settings;

DROP TABLE settings;
