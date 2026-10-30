CREATE TABLE IF NOT EXISTS foodSearchPicks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    usersId INTEGER NOT NULL,
    query TEXT NOT NULL,
    foodCatalogueId INTEGER NOT NULL,
    pickedAt INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idxFoodSearchPicksUserQuery ON foodSearchPicks (usersId, query, foodCatalogueId, pickedAt);
