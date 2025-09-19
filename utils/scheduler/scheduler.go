package scheduler

import (
	"context"
	"log"
	"sync"
	"time"
)

// Task represents a scheduled task
type Task struct {
	Name     string
	Interval time.Duration
	Function func()
	ticker   *time.Ticker
}

// Scheduler manages periodic tasks
type Scheduler struct {
	tasks []*Task
	wg    sync.WaitGroup
	mu    sync.Mutex
}

// New creates a new task scheduler
func New() *Scheduler {
	return &Scheduler{
		tasks: make([]*Task, 0),
	}
}

// Every schedules a function to run at the specified interval
func (s *Scheduler) Every(interval time.Duration, name string, fn func()) *Scheduler {
	s.mu.Lock()
	defer s.mu.Unlock()

	task := &Task{
		Name:     name,
		Interval: interval,
		Function: fn,
	}

	s.tasks = append(s.tasks, task)
	return s
}

// RunAfter schedules a function to run once after a delay
func (s *Scheduler) RunAfter(delay time.Duration, name string, fn func()) *Scheduler {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a special task that runs once
	task := &Task{
		Name:     name,
		Interval: 0, // 0 indicates run once
		Function: func() {
			time.Sleep(delay)
			fn()
		},
	}

	s.tasks = append(s.tasks, task)
	return s
}

// Run starts all scheduled tasks and blocks until context is cancelled
func (s *Scheduler) Run(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Printf("=== STARTING SCHEDULER WITH %d TASKS ===", len(s.tasks))

	for _, task := range s.tasks {
		if task.Interval > 0 {
			// Periodic task
			log.Printf("✓ Scheduled '%s': Every %v", task.Name, task.Interval)
			s.wg.Add(1)
			go s.runPeriodic(ctx, task)
		} else {
			// One-time task
			log.Printf("✓ Scheduled '%s': One-time task", task.Name)
			s.wg.Add(1)
			go s.runOnce(ctx, task)
		}
	}

	// Wait for context cancellation
	<-ctx.Done()

	// Stop all tickers
	for _, task := range s.tasks {
		if task.ticker != nil {
			task.ticker.Stop()
		}
	}

	// Wait for all goroutines to finish
	s.wg.Wait()
	log.Println("=== SCHEDULER STOPPED ===")
}

// RunAsync starts all scheduled tasks without blocking
func (s *Scheduler) RunAsync(ctx context.Context) {
	go s.Run(ctx)
}

// Stop gracefully stops all tasks
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, task := range s.tasks {
		if task.ticker != nil {
			task.ticker.Stop()
		}
	}
}

// GetTaskCount returns the number of scheduled tasks
func (s *Scheduler) GetTaskCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}

// Private methods

func (s *Scheduler) runPeriodic(ctx context.Context, task *Task) {
	defer s.wg.Done()

	ticker := time.NewTicker(task.Interval)
	task.ticker = ticker
	defer ticker.Stop()

	// Run immediately on start
	s.safeRun(task)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.safeRun(task)
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context, task *Task) {
	defer s.wg.Done()

	select {
	case <-ctx.Done():
		return
	default:
		s.safeRun(task)
	}
}

func (s *Scheduler) safeRun(task *Task) {
	// Recover from panics to prevent one task from crashing the scheduler
	defer func() {
		if r := recover(); r != nil {
			log.Printf("ERROR: Task '%s' panicked: %v", task.Name, r)
		}
	}()

	task.Function()
}

// Builder provides a fluent interface for building a scheduler
type Builder struct {
	scheduler *Scheduler
}

// NewBuilder creates a new scheduler builder
func NewBuilder() *Builder {
	return &Builder{
		scheduler: New(),
	}
}

// AddTask adds a periodic task to the scheduler
func (b *Builder) AddTask(name string, interval time.Duration, fn func()) *Builder {
	b.scheduler.Every(interval, name, fn)
	return b
}

// AddDelayedTask adds a one-time delayed task
func (b *Builder) AddDelayedTask(name string, delay time.Duration, fn func()) *Builder {
	b.scheduler.RunAfter(delay, name, fn)
	return b
}

// Build returns the configured scheduler
func (b *Builder) Build() *Scheduler {
	return b.scheduler
}

// Example usage:
// scheduler := scheduler.NewBuilder().
//     AddTask("check_traders", 30*time.Second, s.checkAllTraders).
//     AddTask("check_exits", 20*time.Second, s.checkExitConditions).
//     AddTask("status", 2*time.Minute, s.printStatus).
//     Build()
// scheduler.Run(ctx)
