CREATE INDEX IF NOT EXISTS idxFoodDiaryUserDate ON foodDiary (usersId, dateISO);
CREATE INDEX IF NOT EXISTS idxFoodBodyWeightUserDate ON foodBodyWeight (usersId, dateISO);
