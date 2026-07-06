package settings

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

func TestGetCreatesDefaultsAndReturnsUserMetadata(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))

	userID := insertSettingsTestUser(t, db, "alice", true)

	result, err := service.Get(context.Background(), userID, "alice")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if result.UserName != "alice" {
		t.Fatalf("UserName = %q, want alice", result.UserName)
	}
	if !result.IsUserAdmin {
		t.Fatal("IsUserAdmin = false, want true")
	}
	if result.DarkTheme || result.SelectedChapterFood || result.SelectedChapterMoney || result.SelectedChapterTime || result.LiteVersion {
		t.Fatal("default boolean settings should be false")
	}
	if result.Height != nil {
		t.Fatalf("Height = %v, want nil", result.Height)
	}

	stored, err := NewRepository(db).GetByUserID(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetByUserID() error = %v", err)
	}
	if stored == nil {
		t.Fatal("stored settings = nil, want row")
	}
}

func TestGetHandlesPreExistingRowWithNullSelectedChapterTime(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))
	userID := insertSettingsTestUser(t, db, "alice", false)

	if _, err := db.Exec(`
		INSERT INTO settings (usersId, darkTheme, selectedChapterFood, selectedChapterMoney, liteVersion, height)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, true, false, false, false, nil); err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	result, err := service.Get(context.Background(), userID, "alice")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if result.SelectedChapterTime {
		t.Fatal("SelectedChapterTime = true, want false for NULL column")
	}
}

func TestPutUpdatesSingleSetting(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))
	userID := insertSettingsTestUser(t, db, "alice", false)

	input, err := ParseUpdateInput(map[string]any{"darkTheme": true})
	if err != nil {
		t.Fatalf("ParseUpdateInput() error = %v", err)
	}

	if err := service.Put(context.Background(), userID, input); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	result, err := service.Get(context.Background(), userID, "alice")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !result.DarkTheme {
		t.Fatal("DarkTheme = false, want true")
	}
}

func TestPutUpdatesSelectedChapterTime(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))
	userID := insertSettingsTestUser(t, db, "alice", false)

	input, err := ParseUpdateInput(map[string]any{"selectedChapterTime": true})
	if err != nil {
		t.Fatalf("ParseUpdateInput() error = %v", err)
	}

	if err := service.Put(context.Background(), userID, input); err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	result, err := service.Get(context.Background(), userID, "alice")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !result.SelectedChapterTime {
		t.Fatal("SelectedChapterTime = false, want true")
	}
}

func TestPutRejectsInvalidField(t *testing.T) {
	db := openSettingsTestDB(t)
	_ = insertSettingsTestUser(t, db, "alice", false)

	_, err := ParseUpdateInput(map[string]any{"userName": "bob"})
	if err != ErrInvalidSetting {
		t.Fatalf("ParseUpdateInput() error = %v, want ErrInvalidSetting", err)
	}
}

func TestPostUpsertsSettings(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))
	userID := insertSettingsTestUser(t, db, "alice", false)
	height := int64(185)

	if err := service.Post(context.Background(), userID, UserSettings{
		DarkTheme:            true,
		SelectedChapterFood:  true,
		SelectedChapterMoney: false,
		SelectedChapterTime:  true,
		LiteVersion:          true,
		Height:               &height,
	}); err != nil {
		t.Fatalf("Post() error = %v", err)
	}

	result, err := service.Get(context.Background(), userID, "alice")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !result.DarkTheme || !result.SelectedChapterFood || !result.SelectedChapterTime || !result.LiteVersion {
		t.Fatal("stored settings mismatch")
	}
	if result.Height == nil || *result.Height != 185 {
		t.Fatalf("Height = %v, want 185", result.Height)
	}
}

func TestGetMetricsSettingsReturnsEmptyObjectByDefault(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))
	userID := insertSettingsTestUser(t, db, "alice", false)

	result, err := service.GetMetricsSettings(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetMetricsSettings() error = %v", err)
	}
	if string(result) != "{}" {
		t.Fatalf("GetMetricsSettings() = %q, want {}", result)
	}
}

func TestPutMetricsSettingsUpsertsRawValue(t *testing.T) {
	db := openSettingsTestDB(t)
	service := NewService(NewRepository(db))
	userID := insertSettingsTestUser(t, db, "alice", false)

	value := []byte(`{"granularity":"hour"}`)
	if err := service.PutMetricsSettings(context.Background(), userID, value); err != nil {
		t.Fatalf("PutMetricsSettings() error = %v", err)
	}

	result, err := service.GetMetricsSettings(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetMetricsSettings() error = %v", err)
	}
	if string(result) != `{"granularity":"hour"}` {
		t.Fatalf("GetMetricsSettings() = %s, want %s", result, value)
	}

	updated := []byte(`{"granularity":"day"}`)
	if err := service.PutMetricsSettings(context.Background(), userID, updated); err != nil {
		t.Fatalf("PutMetricsSettings() second call error = %v", err)
	}

	result, err = service.GetMetricsSettings(context.Background(), userID)
	if err != nil {
		t.Fatalf("GetMetricsSettings() error = %v", err)
	}
	if string(result) != `{"granularity":"day"}` {
		t.Fatalf("GetMetricsSettings() after update = %s, want %s", result, updated)
	}
}

func openSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}

	if _, err := db.Exec(`
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT,
			hashedPassword TEXT,
			isAdmin BOOLEAN
		);

		CREATE TABLE settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			usersId INTEGER,
			darkTheme BOOLEAN,
			selectedChapterFood BOOLEAN,
			selectedChapterMoney BOOLEAN,
			selectedChapterTime BOOLEAN,
			liteVersion BOOLEAN,
			height INTEGER,
			sex TEXT DEFAULT NULL,
			birthDate TEXT DEFAULT NULL,
			activityLevel TEXT DEFAULT NULL,
			goal TEXT DEFAULT NULL,
			metricsSettings TEXT
		);
	`); err != nil {
		_ = db.Close()
		t.Fatalf("Exec() error = %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	return db
}

func insertSettingsTestUser(t *testing.T, db *sql.DB, username string, isAdmin bool) int64 {
	t.Helper()

	result, err := db.Exec(`INSERT INTO users (username, hashedPassword, isAdmin) VALUES (?, ?, ?)`, username, "hash", isAdmin)
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("LastInsertId() error = %v", err)
	}

	return id
}
