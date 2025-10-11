package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type child struct {
	name      string
	config    string
	cmd       *exec.Cmd
	crashBack time.Duration
	cancel    context.CancelFunc
}

func main() {
	runType := flag.String("run", getenv("RUN_TYPE", "prod"), "Run profile (prod, test, etc.)")
	useBinary := flag.Bool("binary", true, "Use compiled binaries in /usr/local/bin")
	include := flag.String("include", "", "Comma-separated strategy names to include (optional)")
	exclude := flag.String("exclude", "", "Comma-separated strategy names to exclude (optional)")
	configGlob := flag.String("config_glob", "configs/*-%s.json", "Glob for configs, %s replaced with run")
	flag.Parse()

	log.Printf("[runner] starting (run=%s, binary=%v)", *runType, *useBinary)

	glob := *configGlob
	if strings.Contains(glob, "%s") {
		glob = fmt.Sprintf(glob, *runType)
	}
	cfgs, err := filepath.Glob(glob)
	if err != nil {
		log.Fatalf("[runner] bad glob: %v", err)
	}
	if len(cfgs) == 0 {
		log.Printf("[runner] no configs matched %q; nothing to do", glob)
	}

	inc := splitSet(*include)
	exc := splitSet(*exclude)

	// Resolve strategy name from config: "<name>-<run>.json" -> "<name>"
	strategies := make([]child, 0, len(cfgs))
	for _, cf := range cfgs {
		base := filepath.Base(cf)
		name := strings.SplitN(base, "-", 2)[0]
		if len(inc) > 0 && !inc[name] {
			continue
		}
		if exc[name] {
			continue
		}
		strategies = append(strategies, child{name: name, config: base})
	}
	sort.Slice(strategies, func(i, j int) bool { return strategies[i].name < strategies[j].name })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// graceful shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	var mu sync.Mutex
	var startOne func(*child)

	startOne = func(ch *child) {
		mu.Lock()
		defer mu.Unlock()

		if ch.cmd != nil && ch.cmd.Process != nil {
			return // already running
		}

		ctxChild, cancelChild := context.WithCancel(ctx)
		ch.cancel = cancelChild

		var cmd *exec.Cmd
		if *useBinary {
			// binary named exactly the strategy (e.g., /usr/local/bin/momentum)
			bin := ch.name
			if _, err := exec.LookPath(bin); err != nil {
				log.Printf("[runner] binary not found for %s: %v", ch.name, err)
				return
			}
			cmd = exec.CommandContext(ctxChild, bin, "--config", ch.config)
		} else {
			// dev mode: run from source
			path := fmt.Sprintf("./strategies/%s/main.go", ch.name)
			cmd = exec.CommandContext(ctxChild, "go", "run", path, "--config", ch.config)
		}

		cmd.Stdout = prefixWriter(os.Stdout, ch.name)
		cmd.Stderr = prefixWriter(os.Stderr, ch.name)
		ch.cmd = cmd

		go func(c *child) {
			log.Printf("[runner] starting %s (config=%s)", c.name, c.config)
			err := c.cmd.Start()
			if err != nil {
				log.Printf("[runner] start failed %s: %v", c.name, err)
				mu.Lock()
				c.cmd = nil
				mu.Unlock()
				backoff(c)
				return
			}
			waitErr := c.cmd.Wait()
			mu.Lock()
			c.cmd = nil
			mu.Unlock()
			if ctx.Err() != nil {
				return // shutting down
			}
			if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
				log.Printf("[runner] %s crashed: %v", c.name, waitErr)
				d := backoff(c)
				go func() {
					time.Sleep(d)
					if ctx.Err() == nil {
						startOne(c) // restart the same strategy
					}
				}()
			}
		}(ch)
	}

	stopOne := func(ch *child) {
		mu.Lock()
		defer mu.Unlock()
		if ch.cancel != nil {
			ch.cancel()
		}
		if ch.cmd != nil && ch.cmd.Process != nil {
			_ = ch.cmd.Process.Signal(syscall.SIGTERM)
		}
		ch.cmd = nil
		ch.cancel = nil
	}

	// Start all
	for i := range strategies {
		startOne(&strategies[i])
		time.Sleep(300 * time.Millisecond) // small stagger
	}

	// Block until signal
	<-sigs
	log.Printf("[runner] shutdown requested")
	for i := range strategies {
		stopOne(&strategies[i])
	}
	log.Printf("[runner] stopped")
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitSet(csv string) map[string]bool {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	m := map[string]bool{}
	for _, s := range strings.Split(csv, ",") {
		m[strings.TrimSpace(s)] = true
	}
	return m
}

func prefixWriter(w *os.File, prefix string) *os.File {
	// keep it simple; if you want strict prefixing per line, wrap with a custom writer
	return w
}

func backoff(c *child) time.Duration {
	if c.crashBack == 0 {
		c.crashBack = time.Second
	} else {
		c.crashBack *= 2
		if c.crashBack > 60*time.Second {
			c.crashBack = 60 * time.Second
		}
	}
	log.Printf("[runner] scheduling restart %s in %s", c.name, c.crashBack)
	d := c.crashBack
	c.crashBack = 0
	return d
}
