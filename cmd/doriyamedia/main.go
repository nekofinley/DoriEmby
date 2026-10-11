package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"doriyamedia/internal/api"
	"doriyamedia/internal/config"
	"doriyamedia/internal/db"
	"doriyamedia/internal/media"
	"doriyamedia/internal/metadata"
	"doriyamedia/internal/models"
	"doriyamedia/internal/scan"
	"doriyamedia/internal/source"
	"doriyamedia/internal/web"
)

func main() {
	cfgPath := flag.String("config", "config.json", "config file path")
	port := flag.Int("port", 0, "override listen port")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if *port > 0 {
		cfg.Port = *port
	}
	if err := os.MkdirAll(cfg.ImagesDir(), 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	store, err := db.Open(cfg.DBPath())
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	engine := media.NewEngine(store, cfg)
	manager := metadata.NewManager(store, cfg)
	scanner := &scan.Scanner{
		Store: store,
		Cfg:   cfg,
		OnIdentify: func(lib *models.Library, listings map[string][]source.Entry, report func(done, total int, msg string)) {
			manager.SeedListings(listings)
			manager.EnrichLibrary(lib, report)
		},
	}

	srv := api.New(store, cfg, scanner, manager, engine, web.Dist())
	srv.ConfigPath = *cfgPath

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	if os.Getenv("DORIYAMEDIA_LOCAL") == "1" {
		addr = fmt.Sprintf("127.0.0.1:%d", cfg.Port) // 仅本机访问
	}
	log.Printf("===========================================")
	log.Printf("  DoriyaMedia v0.1.0  →  http://%s", addr)
	log.Printf("  首次使用请先打开网页完成管理员初始化")
	log.Printf("===========================================")
	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
