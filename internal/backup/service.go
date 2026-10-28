package backup

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"megaapp-back/internal/httpx/legacy"
	platformclock "megaapp-back/internal/platform/clock"
	logplatform "megaapp-back/internal/platform/log"
	"megaapp-back/internal/platform/sqlite"
)

const MetricJobRan = "backup_job_ran"

type Config struct {
	DatabaseName   string
	DatabaseEnv    string
	BackupsDir     string
	StorageEnabled bool
	StorageClass   string
}

type RunResult struct {
	UploadedKey      string `json:"uploadedKey"`
	SnapshotFileName string `json:"snapshotFileName"`
	ArchiveFileName  string `json:"archiveFileName"`
	CleanedUp        bool   `json:"cleanedUp"`
}

type ArchiveUploader interface {
	UploadFile(ctx context.Context, key string, filePath string, contentType string, storageClass string) error
}

type Service struct {
	db       sqlite.WriteDB
	cfg      Config
	clock    platformclock.Clock
	logger   *slog.Logger
	uploader ArchiveUploader
}

func NewService(db sqlite.WriteDB, cfg Config, clk platformclock.Clock, logger *slog.Logger, uploader ArchiveUploader) *Service {
	if clk == nil {
		clk = platformclock.NewRealClock()
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{db: db, cfg: cfg, clock: clk, logger: logger, uploader: uploader}
}

func (s *Service) Run(ctx context.Context) (result RunResult, err error) {
	runStartedAt := time.Now()
	if !s.cfg.StorageEnabled {
		return RunResult{}, legacy.NewError(legacy.ErrorKindValidation, "Backup storage is disabled")
	}
	if s.uploader == nil {
		return RunResult{}, legacy.NewError(legacy.ErrorKindValidation, "Backup uploader is not configured")
	}
	if err := os.MkdirAll(s.cfg.BackupsDir, 0o755); err != nil {
		return RunResult{}, legacy.WrapError(legacy.ErrorKindInternal, "Failed to create backups directory", err)
	}

	timestamp := s.clock.Now().UTC().Format("2006-01-02T15-04-05Z")
	archiveBaseName := fmt.Sprintf("%s-%s-%s", s.cfg.DatabaseName, s.cfg.DatabaseEnv, timestamp)
	snapshotPath := filepath.Join(s.cfg.BackupsDir, archiveBaseName+".snapshot.db")
	archivePath := filepath.Join(s.cfg.BackupsDir, archiveBaseName+".zip")
	cleanupPaths := []string{snapshotPath, archivePath}
	defer func() {
		if cleanupErr := cleanupLocalFiles(cleanupPaths); cleanupErr != nil {
			if err == nil {
				err = cleanupErr
			}
			return
		}
		result.CleanedUp = true
	}()

	if err := cleanupLocalFiles(cleanupPaths); err != nil {
		return RunResult{}, err
	}

	dbStartedAt := time.Now()
	if err := s.createSnapshot(ctx, snapshotPath); err != nil {
		return RunResult{}, err
	}
	dbDuration := time.Since(dbStartedAt)

	archiveStartedAt := time.Now()
	if err := createZipArchive(snapshotPath, archivePath, s.clock.Now().UTC()); err != nil {
		return RunResult{}, err
	}
	archiveDuration := time.Since(archiveStartedAt)

	uploadedKey := filepath.Base(archivePath)
	uploadStartedAt := time.Now()
	if err := s.uploader.UploadFile(ctx, uploadedKey, archivePath, "application/zip", s.cfg.StorageClass); err != nil {
		return RunResult{}, legacy.WrapError(legacy.ErrorKindExternal, "Failed to upload backup archive", err)
	}
	uploadDuration := time.Since(uploadStartedAt)

	s.logger.Info("backup_completed",
		"db_phase", logplatform.FormatDuration(dbDuration),
		"archive_phase", logplatform.FormatDuration(archiveDuration),
		"upload_phase", logplatform.FormatDuration(uploadDuration),
		"total", logplatform.FormatDuration(time.Since(runStartedAt)),
	)

	return RunResult{
		UploadedKey:      uploadedKey,
		SnapshotFileName: filepath.Base(snapshotPath),
		ArchiveFileName:  filepath.Base(archivePath),
	}, nil
}

func (s *Service) createSnapshot(ctx context.Context, snapshotPath string) error {
	escapedPath := strings.ReplaceAll(snapshotPath, "'", "''")
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("VACUUM INTO '%s'", escapedPath)); err != nil {
		return legacy.WrapError(legacy.ErrorKindInternal, "Failed to create database snapshot", err)
	}
	return nil
}

func createZipArchive(snapshotPath string, archivePath string, modifiedAt time.Time) error {
	snapshotFile, err := os.Open(snapshotPath)
	if err != nil {
		return legacy.WrapError(legacy.ErrorKindInternal, "Failed to open snapshot file", err)
	}
	defer snapshotFile.Close()

	archiveFile, err := os.Create(archivePath)
	if err != nil {
		return legacy.WrapError(legacy.ErrorKindInternal, "Failed to create archive file", err)
	}
	defer archiveFile.Close()

	zipWriter := zip.NewWriter(archiveFile)
	entryWriter, err := zipWriter.CreateHeader(&zip.FileHeader{
		Name:     filepath.Base(snapshotPath),
		Method:   zip.Deflate,
		Modified: modifiedAt,
	})
	if err != nil {
		_ = zipWriter.Close()
		return legacy.WrapError(legacy.ErrorKindInternal, "Failed to create archive entry", err)
	}
	if _, err := io.Copy(entryWriter, snapshotFile); err != nil {
		_ = zipWriter.Close()
		return legacy.WrapError(legacy.ErrorKindInternal, "Failed to write archive entry", err)
	}
	if err := zipWriter.Close(); err != nil {
		return legacy.WrapError(legacy.ErrorKindInternal, "Failed to finalize archive", err)
	}
	return nil
}

func cleanupLocalFiles(paths []string) error {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return legacy.WrapError(legacy.ErrorKindInternal, "Failed to clean up local backup files", err)
		}
	}
	return nil
}
