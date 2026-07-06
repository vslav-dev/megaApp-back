package httpx

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"megaapp-back/internal/auth"
	"megaapp-back/internal/backup"
	"megaapp-back/internal/config"
	"megaapp-back/internal/food"
	"megaapp-back/internal/jobs"
	"megaapp-back/internal/metrics"
	"megaapp-back/internal/money"
	clockplatform "megaapp-back/internal/platform/clock"
	s3platform "megaapp-back/internal/platform/s3"
	"megaapp-back/internal/quotes"
	"megaapp-back/internal/settings"
	"megaapp-back/internal/timetrack"
	"megaapp-back/internal/ws"
)

type authModule struct {
	service *auth.Service
	handler *auth.Handler
}

type settingsModule struct {
	service *settings.Service
	handler *settings.Handler
}

type wsModule struct {
	hub     *ws.Hub
	handler *ws.Handler
}

type moneyModule struct {
	service *money.Service
	handler *money.Handler
}

type timetrackModule struct {
	service *timetrack.Service
	handler *timetrack.Handler
}

type quotesModule struct {
	service      *quotes.Service
	debugHandler *quotes.DebugHandler
}

type backupModule struct {
	service      *backup.Service
	debugHandler *backup.DebugHandler
}

type metricsModule struct {
	service  *metrics.Service
	realtime *metrics.Realtime
	poller   *metrics.Poller
}

type foodModule struct {
	service          *food.Service
	readHandler      *food.Handler
	writeHandler     *food.WriteHandler
	catalogueHandler *food.CatalogueHandler
	imageHandler     *food.ImageHandler
	labHandler       *food.LabHandler
	debugHandler     *food.DebugHandler
	backgrounds      []interface{ Close() error }
}

func buildAuthModule(db *sql.DB, cfg config.Config) authModule {
	repo := auth.NewRepository(db)
	tokenManager := auth.NewTokenManager(cfg.JWTSecret, auth.AccessTokenTTL(), auth.RefreshTokenTTL())
	service := auth.NewService(repo, tokenManager)
	return authModule{service: service, handler: auth.NewHandler(service)}
}

func buildSettingsModule(db *sql.DB) settingsModule {
	repo := settings.NewRepository(db)
	service := settings.NewService(repo)
	return settingsModule{service: service, handler: settings.NewHandler(service)}
}

func buildWSModule(cfg config.Config, authService *auth.Service) wsModule {
	hub := ws.NewHub(30*time.Second, ws.NewSyncState())
	hub.SetReadLimitBytes(cfg.WSReadLimitBytes)
	hub.SetWriteTimeout(cfg.WSWriteTimeout)
	return wsModule{hub: hub, handler: ws.NewHandler(authService, hub)}
}

func buildMoneyModule(db *sql.DB) moneyModule {
	repo := money.NewRepository(db)
	service := money.NewService(repo)
	return moneyModule{service: service, handler: money.NewHandler(service)}
}

func buildTimetrackModule(db *sql.DB) timetrackModule {
	repo := timetrack.NewRepository(db)
	service := timetrack.NewService(repo)
	return timetrackModule{service: service, handler: timetrack.NewHandler(service)}
}

func buildQuotesModule(db *sql.DB, cfg config.Config, logger *slog.Logger, clk clockplatform.Clock, runtime *jobs.Runtime) (quotesModule, error) {
	repo := quotes.NewRepository(db)
	service := quotes.NewService(repo, quotes.Config{
		FetchDays:      cfg.QuotesFetchDays,
		RetryAttempts:  cfg.QuotesRetryAttempts,
		RetryDelay:     cfg.QuotesRetryDelay,
		RequestTimeout: cfg.QuotesRequestTimeout,
	}, clk, quotes.NewMarketFetcher(cfg.QuotesRequestTimeout))
	if cfg.QuotesJobEnabled {
		if err := runtime.Register("quotes", cfg.QuotesJobSchedule, func(ctx context.Context) error {
			_, err := service.Run(ctx)
			return err
		}); err != nil {
			return quotesModule{}, err
		}
	}
	return quotesModule{service: service, debugHandler: quotes.NewDebugHandler(service)}, nil
}

func buildBackupModule(db *sql.DB, cfg config.Config, logger *slog.Logger, clk clockplatform.Clock, runtime *jobs.Runtime, metricsRecorder *metrics.Service) (backupModule, error) {
	var uploader *s3platform.Client
	if cfg.BackupStorageEnabled {
		uploader = s3platform.NewClient(s3platform.Config{
			Region:          cfg.BackupStorageRegion,
			Bucket:          cfg.BackupStorageBucket,
			Endpoint:        cfg.BackupStorageEndpoint,
			ForcePathStyle:  cfg.BackupStorageForcePathStyle,
			StorageClass:    cfg.BackupStorageClass,
			AccessKeyID:     cfg.BackupStorageAccessKeyID,
			SecretAccessKey: cfg.BackupStorageSecretAccessKey,
		})
	}
	service := backup.NewService(db, backup.Config{
		DatabaseName:   cfg.DatabaseName,
		DatabaseEnv:    cfg.AppEnv,
		BackupsDir:     cfg.BackupsDir,
		StorageEnabled: cfg.BackupStorageEnabled,
		StorageClass:   cfg.BackupStorageClass,
	}, clk, logger, uploader)
	if cfg.BackupJobEnabled {
		if err := runtime.Register("backup", cfg.BackupJobSchedule, func(ctx context.Context) error {
			backupCtx, cancel := context.WithTimeout(ctx, cfg.BackupOperationTimeout)
			defer cancel()
			if _, err := service.Run(backupCtx); err != nil {
				return err
			}
			metricsRecorder.Increment(backup.MetricJobRan)
			return nil
		}); err != nil {
			return backupModule{}, err
		}
	}
	return backupModule{service: service, debugHandler: backup.NewDebugHandler(service)}, nil
}

func buildMetricsModule(cfg config.Config, logger *slog.Logger, hub *ws.Hub, authService *auth.Service, clk clockplatform.Clock, runtime *jobs.Runtime) (metricsModule, error) {
	service := metrics.NewService(cfg.MetricsServiceKey, clk, authService)
	realtime := metrics.NewRealtime(hub)
	flatlineClient := metrics.NewFlatlineClient(cfg.FlatlineBaseURL, cfg.FlatlinePushTimeout)

	exporter, err := metrics.NewExporter(metrics.ExporterConfig{
		Service:    cfg.MetricsServiceKey,
		NDJSONPath: filepath.Join(cfg.DataDir, "metrics-outbox.ndjson"),
		AckPath:    filepath.Join(cfg.DataDir, "metrics-outbox.ack.json"),
	}, flatlineClient, logger)
	if err != nil {
		return metricsModule{}, err
	}

	poller := metrics.NewPoller(flatlineClient, realtime, authService, cfg.FlatlinePollInterval, cfg.FlatlinePollInitialLookback, clk, logger)

	hub.RegisterHandler("METRICS_SUBSCRIBE", metrics.NewSubscribeHandler(service, realtime, flatlineClient, clk, logger))
	hub.RegisterHandler("METRICS_UNSUBSCRIBE", metrics.NewUnsubscribeHandler(realtime))

	if err := runtime.Register("metrics", "* * * * *", func(ctx context.Context) error {
		points := service.Flush()
		if len(points) == 0 {
			return nil
		}
		exporter.FlushAndPush(ctx, points[0].Bucket, points)
		return nil
	}); err != nil {
		return metricsModule{}, err
	}

	poller.Start()

	return metricsModule{service: service, realtime: realtime, poller: poller}, nil
}

func buildFoodModule(db *sql.DB, cfg config.Config, logger *slog.Logger, hub *ws.Hub, clk clockplatform.Clock, metricsRecorder food.MetricsRecorder) (foodModule, error) {
	repo := food.NewRepository(db)
	service := food.NewService(repo)
	service.SetClock(clk)
	service.SetPersonalKcalConfig(food.PersonalKcalConfig{
		LookbackMonths:          cfg.PersonalKcalLookbackMonths,
		DecayRate:               cfg.PersonalKcalDecayRate,
		CoverageThreshold:       cfg.PersonalKcalCoverageThreshold,
		MaxMonthlyChangePercent: cfg.PersonalKcalMaxMonthlyChangePercent,
		AnchorLambda:            cfg.PersonalKcalAnchorLambda,
		EvidenceHalfKcal:        cfg.PersonalKcalEvidenceHalfKcal,
		CoefLogStep:             cfg.PersonalKcalCoefLogStep,
		NormStep:                cfg.PersonalKcalNormStep,
		XStep:                   cfg.PersonalKcalXStep,
		Population:              cfg.PersonalKcalPopulation,
		MaxGenerations:          cfg.PersonalKcalMaxGenerations,
		MaxStale:                cfg.PersonalKcalMaxStale,
	})
	var mediaClient *food.OpenRouterMediaClient
	if strings.TrimSpace(cfg.OpenRouterAPIKey) != "" {
		productGenerator, err := food.NewOpenRouterProductGenerator(food.OpenRouterProductGeneratorConfig{
			APIKey:  cfg.OpenRouterAPIKey,
			Model:   cfg.OpenRouterModel,
			Timeout: cfg.OpenRouterTimeout,
			Logger:  logger,
		})
		if err != nil {
			return foodModule{}, err
		}
		service.SetProductGenerator(productGenerator)
		mediaClient, err = food.NewOpenRouterMediaClient(food.OpenRouterMediaClientConfig{
			APIKey:      cfg.OpenRouterAPIKey,
			VisionModel: cfg.OpenRouterVisionModel,
			ImageModel:  cfg.OpenRouterImageModel,
			Timeout:     cfg.OpenRouterTimeout,
			Logger:      logger,
		})
		if err != nil {
			return foodModule{}, err
		}
		service.SetImageAnalyzer(mediaClient)
	}
	if strings.TrimSpace(cfg.OpenAIAPIKey) != "" {
		embeddingGenerator, err := food.NewOpenAIEmbeddingGenerator(food.OpenAIEmbeddingGeneratorConfig{
			APIKey:     cfg.OpenAIAPIKey,
			Model:      cfg.OpenAIEmbeddingModel,
			Dimensions: cfg.OpenAIEmbeddingDims,
			Timeout:    cfg.OpenAITimeout,
		})
		if err != nil {
			return foodModule{}, err
		}
		service.SetEmbeddingGenerator(embeddingGenerator)
	}
	imageStore, err := food.NewImageStore(cfg.PublicDir)
	if err != nil {
		return foodModule{}, err
	}
	service.SetImageVersionProvider(imageStore)
	realtime := food.NewWSRealtimePublisher(hub, clk)
	imagePipeline := food.NewImagePipeline(imageStore, mediaClient, realtime, cfg.ImageGenerationMaxAttempts, logger)
	service.SetImageGenerationRequester(imagePipeline)
	hub.RegisterHandler("SEARCH_QUERY", food.NewSearchWSHandler(service, clk))
	return foodModule{
		service:          service,
		readHandler:      food.NewHandler(service, realtime),
		writeHandler:     food.NewWriteHandler(service, realtime, metricsRecorder),
		catalogueHandler: food.NewCatalogueHandler(service, realtime, metricsRecorder),
		imageHandler:     food.NewImageHandler(imageStore),
		labHandler:       food.NewLabHandler(food.NewLabService(repo, service, imagePipeline)),
		debugHandler:     food.NewDebugHandler(food.NewDebugService(repo, cfg.BackupsDir, mediaClient, service, realtime)),
		backgrounds:      []interface{ Close() error }{imagePipeline, imageStore},
	}, nil
}
