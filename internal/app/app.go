package app

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/drstein77/priceanalyzer/internal/bdkeeper"
	"github.com/drstein77/priceanalyzer/internal/config"
	"github.com/drstein77/priceanalyzer/internal/controllers"
	"github.com/drstein77/priceanalyzer/internal/logger"
	"github.com/drstein77/priceanalyzer/internal/middleware"
	"github.com/drstein77/priceanalyzer/internal/storage"
	"github.com/go-chi/chi"
)

type Server struct {
	srv *http.Server
	ctx context.Context
}

// NewServer creates a new Server instance with the provided context
func NewServer(ctx context.Context) *Server {
	server := new(Server)
	server.ctx = ctx
	return server
}

// Serve starts the server and handles signal interruption for graceful shutdown
func (server *Server) Serve() {
	// create and initialize a new option instance
	option := config.NewOptions()
	option.ParseFlags()

	// get a new logger
	nLogger, err := logger.NewLogger(option.LogLevel())
	if err != nil {
		log.Fatalln(err)
	}

	// initialize the keeper instance
	keeper := initializeKeeper(option.DataBaseDSN, nLogger)
	if keeper == nil {
		nLogger.Debug("Failed to initialize keeper")
	}
	defer keeper.Close()

	// initialize the storage instance
	memoryStorage := initializeStorage(server.ctx, keeper, nLogger)
	if memoryStorage == nil {
		nLogger.Debug("Failed to initialize storage")
	}

	// create a new controller to process incoming requests
	basecontr := initializeBaseController(server.ctx, memoryStorage, nLogger)

	// get a middleware for logging requests
	reqLog := middleware.NewReqLog(nLogger)

	// create router and mount routes
	r := chi.NewRouter()
	r.Use(reqLog.RequestLogger)
	r.Mount("/", basecontr.Route())

	// configure and start the server
	server.srv = startServer(r, option.RunAddr())

	// Create a channel to receive interrupt signals (e.g., CTRL+C)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt)

	// Block execution until a signal is received
	<-stopChan
}

// initializeKeeper initializes a BDKeeper instance
func initializeKeeper(dataBaseDSN func() string, logger *logger.Logger) *bdkeeper.BDKeeper {
	if dataBaseDSN() == "" {
		logger.Warn("DataBaseDSN is empty")
		return nil
	}

	return bdkeeper.NewBDKeeper(dataBaseDSN, logger)
}

// initializeStorage initializes a MemoryStorage instance
func initializeStorage(ctx context.Context, keeper storage.Keeper, logger *logger.Logger) *storage.MemoryStorage {
	if keeper == nil {
		logger.Warn("Keeper is nil, cannot initialize storage")
		return nil
	}

	return storage.NewMemoryStorage(ctx, keeper, logger)
}

// initializeBaseController initializes a BaseController instance
func initializeBaseController(ctx context.Context, storage *storage.MemoryStorage,
	logger *logger.Logger,
) *controllers.BaseController {
	return controllers.NewBaseController(ctx, storage, logger)
}

// startServer configures and starts an HTTP server with the provided router and address
func startServer(router chi.Router, address string) *http.Server {
	const (
		oneMegabyte = 1 << 20
		readTimeout = 3 * time.Second
	)

	server := &http.Server{
		Addr:                         address,
		Handler:                      router,
		ReadHeaderTimeout:            readTimeout,
		WriteTimeout:                 readTimeout,
		IdleTimeout:                  readTimeout,
		ReadTimeout:                  readTimeout,
		MaxHeaderBytes:               oneMegabyte, // 1 MB
		DisableGeneralOptionsHandler: false,
		TLSConfig:                    nil,
		TLSNextProto:                 nil,
		ConnState:                    nil,
		ErrorLog:                     nil,
		BaseContext:                  nil,
		ConnContext:                  nil,
	}

	go func() {
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalln(err)
		}
	}()

	return server
}

// Shutdown gracefully shuts down the server
func (server *Server) Shutdown(timeout time.Duration) {
	ctxShutDown, cancel := context.WithTimeout(server.ctx, timeout)
	defer cancel()

	log.Println("attempting to stop the server")

	if server.srv != nil {
		if err := server.srv.Shutdown(ctxShutDown); err != nil {
			if !errors.Is(err, http.ErrServerClosed) {
				log.Printf("server Shutdown Failed: %s", err)
				return
			}
		}
		log.Println("server stopped")
	}

	log.Println("server exited properly")
}
