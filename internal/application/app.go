package application

import (
	_ "booking_api/docs"
	"booking_api/internal/config"
	"booking_api/internal/handler"
	"booking_api/internal/infrastructure/database"
	"booking_api/internal/infrastructure/logger"
	"booking_api/internal/infrastructure/redislock"
	"booking_api/internal/middleware"
	"booking_api/internal/observability"
	"booking_api/internal/redisqueue"
	"booking_api/internal/repository"
	"booking_api/internal/server"
	"booking_api/internal/services"
	"context"
	"fmt"
	"sync"
	"time"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type App struct {
	config   config.Config
	server   *server.HTTPServer
	db       *gorm.DB
	logger   *zap.Logger
	seatLock *redislock.SeatLocker
	handler  *handler.BookingHandler
	stopOnce sync.Once
}

var (
	newLoggerFn       = logger.NewLogger
	newHTTPServerFn   = server.NewHTTPServer
	connectPostgresFn = database.ConnectPostgres
)

func NewApp(cfg config.Config) (*App, error) {
	zapLogger, err := newLoggerFn(cfg.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to init logger: %w", err)
	}

	srv := newHTTPServerFn(cfg.Server)

	db, err := connectPostgresFn(cfg.Postgres)
	if err != nil {
		return nil, fmt.Errorf("failed to init database: %w", err)
	}

	// ---- Wire dependencies ----
	var seatLocker *redislock.SeatLocker
	var queueManager *redisqueue.Manager
	if cfg.Redis.Addr != "" {
		redisClient := redislock.NewClient(cfg.Redis)
		pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := redislock.Ping(pingCtx, redisClient)
		cancel()
		if err != nil {
			_ = redisClient.Close()
			return nil, fmt.Errorf("redis ping failed (addr=%s db=%d): %w", cfg.Redis.Addr, cfg.Redis.DB, err)
		}
		zapLogger.Info("redis connection verified",
			zap.String("addr", cfg.Redis.Addr),
			zap.Int("db", cfg.Redis.DB),
		)
		seatLocker = redislock.NewSeatLocker(redisClient, cfg.Redis.TTL)
		queueManager = redisqueue.NewManager(redisClient, cfg.Redis.QueueTTL, zapLogger)
	} else {
		zapLogger.Info("redis disabled for seat locking (REDIS_ADDR empty); holds use database only")
	}

	bookingRepo := repository.NewBookingRepository(db, zapLogger)
	bookingService := services.NewBookingServiceWithSeatLocker(zapLogger, bookingRepo, seatLocker)
	virtualQueueService := services.NewVirtualQueueService(zapLogger, queueManager, bookingRepo, cfg.Redis.QueueActiveLimit)
	bookingHandler := handler.NewBookingHandler(bookingService, zapLogger)
	bookingHandler.SetQueueService(virtualQueueService)
	bookingHandler.StartExpiredHoldReleaser(15 * time.Second)

	// ---- Register middleware ----
	router := srv.Router()
	metrics := observability.NewMetrics()
	router.Use(middleware.CORS())
	router.Use(middleware.RequestID())
	router.Use(metrics.Middleware())
	router.Use(middleware.RequestLogger(zapLogger))
	router.GET("/metrics", metrics.Handler())
	router.Use(middleware.RequireAuth(middleware.AuthConfig{
		JWTSecret:    cfg.Auth.JWTSecret,
		JWTAlgorithm: cfg.Auth.JWTAlgorithm,
	}))
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// ---- Register routes ----
	apiV1 := router.Group("/api/v1")
	bookingHandler.RegisterRoutes(apiV1)

	zapLogger.Info("application initialized successfully")

	return &App{
		config:   cfg,
		logger:   zapLogger,
		server:   srv,
		db:       db,
		seatLock: seatLocker,
		handler:  bookingHandler,
	}, nil
}

func (app *App) Start() error {
	app.logger.Info("starting http server...")
	return app.server.Start()
}

func (app *App) Shutdown(ctx context.Context) error {
	app.logger.Info("shutting down application...")

	if err := app.server.Shutdown(ctx); err != nil {
		app.logger.Error("shutdown server failed", zap.Error(err))
		return err
	}

	app.cleanup()
	app.logger.Info("shutdown application successfully")
	_ = app.logger.Sync()

	return nil
}

func (app *App) ForceShutdown() error {
	app.logger.Warn("forcing application shutdown")

	if err := app.server.Close(); err != nil {
		app.logger.Error("force close server failed", zap.Error(err))
		return err
	}

	app.cleanup()
	app.logger.Info("forced shutdown completed")
	_ = app.logger.Sync()

	return nil
}

func (app *App) cleanup() {
	app.stopOnce.Do(func() {
		sqlDB, err := app.db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
		if app.seatLock != nil {
			_ = app.seatLock.Close()
		}
		if app.handler != nil {
			app.handler.StopExpiredHoldReleaser()
		}
	})
}
