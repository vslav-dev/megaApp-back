package timetrack

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"megaapp-back/internal/httpx/legacy"

	_ "modernc.org/sqlite"
)

func TestServiceEntryOverlapRejectedWithinSameTrack(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	activityID := createTestActivity(t, service, 1, "Coding")

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T09:00:00Z",
		EndAt:      "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() first error = %v", err)
	}

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T11:00:00Z",
		EndAt:      "2026-07-06T13:00:00Z",
	})
	if err == nil {
		t.Fatal("CreateEntry() overlapping error = nil, want conflict")
	}
	if legacy.ErrorKindOf(err) != legacy.ErrorKindConflict {
		t.Fatalf("CreateEntry() error kind = %v, want conflict", legacy.ErrorKindOf(err))
	}
}

func TestServiceEntryOverlapAllowedAcrossDifferentTracks(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	activityID := createTestActivity(t, service, 1, "Coding")

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T09:00:00Z",
		EndAt:      "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() primary error = %v", err)
	}

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackSecondary,
		StartAt:    "2026-07-06T09:30:00Z",
		EndAt:      "2026-07-06T10:15:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() secondary error = %v, want success (different track)", err)
	}
}

func TestServiceEntryEndAtMustBeAfterStartAt(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	activityID := createTestActivity(t, service, 1, "Coding")

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T12:00:00Z",
		EndAt:      "2026-07-06T09:00:00Z",
	})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
		t.Fatalf("CreateEntry() error kind = %v, want validation", legacy.ErrorKindOf(err))
	}
}

func TestServiceUpdateEntryExcludesItselfFromOverlapCheck(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	activityID := createTestActivity(t, service, 1, "Coding")

	entryID, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T09:00:00Z",
		EndAt:      "2026-07-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	if err := service.UpdateEntry(context.Background(), 1, entryID, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T09:30:00Z",
		EndAt:      "2026-07-06T12:30:00Z",
	}); err != nil {
		t.Fatalf("UpdateEntry() error = %v, want success (moving itself)", err)
	}
}

func TestServiceRestrictsActivityDeleteAtRepoLevelViaForeignKey(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	activityID := createTestActivity(t, service, 1, "Coding")

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: activityID,
		Track:      TrackPrimary,
		StartAt:    "2026-07-06T09:00:00Z",
		EndAt:      "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	_, err := db.Exec(`DELETE FROM timeActivity WHERE id = ?`, activityID)
	if err == nil {
		t.Fatal("DELETE timeActivity error = nil, want FK restrict violation")
	}
}

func TestServiceActivityUpdateReplacesCategoryIds(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))

	categoryAID, err := service.CreateCategory(context.Background(), 1, CategoryInput{Name: "Work"})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}
	categoryBID, err := service.CreateCategory(context.Background(), 1, CategoryInput{Name: "Fun"})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}

	activityID, err := service.CreateActivity(context.Background(), 1, ActivityInput{
		Name:        "Youtube",
		CategoryIDs: []int64{categoryAID},
	})
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}

	if err := service.UpdateActivity(context.Background(), 1, activityID, ActivityInput{
		Name:        "Youtube",
		CategoryIDs: []int64{categoryBID},
	}); err != nil {
		t.Fatalf("UpdateActivity() error = %v", err)
	}

	activity, err := service.repo.GetActivityByID(context.Background(), 1, activityID)
	if err != nil {
		t.Fatalf("GetActivityByID() error = %v", err)
	}
	if len(activity.CategoryIDs) != 1 || activity.CategoryIDs[0] != categoryBID {
		t.Fatalf("CategoryIDs = %v, want [%d]", activity.CategoryIDs, categoryBID)
	}
}

func TestServiceGetActivitiesCategoryIDsEmptyNotNilAfterCategoryDeleted(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))

	categoryID, err := service.CreateCategory(context.Background(), 1, CategoryInput{Name: "Fun"})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}

	activityID, err := service.CreateActivity(context.Background(), 1, ActivityInput{
		Name:        "Youtube",
		CategoryIDs: []int64{categoryID},
	})
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}

	if err := service.DeleteCategory(context.Background(), 1, categoryID); err != nil {
		t.Fatalf("DeleteCategory() error = %v", err)
	}

	activities, err := service.GetActivities(context.Background(), 1, nil)
	if err != nil {
		t.Fatalf("GetActivities() error = %v", err)
	}

	var activity *Activity
	for i := range activities {
		if activities[i].ID == activityID {
			activity = &activities[i]
		}
	}
	if activity == nil {
		t.Fatalf("activity %d not found in %+v", activityID, activities)
	}
	if activity.CategoryIDs == nil {
		t.Fatal("CategoryIDs = nil, want empty non-nil slice (marshals to `null` instead of `[]` otherwise)")
	}
	if len(activity.CategoryIDs) != 0 {
		t.Fatalf("CategoryIDs = %v, want empty", activity.CategoryIDs)
	}
}

func TestServiceRecentActivityIDsOrderedByLastUsed(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))

	olderID := createTestActivity(t, service, 1, "Sleep")
	newerID := createTestActivity(t, service, 1, "Coding")

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: olderID, Track: TrackPrimary, StartAt: "2026-07-01T00:00:00Z", EndAt: "2026-07-01T08:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}
	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityID: newerID, Track: TrackPrimary, StartAt: "2026-07-06T09:00:00Z", EndAt: "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	recent, err := service.GetRecentActivityIDs(context.Background(), 1, nil, 0)
	if err != nil {
		t.Fatalf("GetRecentActivityIDs() error = %v", err)
	}
	if len(recent) != 2 || recent[0] != newerID || recent[1] != olderID {
		t.Fatalf("GetRecentActivityIDs() = %v, want [%d, %d]", recent, newerID, olderID)
	}
}

func TestServiceConcurrentOverlappingCreatesExactlyOneSucceeds(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	activityID := createTestActivity(t, service, 1, "Coding")

	const attempts = 10
	var wg sync.WaitGroup
	var successCount, conflictCount int64
	var mu sync.Mutex

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.CreateEntry(context.Background(), 1, EntryInput{
				ActivityID: activityID,
				Track:      TrackPrimary,
				StartAt:    "2026-07-06T09:00:00Z",
				EndAt:      "2026-07-06T12:00:00Z",
			})
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				successCount++
			} else if legacy.ErrorKindOf(err) == legacy.ErrorKindConflict {
				conflictCount++
			} else {
				t.Errorf("CreateEntry() unexpected error = %v", err)
			}
		}()
	}
	wg.Wait()

	if successCount != 1 {
		t.Fatalf("successCount = %d, want 1", successCount)
	}
	if conflictCount != attempts-1 {
		t.Fatalf("conflictCount = %d, want %d", conflictCount, attempts-1)
	}
}

func createTestActivity(t *testing.T, service *Service, userID int64, name string) int64 {
	t.Helper()
	id, err := service.CreateActivity(context.Background(), userID, ActivityInput{Name: name})
	if err != nil {
		t.Fatalf("CreateActivity() error = %v", err)
	}
	return id
}

func openTimetrackTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	schema := `
		PRAGMA foreign_keys = ON;

		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			hashedPassword TEXT,
			isAdmin BOOLEAN
		);

		CREATE TABLE timeActivity (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			userId INTEGER NOT NULL,
			name TEXT NOT NULL,
			isArchived BOOLEAN NOT NULL DEFAULT 0,
			createdAt TEXT NOT NULL,
			FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE timeCategory (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			userId INTEGER NOT NULL,
			name TEXT NOT NULL,
			kind TEXT,
			color TEXT,
			impact TEXT,
			createdAt TEXT NOT NULL,
			FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE timeActivityCategory (
			activityId INTEGER NOT NULL,
			categoryId INTEGER NOT NULL,
			PRIMARY KEY (activityId, categoryId),
			FOREIGN KEY (activityId) REFERENCES timeActivity(id) ON DELETE CASCADE,
			FOREIGN KEY (categoryId) REFERENCES timeCategory(id) ON DELETE CASCADE
		);

		CREATE TABLE timeEntry (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			userId INTEGER NOT NULL,
			activityId INTEGER NOT NULL,
			track TEXT NOT NULL,
			startAt TEXT NOT NULL,
			endAt TEXT NOT NULL,
			createdAt TEXT NOT NULL,
			updatedAt TEXT NOT NULL,
			FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (activityId) REFERENCES timeActivity(id) ON DELETE RESTRICT
		);

		CREATE INDEX idx_timeEntry_userId_track_startAt ON timeEntry (userId, track, startAt);
	`

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		t.Fatalf("Exec() error = %v", err)
	}

	// :memory: without shared cache gives each new pool connection its own
	// blank database — pin the pool to one connection so concurrent tests
	// (see TestServiceConcurrentOverlappingCreatesExactlyOneSucceeds) all
	// see the same data, same as the single-connection setup in production.
	db.SetMaxOpenConns(1)

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func insertTimetrackTestUser(t *testing.T, db *sql.DB, userID int64, username string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (id, username, hashedPassword, isAdmin) VALUES (?, ?, 'hash', 0)`, userID, username); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
}
