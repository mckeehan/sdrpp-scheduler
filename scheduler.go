package main

import (
	"log"
	"sync"
	"time"
)

// cronJob pairs a parsed cron expression with its raw schedule entry.
type cronJob struct {
	expr CronExpr
	raw  ScheduleEntry
}

// activeJob tracks a currently running recording.
type activeJob struct {
	entry     ScheduleEntry
	startTime time.Time
}

// Scheduler manages all scheduled recording tasks.
type Scheduler struct {
	cfg    *Config
	client *RigCtlClient
	logger *log.Logger
	stopCh chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	active *activeJob // currently running recording job, if any

	// dispatched tracks (entry.Name + minute) keys for jobs already fired this minute,
	// preventing double-dispatch when the 10-second ticker fires multiple times.
	dispatched map[string]bool
}

// NewScheduler creates a new Scheduler.
func NewScheduler(cfg *Config, client *RigCtlClient, logger *log.Logger) *Scheduler {
	return &Scheduler{
		cfg:        cfg,
		client:     client,
		logger:     logger,
		stopCh:     make(chan struct{}),
		dispatched: make(map[string]bool),
	}
}

// Run starts the scheduling loop. It blocks until Stop() is called.
func (s *Scheduler) Run() {
	s.logger.Println("Scheduler started. Press Ctrl+C to stop.")

	// Parse all cron expressions upfront.
	var jobs []cronJob
	for _, entry := range s.cfg.Schedule {
		if !entry.Enabled {
			s.logger.Printf("Skipping disabled entry: %s", entry.Name)
			continue
		}
		cron, err := ParseCron(entry.Cron)
		if err != nil {
			s.logger.Printf("Invalid cron for %q: %v - skipping", entry.Name, err)
			continue
		}
		jobs = append(jobs, cronJob{expr: *cron, raw: entry})
	}

	if len(jobs) == 0 {
		s.logger.Println("No enabled schedule entries found.")
		return
	}

	// Ticker fires every 10 seconds so we don't miss any minute boundary.
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	// Prune the dispatched map every minute to avoid unbounded growth.
	cleanupTicker := time.NewTicker(time.Minute)
	defer cleanupTicker.Stop()

	// Check immediately on start (in case a job fires in the current minute).
	s.checkAndDispatch(jobs)

	for {
		select {
		case <-s.stopCh:
			s.logger.Println("Scheduler received stop signal.")
			s.waitForActive()
			return

		case <-ticker.C:
			s.checkAndDispatch(jobs)

		case <-cleanupTicker.C:
			s.mu.Lock()
			s.dispatched = make(map[string]bool)
			s.mu.Unlock()
		}
	}
}

// Stop signals the scheduler to stop. Any active recording will complete first.
func (s *Scheduler) Stop() {
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}
}

// checkAndDispatch checks whether any job is due in the current minute and
// dispatches it if it has not already been dispatched this minute.
func (s *Scheduler) checkAndDispatch(jobs []cronJob) {
	now := time.Now()
	windowStart := now.Truncate(time.Minute)
	windowEnd := windowStart.Add(time.Minute)

	for _, job := range jobs {
		// What is the next fire time after just before this minute?
		next := job.expr.Next(windowStart.Add(-time.Second))
		if next.IsZero() {
			continue
		}
		// Is that time within the current minute window?
		if next.Before(windowStart) || !next.Before(windowEnd) {
			continue
		}
		// Guard against late starts: skip if we missed by more than 30 seconds.
		if now.Sub(next) > 30*time.Second {
			continue
		}

		// Deduplication key: name + scheduled minute.
		key := job.raw.Name + "|" + next.Format("2006-01-02T15:04")
		s.mu.Lock()
		already := s.dispatched[key]
		if !already {
			s.dispatched[key] = true
		}
		s.mu.Unlock()

		if already {
			continue
		}

		// If fire time is slightly in the future, wait for it in a goroutine.
		delay := time.Until(next)
		entry := job.raw
		scheduledTime := next
		if delay > 0 {
			go s.dispatchAfter(entry, scheduledTime, delay)
		} else {
			go s.dispatchNow(entry, scheduledTime)
		}
	}
}

// dispatchAfter waits delay then calls dispatchNow.
func (s *Scheduler) dispatchAfter(entry ScheduleEntry, scheduledTime time.Time, delay time.Duration) {
	select {
	case <-time.After(delay):
		s.dispatchNow(entry, scheduledTime)
	case <-s.stopCh:
		s.logger.Printf("Job %q cancelled before start (scheduler stopped).", entry.Name)
	}
}

// dispatchNow runs the job immediately, respecting the active-job mutex.
func (s *Scheduler) dispatchNow(entry ScheduleEntry, scheduledTime time.Time) {
	s.mu.Lock()

	if s.active != nil {
		if s.active.entry.Name == entry.Name &&
			s.active.startTime.Truncate(time.Minute).Equal(scheduledTime.Truncate(time.Minute)) {
			// Same job already running for this minute.
			s.mu.Unlock()
			return
		}
		s.logger.Printf("WARNING: Job %q would start now but %q is still recording (started %s). Skipping.",
			entry.Name, s.active.entry.Name, s.active.startTime.Format("15:04:05"))
		s.mu.Unlock()
		return
	}

	job := &activeJob{
		entry:     entry,
		startTime: scheduledTime,
	}
	s.active = job
	s.mu.Unlock()

	s.wg.Add(1)
	defer s.wg.Done()

	s.runJob(job)

	s.mu.Lock()
	s.active = nil
	s.mu.Unlock()
}

// runJob executes a single recording session:
//  1. Set frequency
//  2. Set mode
//  3. Send AOS (start recording)
//  4. Wait for the configured duration (or shutdown)
//  5. Send LOS (stop recording)
func (s *Scheduler) runJob(job *activeJob) {
	e := job.entry
	s.logger.Printf("=== Starting job: %s ===", e.Name)
	s.logger.Printf("  Frequency : %s (%d Hz)", FormatFrequency(e.FrequencyHz), e.FrequencyHz)
	s.logger.Printf("  Mode      : %s (passband: %d Hz)", e.Mode, e.Passband)
	s.logger.Printf("  Duration  : %s", e.Duration)
	s.logger.Printf("  Stop at   : %s", time.Now().Add(e.Duration.Duration).Format("15:04:05"))

	// Step 1: Set frequency.
	if err := s.client.SetFrequency(e.FrequencyHz); err != nil {
		s.logger.Printf("ERROR [%s]: SetFrequency failed: %v", e.Name, err)
		return
	}
	time.Sleep(300 * time.Millisecond) // let SDR++ settle

	// Step 2: Set mode (non-fatal if unsupported).
	if err := s.client.SetMode(e.Mode, e.Passband); err != nil {
		s.logger.Printf("WARNING [%s]: SetMode failed: %v (continuing)", e.Name, err)
	}
	time.Sleep(300 * time.Millisecond)

	// Step 3: Start recording (AOS).
	if err := s.client.StartRecording(); err != nil {
		s.logger.Printf("ERROR [%s]: StartRecording (AOS) failed: %v", e.Name, err)
		return
	}
	s.logger.Printf("  > Recording STARTED for %q", e.Name)

	// Step 4: Wait for duration or shutdown signal.
	select {
	case <-time.After(e.Duration.Duration):
		s.logger.Printf("  . Duration elapsed for %q", e.Name)
	case <-s.stopCh:
		s.logger.Printf("  . Job %q interrupted by scheduler shutdown", e.Name)
	}

	// Step 5: Stop recording (LOS).
	if err := s.client.StopRecording(); err != nil {
		s.logger.Printf("ERROR [%s]: StopRecording (LOS) failed: %v", e.Name, err)
	} else {
		s.logger.Printf("  . Recording STOPPED for %q", e.Name)
	}

	s.logger.Printf("=== Job complete: %s ===", e.Name)
}

// waitForActive blocks until any currently active recording finishes.
func (s *Scheduler) waitForActive() {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()

	if active != nil {
		s.logger.Printf("Waiting for active job %q to finish before exiting...", active.entry.Name)
	}
	s.wg.Wait()
}
