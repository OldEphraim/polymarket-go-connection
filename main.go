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

type StrategyProcess struct {
	Name   string
	Cmd    *exec.Cmd
	Config string
}

func main() {
	godotenv.Load()

	// Parse command line flags
	runType := flag.String("run", "test", "Run type (test, prod, etc.)")
	useBinary := flag.Bool("binary", false, "Use compiled binaries instead of go run")
	flag.Parse()

	log.Printf("Starting strategy orchestrator with run type: %s (binary mode: %v)", *runType, *useBinary)

	// Find all config files matching pattern
	configPattern := fmt.Sprintf("configs/*-%s.json", *runType)
	configFiles, err := filepath.Glob(configPattern)
	if err != nil {
		log.Fatal("Failed to find config files:", err)
	}

	if len(configFiles) == 0 {
		log.Fatalf("No config files found matching pattern: %s", configPattern)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	var wg sync.WaitGroup
	strategies := []StrategyProcess{}

	// Launch each strategy
	for _, configFile := range configFiles {
		baseName := filepath.Base(configFile)
		parts := strings.Split(baseName, "-")
		if len(parts) < 2 {
			log.Printf("Skipping invalid config name: %s", baseName)
			continue
		}
		strategyName := parts[0]

		var cmd *exec.Cmd

		if *useBinary {
			// Docker mode - use compiled binaries
			binaryPath, err := exec.LookPath(strategyName)
			if err != nil {
				log.Printf("Strategy binary not found: %s (skipping)", strategyName)
				continue
			}
			log.Printf("Starting strategy: %s with config: %s (binary: %s)", strategyName, baseName, binaryPath)
			cmd = exec.CommandContext(ctx, binaryPath, "--config", baseName)
		} else {
			// Local development mode - use go run
			strategyPath := fmt.Sprintf("./strategies/%s/main.go", strategyName)
			if _, err := os.Stat(strategyPath); os.IsNotExist(err) {
				log.Printf("Strategy source not found: %s (skipping)", strategyPath)
				continue
			}
			log.Printf("Starting strategy: %s with config: %s (go run: %s)", strategyName, baseName, strategyPath)
			cmd = exec.CommandContext(ctx, "go", "run", strategyPath, "--config", baseName)
		}

		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		strategies = append(strategies, StrategyProcess{
			Name:   strategyName,
			Cmd:    cmd,
			Config: baseName,
		})

		wg.Add(1)
		go func(sp StrategyProcess) {
			defer wg.Done()

			// Add small delay between launches to avoid DB conflicts
			time.Sleep(2 * time.Second)

			if err := sp.Cmd.Start(); err != nil {
				log.Printf("Failed to start %s: %v", sp.Name, err)
				return
			}

			log.Printf("Started %s (PID: %d)", sp.Name, sp.Cmd.Process.Pid)

			// Wait for process to complete
			if err := sp.Cmd.Wait(); err != nil {
				// Check if it was a signal termination (which is expected on shutdown)
				if exitErr, ok := err.(*exec.ExitError); ok {
					if exitErr.ExitCode() == -1 {
						log.Printf("Strategy %s terminated by signal", sp.Name)
					} else {
						log.Printf("Strategy %s exited with error: %v", sp.Name, err)
					}
				} else {
					log.Printf("Strategy %s error: %v", sp.Name, err)
				}
			} else {
				log.Printf("Strategy %s completed successfully", sp.Name)
			}
		}(strategies[len(strategies)-1])
	}

	if len(strategies) == 0 {
		log.Fatal("No strategies were started")
	}

	// Wait for interrupt signal
	go func() {
		<-sigChan
		log.Println("\nShutdown signal received, stopping strategies...")
		cancel() // This will terminate all strategy processes
	}()

	// Wait for all strategies to complete
	wg.Wait()
	log.Println("All strategies stopped")
}
