package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

type ServiceProcess struct {
	Name   string
	Cmd    *exec.Cmd
	Config string
	IsCore bool // Core services like gatherer must start first
}

func main() {
	godotenv.Load()

	runType := flag.String("run", "test", "Run type (test, prod, etc.)")
	useBinary := flag.Bool("binary", false, "Use compiled binaries")
	flag.Parse()

	log.Printf("Starting orchestrator with run type: %s (binary mode: %v)", *runType, *useBinary)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup

	// Start gatherer first
	log.Println("Starting gatherer service...")
	gathererCmd := exec.CommandContext(ctx, "gatherer")
	gathererCmd.Stdout = os.Stdout
	gathererCmd.Stderr = os.Stderr

	if err := gathererCmd.Start(); err != nil {
		log.Fatal("Failed to start gatherer:", err)
	}
	log.Printf("Gatherer started (PID: %d)", gathererCmd.Process.Pid)

	// Wait for gatherer to initialize
	time.Sleep(10 * time.Second)

	// After starting gatherer, start the API server
	log.Println("Starting API server...")
	apiCmd := exec.CommandContext(ctx, "api") // You'll need to build this
	apiCmd.Stdout = os.Stdout
	apiCmd.Stderr = os.Stderr
	apiCmd.Env = append(os.Environ(),
		"API_KEY="+os.Getenv("API_KEY"),
		"DATABASE_URL="+os.Getenv("DATABASE_URL"),
	)

	if err := apiCmd.Start(); err != nil {
		log.Fatal("Failed to start API:", err)
	}
	log.Printf("API server started (PID: %d)", apiCmd.Process.Pid)

	// Also wait for API
	wg.Add(1)
	go func() {
		defer wg.Done()
		apiCmd.Wait()
	}()

	// Find and start strategies
	configPattern := fmt.Sprintf("configs/*-%s.json", *runType)
	configFiles, err := filepath.Glob(configPattern)
	if err != nil {
		log.Fatal("Failed to find config files:", err)
	}

	strategies := []ServiceProcess{}

	for _, configFile := range configFiles {
		baseName := filepath.Base(configFile)
		parts := strings.Split(baseName, "-")
		if len(parts) < 2 {
			continue
		}
		strategyName := parts[0]

		var cmd *exec.Cmd
		if *useBinary {
			binaryPath, err := exec.LookPath(strategyName)
			if err != nil {
				log.Printf("Strategy binary not found: %s", strategyName)
				continue
			}
			cmd = exec.CommandContext(ctx, binaryPath, "--config", baseName)
		} else {
			strategyPath := fmt.Sprintf("./strategies/%s/main.go", strategyName)
			if _, err := os.Stat(strategyPath); os.IsNotExist(err) {
				continue
			}
			cmd = exec.CommandContext(ctx, "go", "run", strategyPath, "--config", baseName)
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		strategies = append(strategies, ServiceProcess{
			Name:   strategyName,
			Cmd:    cmd,
			Config: baseName,
		})

		wg.Add(1)
		go func(sp ServiceProcess) {
			defer wg.Done()
			time.Sleep(2 * time.Second)

			if err := sp.Cmd.Start(); err != nil {
				log.Printf("Failed to start %s: %v", sp.Name, err)
				return
			}

			log.Printf("Started %s (PID: %d)", sp.Name, sp.Cmd.Process.Pid)

			if err := sp.Cmd.Wait(); err != nil {
				log.Printf("Strategy %s exited: %v", sp.Name, err)
			}
		}(strategies[len(strategies)-1])
	}

	// Wait for interrupt
	go func() {
		<-sigChan
		log.Println("\nShutdown signal received...")
		cancel()
	}()

	// Also wait for gatherer
	wg.Add(1)
	go func() {
		defer wg.Done()
		gathererCmd.Wait()
	}()

	wg.Wait()
	log.Println("All services stopped")
}
