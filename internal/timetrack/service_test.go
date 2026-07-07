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
	kindID := createTestActivityKind(t, service, 1, "Coding", nil)

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() first error = %v", err)
	}

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T11:00:00Z",
		EndAt:          "2026-07-06T13:00:00Z",
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
	kindID := createTestActivityKind(t, service, 1, "Coding", nil)

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() primary error = %v", err)
	}

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackSecondary,
		StartAt:        "2026-07-06T09:30:00Z",
		EndAt:          "2026-07-06T10:15:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() secondary error = %v, want success (different track)", err)
	}
}

func TestServiceEntryEndAtMustBeAfterStartAt(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	kindID := createTestActivityKind(t, service, 1, "Coding", nil)

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T12:00:00Z",
		EndAt:          "2026-07-06T09:00:00Z",
	})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
		t.Fatalf("CreateEntry() error kind = %v, want validation", legacy.ErrorKindOf(err))
	}
}

func TestServiceUpdateEntryTimeExcludesItselfFromOverlapCheck(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	kindID := createTestActivityKind(t, service, 1, "Coding", nil)

	entryID, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	if err := service.UpdateEntryTime(context.Background(), 1, entryID, EntryTimeInput{
		Track:   TrackPrimary,
		StartAt: "2026-07-06T09:30:00Z",
		EndAt:   "2026-07-06T12:30:00Z",
	}); err != nil {
		t.Fatalf("UpdateEntryTime() error = %v, want success (moving itself)", err)
	}
}

func TestServiceUpdateEntryTimeDoesNotRevalidateSelection(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	optionID := createTestCategoryOption(t, service, 1, groupID, "ATS")
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: groupID, Required: true}})

	entryID, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
		Options:        []EntryOption{{GroupID: groupID, OptionID: optionID}},
	})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	// Archive the kind after the entry was created — a pure time update must
	// still succeed, because update-time never revalidates the selection.
	if err := service.UpdateActivityKind(context.Background(), 1, kindID, ActivityKindInput{
		Name:          "Game",
		GroupBindings: []GroupBinding{{GroupID: groupID, Required: true}},
		IsArchived:    true,
	}); err != nil {
		t.Fatalf("UpdateActivityKind() error = %v", err)
	}

	if err := service.UpdateEntryTime(context.Background(), 1, entryID, EntryTimeInput{
		Track:   TrackPrimary,
		StartAt: "2026-07-06T10:00:00Z",
		EndAt:   "2026-07-06T13:00:00Z",
	}); err != nil {
		t.Fatalf("UpdateEntryTime() error = %v, want success even with archived kind", err)
	}

	entry, err := service.repo.GetEntryByID(context.Background(), 1, entryID)
	if err != nil {
		t.Fatalf("GetEntryByID() error = %v", err)
	}
	if len(entry.Options) != 1 || entry.Options[0].OptionID != optionID {
		t.Fatalf("Options = %+v, want unchanged [%d]", entry.Options, optionID)
	}
}

func TestServiceUpdateEntrySelectionDoesNotChangeTime(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	optionAID := createTestCategoryOption(t, service, 1, groupID, "ATS")
	optionBID := createTestCategoryOption(t, service, 1, groupID, "ETS2")
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: groupID, Required: true}})

	entryID, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
		Options:        []EntryOption{{GroupID: groupID, OptionID: optionAID}},
	})
	if err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	if err := service.UpdateEntrySelection(context.Background(), 1, entryID, EntrySelectionInput{
		ActivityKindID: kindID,
		Options:        []EntryOption{{GroupID: groupID, OptionID: optionBID}},
	}); err != nil {
		t.Fatalf("UpdateEntrySelection() error = %v", err)
	}

	entry, err := service.repo.GetEntryByID(context.Background(), 1, entryID)
	if err != nil {
		t.Fatalf("GetEntryByID() error = %v", err)
	}
	if entry.StartAt != "2026-07-06T09:00:00Z" || entry.EndAt != "2026-07-06T12:00:00Z" {
		t.Fatalf("time fields changed: startAt=%s endAt=%s", entry.StartAt, entry.EndAt)
	}
	if len(entry.Options) != 1 || entry.Options[0].OptionID != optionBID {
		t.Fatalf("Options = %+v, want [%d]", entry.Options, optionBID)
	}
}

func TestServiceCreateEntryRequiresRequiredGroup(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: groupID, Required: true}})

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
	})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
		t.Fatalf("CreateEntry() error kind = %v, want validation (missing required group)", legacy.ErrorKindOf(err))
	}
}

func TestServiceCreateEntryRejectsGroupNotBoundToKind(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	boundGroupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	foreignGroupID := createTestCategoryGroup(t, service, 1, "Source", nil)
	foreignOptionID := createTestCategoryOption(t, service, 1, foreignGroupID, "YouTube")
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: boundGroupID, Required: false}})

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
		Options:        []EntryOption{{GroupID: foreignGroupID, OptionID: foreignOptionID}},
	})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
		t.Fatalf("CreateEntry() error kind = %v, want validation (group not applicable)", legacy.ErrorKindOf(err))
	}
}

func TestServiceCreateEntryRejectsOptionFromWrongGroup(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupAID := createTestCategoryGroup(t, service, 1, "Game", nil)
	groupBID := createTestCategoryGroup(t, service, 1, "Source", nil)
	optionInGroupB := createTestCategoryOption(t, service, 1, groupBID, "YouTube")
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{
		{GroupID: groupAID, Required: false},
		{GroupID: groupBID, Required: false},
	})

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
		// claims optionInGroupB under groupAID, which is wrong.
		Options: []EntryOption{{GroupID: groupAID, OptionID: optionInGroupB}},
	})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
		t.Fatalf("CreateEntry() error kind = %v, want validation (option from wrong group)", legacy.ErrorKindOf(err))
	}
}

func TestServiceCreateEntryRejectsTwoOptionsInSameGroup(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	optionAID := createTestCategoryOption(t, service, 1, groupID, "ATS")
	optionBID := createTestCategoryOption(t, service, 1, groupID, "ETS2")
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: groupID, Required: false}})

	_, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
		Options: []EntryOption{
			{GroupID: groupID, OptionID: optionAID},
			{GroupID: groupID, OptionID: optionBID},
		},
	})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
		t.Fatalf("CreateEntry() error kind = %v, want validation (single-select violated)", legacy.ErrorKindOf(err))
	}
}

func TestServiceCreateEntryRejectsArchivedKindGroupAndOption(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))

	t.Run("archived kind", func(t *testing.T) {
		kindID := createTestActivityKind(t, service, 1, "Sleep", nil)
		if err := service.UpdateActivityKind(context.Background(), 1, kindID, ActivityKindInput{Name: "Sleep", IsArchived: true}); err != nil {
			t.Fatalf("UpdateActivityKind() error = %v", err)
		}
		_, err := service.CreateEntry(context.Background(), 1, EntryInput{
			ActivityKindID: kindID, Track: TrackPrimary, StartAt: "2026-07-06T09:00:00Z", EndAt: "2026-07-06T10:00:00Z",
		})
		if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
			t.Fatalf("CreateEntry() error kind = %v, want validation (archived kind)", legacy.ErrorKindOf(err))
		}
	})

	t.Run("archived option", func(t *testing.T) {
		groupID := createTestCategoryGroup(t, service, 1, "Game2", nil)
		optionID := createTestCategoryOption(t, service, 1, groupID, "ATS2")
		kindID := createTestActivityKind(t, service, 1, "Game2", []GroupBinding{{GroupID: groupID, Required: false}})
		if err := service.UpdateCategoryOption(context.Background(), 1, optionID, CategoryOptionInput{GroupID: groupID, Name: "ATS2", IsArchived: true}); err != nil {
			t.Fatalf("UpdateCategoryOption() error = %v", err)
		}
		_, err := service.CreateEntry(context.Background(), 1, EntryInput{
			ActivityKindID: kindID, Track: TrackPrimary, StartAt: "2026-07-06T09:00:00Z", EndAt: "2026-07-06T10:00:00Z",
			Options: []EntryOption{{GroupID: groupID, OptionID: optionID}},
		})
		if legacy.ErrorKindOf(err) != legacy.ErrorKindValidation {
			t.Fatalf("CreateEntry() error kind = %v, want validation (archived option)", legacy.ErrorKindOf(err))
		}
	})
}

func TestServiceOnlyOneNonArchivedAreaGroupPerUser(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	areaKind := CategoryGroupKindArea

	firstID, err := service.CreateCategoryGroup(context.Background(), 1, CategoryGroupInput{Name: "Life area", Kind: &areaKind})
	if err != nil {
		t.Fatalf("CreateCategoryGroup() first error = %v", err)
	}

	_, err = service.CreateCategoryGroup(context.Background(), 1, CategoryGroupInput{Name: "Second area", Kind: &areaKind})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindConflict {
		t.Fatalf("CreateCategoryGroup() second error kind = %v, want conflict", legacy.ErrorKindOf(err))
	}

	// Archiving the first frees the slot for a new one.
	if err := service.UpdateCategoryGroup(context.Background(), 1, firstID, CategoryGroupInput{Name: "Life area", Kind: &areaKind, IsArchived: true}); err != nil {
		t.Fatalf("UpdateCategoryGroup() archive error = %v", err)
	}
	if _, err := service.CreateCategoryGroup(context.Background(), 1, CategoryGroupInput{Name: "New area", Kind: &areaKind}); err != nil {
		t.Fatalf("CreateCategoryGroup() after archiving previous error = %v, want success", err)
	}
}

func TestServiceCategoryGroupArchiveBlockedWhileBoundToActiveKind(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: groupID, Required: false}})

	err := service.UpdateCategoryGroup(context.Background(), 1, groupID, CategoryGroupInput{Name: "Game", IsArchived: true})
	if legacy.ErrorKindOf(err) != legacy.ErrorKindConflict {
		t.Fatalf("UpdateCategoryGroup() archive error kind = %v, want conflict", legacy.ErrorKindOf(err))
	}

	err = service.DeleteCategoryGroup(context.Background(), 1, groupID)
	if legacy.ErrorKindOf(err) != legacy.ErrorKindConflict {
		t.Fatalf("DeleteCategoryGroup() error kind = %v, want conflict", legacy.ErrorKindOf(err))
	}
}

func TestServiceRestrictsActivityKindDeleteAtRepoLevelViaForeignKey(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	kindID := createTestActivityKind(t, service, 1, "Coding", nil)

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID,
		Track:          TrackPrimary,
		StartAt:        "2026-07-06T09:00:00Z",
		EndAt:          "2026-07-06T12:00:00Z",
	}); err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	_, err := db.Exec(`DELETE FROM timeActivityKind WHERE id = ?`, kindID)
	if err == nil {
		t.Fatal("DELETE timeActivityKind error = nil, want FK restrict violation")
	}
}

func TestServiceActivityKindUpdateReplacesGroupBindings(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupAID := createTestCategoryGroup(t, service, 1, "A", nil)
	groupBID := createTestCategoryGroup(t, service, 1, "B", nil)

	kindID := createTestActivityKind(t, service, 1, "Youtube", []GroupBinding{{GroupID: groupAID, Required: true}})

	if err := service.UpdateActivityKind(context.Background(), 1, kindID, ActivityKindInput{
		Name:          "Youtube",
		GroupBindings: []GroupBinding{{GroupID: groupBID, Required: false}},
	}); err != nil {
		t.Fatalf("UpdateActivityKind() error = %v", err)
	}

	kind, err := service.repo.GetActivityKindByID(context.Background(), 1, kindID)
	if err != nil {
		t.Fatalf("GetActivityKindByID() error = %v", err)
	}
	if len(kind.GroupBindings) != 1 || kind.GroupBindings[0].GroupID != groupBID || kind.GroupBindings[0].Required {
		t.Fatalf("GroupBindings = %+v, want [{%d false}]", kind.GroupBindings, groupBID)
	}
}

func TestServiceCategoryOptionDeleteBlockedWhenUsedByEntry(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	groupID := createTestCategoryGroup(t, service, 1, "Game", nil)
	optionID := createTestCategoryOption(t, service, 1, groupID, "ATS")
	kindID := createTestActivityKind(t, service, 1, "Game", []GroupBinding{{GroupID: groupID, Required: true}})

	if _, err := service.CreateEntry(context.Background(), 1, EntryInput{
		ActivityKindID: kindID, Track: TrackPrimary, StartAt: "2026-07-06T09:00:00Z", EndAt: "2026-07-06T10:00:00Z",
		Options: []EntryOption{{GroupID: groupID, OptionID: optionID}},
	}); err != nil {
		t.Fatalf("CreateEntry() error = %v", err)
	}

	err := service.DeleteCategoryOption(context.Background(), 1, optionID)
	if legacy.ErrorKindOf(err) != legacy.ErrorKindConflict {
		t.Fatalf("DeleteCategoryOption() error kind = %v, want conflict", legacy.ErrorKindOf(err))
	}
}

func TestServiceConcurrentOverlappingCreatesExactlyOneSucceeds(t *testing.T) {
	db := openTimetrackTestDB(t)
	insertTimetrackTestUser(t, db, 1, "alice")
	service := NewService(NewRepository(db))
	kindID := createTestActivityKind(t, service, 1, "Coding", nil)

	const attempts = 10
	var wg sync.WaitGroup
	var successCount, conflictCount int64
	var mu sync.Mutex

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.CreateEntry(context.Background(), 1, EntryInput{
				ActivityKindID: kindID,
				Track:          TrackPrimary,
				StartAt:        "2026-07-06T09:00:00Z",
				EndAt:          "2026-07-06T12:00:00Z",
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

func createTestActivityKind(t *testing.T, service *Service, userID int64, name string, bindings []GroupBinding) int64 {
	t.Helper()
	id, err := service.CreateActivityKind(context.Background(), userID, ActivityKindInput{Name: name, GroupBindings: bindings})
	if err != nil {
		t.Fatalf("CreateActivityKind() error = %v", err)
	}
	return id
}

func createTestCategoryGroup(t *testing.T, service *Service, userID int64, name string, kind *string) int64 {
	t.Helper()
	id, err := service.CreateCategoryGroup(context.Background(), userID, CategoryGroupInput{Name: name, Kind: kind})
	if err != nil {
		t.Fatalf("CreateCategoryGroup() error = %v", err)
	}
	return id
}

func createTestCategoryOption(t *testing.T, service *Service, userID int64, groupID int64, name string) int64 {
	t.Helper()
	id, err := service.CreateCategoryOption(context.Background(), userID, CategoryOptionInput{GroupID: groupID, Name: name})
	if err != nil {
		t.Fatalf("CreateCategoryOption() error = %v", err)
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

		CREATE TABLE timeActivityKind (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			userId INTEGER NOT NULL,
			name TEXT NOT NULL,
			groupBindingsJson TEXT NOT NULL DEFAULT '[]',
			isArchived BOOLEAN NOT NULL DEFAULT 0,
			createdAt TEXT NOT NULL,
			updatedAt TEXT NOT NULL,
			FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE timeCategoryGroup (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			userId INTEGER NOT NULL,
			name TEXT NOT NULL,
			kind TEXT,
			isArchived BOOLEAN NOT NULL DEFAULT 0,
			createdAt TEXT NOT NULL,
			updatedAt TEXT NOT NULL,
			FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE
		);

		CREATE TABLE timeCategoryOption (
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

		CREATE TABLE timeEntry (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			userId INTEGER NOT NULL,
			activityKindId INTEGER NOT NULL,
			track TEXT NOT NULL,
			startAt TEXT NOT NULL,
			endAt TEXT NOT NULL,
			createdAt TEXT NOT NULL,
			updatedAt TEXT NOT NULL,
			FOREIGN KEY (userId) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY (activityKindId) REFERENCES timeActivityKind(id) ON DELETE RESTRICT
		);

		CREATE INDEX idx_timeEntry_userId_track_startAt ON timeEntry (userId, track, startAt);

		CREATE TABLE timeEntryOption (
			entryId INTEGER NOT NULL,
			groupId INTEGER NOT NULL,
			optionId INTEGER NOT NULL,
			PRIMARY KEY (entryId, groupId),
			FOREIGN KEY (entryId) REFERENCES timeEntry(id) ON DELETE CASCADE,
			FOREIGN KEY (groupId) REFERENCES timeCategoryGroup(id) ON DELETE RESTRICT,
			FOREIGN KEY (optionId) REFERENCES timeCategoryOption(id) ON DELETE RESTRICT
		);
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
