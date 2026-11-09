package main

import (
	"context"
	"fmt"
	"lab-router/internal/api"
	"lab-router/internal/cwmp"
	"lab-router/internal/model"
	"lab-router/internal/network"
	"lab-router/internal/service"
	"lab-router/internal/store"
	"lab-router/internal/traversal"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
func run() error {
	mode := "api"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if mode == "show-wifi-password" {
		if os.Geteuid() != 0 {
			return fmt.Errorf("somente root local pode consultar esta credencial")
		}
		s, e := store.Open("/var/lib/lab-router")
		if e != nil {
			return e
		}
		defer s.DB.Close()
		c, e := s.Config()
		if e != nil {
			return e
		}
		fmt.Println(c.WiFi.Password)
		return nil
	}
	if mode == "helper" {
		return network.Serve()
	}
	if mode == "validate" {
		return network.ValidateEnvironment(model.Default())
	}
	if os.Geteuid() == 0 {
		return fmt.Errorf("API/CWMP devem executar sem root")
	}
	dir := env("LAB_DATA_DIR", "./var")
	s, e := store.Open(dir)
	if e != nil {
		return e
	}
	defer s.DB.Close()
	if mode == "init" {
		var existing string
		if s.Get("admin", &existing) == nil {
			return fmt.Errorf("instalação já inicializada; senha não alterada")
		}
		password := model.Token()
		if e = s.Init(password); e != nil {
			return e
		}
		path := filepath.Join(dir, "initial-admin-password")
		if _, e = os.Stat(path); os.IsNotExist(e) {
			if e = os.WriteFile(path, []byte(password+"\n"), 0600); e != nil {
				return e
			}
		}
		fmt.Println("Inicialização concluída. Senha administrativa em", path)
		return nil
	}
	if e = s.Init(os.Getenv("LAB_ADMIN_PASSWORD")); e != nil {
		return e
	}
	if mode == "apply-initial" {
		c, e := s.Config()
		if e != nil {
			return e
		}
		return network.Apply(c)
	}
	if mode == "inform-once" {
		svc := &service.ConfigService{Store: s, Dir: dir, Live: env("LAB_MODE", "observation") == "live"}
		a := cwmp.New(cwmp.Model{Service: svc})
		return a.Session(context.Background())
	}
	svc := &service.ConfigService{Store: s, Dir: dir, Live: env("LAB_MODE", "observation") == "live"}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if mode == "cwmp" {
		a := cwmp.New(cwmp.Model{Service: svc})
		manager := traversal.New(s, a.QueueRequest)
		go manager.Run(ctx)
		server := &http.Server{Addr: "127.0.0.1:7548", Handler: a.ConnectionHandler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 16 << 10}
		go func() { <-ctx.Done(); server.Shutdown(context.Background()) }()
		go func() {
			if e := server.ListenAndServe(); e != nil && e != http.ErrServerClosed {
				log.Print("Connection Request loopback indisponível")
			}
		}()
		a.Run(ctx)
		return nil
	}
	if mode != "api" {
		return fmt.Errorf("modo inválido: api | cwmp | helper | init | validate")
	}
	bind := env("LAB_BIND", "127.0.0.1:8088")
	host, _, e := net.SplitHostPort(bind)
	if e != nil || !(host == "127.0.0.1" || host == "::1") {
		return fmt.Errorf("painel deve permanecer em localhost nesta versão")
	}
	a := api.New(svc)
	server := &http.Server{Addr: bind, Handler: a.Router(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(shutdown)
	}()
	log.Printf("LAB Router http://%s (%s)", bind, strings.ToLower(env("LAB_MODE", "observation")))
	e = server.ListenAndServe()
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
