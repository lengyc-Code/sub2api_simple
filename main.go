package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"elysiafly.com/sub2api_simple/internal/gateway"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to config file")
	flag.Parse()

	cfg, err := gateway.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	gw := gateway.New(cfg)

	srv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      gw,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	log.Printf("Sub2API Standalone starting on %s", cfg.ListenAddr)
	log.Printf("  Accounts: %d", len(cfg.Accounts))
	for i, a := range cfg.Accounts {
		authMode := "static"
		if a.RefreshToken != "" {
			authMode = "refresh_token"
		}
		log.Printf("  [%d] %s (platform=%s, type=%s, auth=%s, priority=%d, concurrency=%d)",
			i+1, a.Name, a.Platform, a.Type, authMode, a.Priority, a.Concurrency)
	}
	log.Printf("  Auth tokens: %d configured", len(cfg.AuthTokens))
	log.Printf("  Request log enabled: %v", cfg.EnableRequestLog)
	log.Printf("  Stream debug log enabled: %v", cfg.EnableStreamDebugLog)
	log.Printf("  Max failover switches: %d", cfg.MaxAccountSwitches)
	log.Printf("  Sticky session TTL: %v", cfg.StickySessionTTL.Duration)

	needsLogin := gw.PrewarmOpenAITokens(context.Background(), 30*time.Second)

	// Print login URLs for accounts that need browser authorization
	if len(needsLogin) > 0 {
		log.Println("")
		log.Println("========================================================")
		log.Println("Some accounts need browser login to obtain tokens:")
		log.Println("========================================================")
		for _, name := range needsLogin {
			log.Printf("  -> http://localhost%s/auth/login?account=%s", cfg.ListenAddr, url.QueryEscape(name))
		}
		log.Println("")
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server failed: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Shutdown failed: %v", err)
	}
	log.Println("Server stopped")
}
