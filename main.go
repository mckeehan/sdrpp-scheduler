package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const version = "1.0.0"

func main() {
	// CLI flags
	configPath := flag.String("config", "config.yaml", "Path to the schedule configuration file")
	dryRun := flag.Bool("dry-run", false, "Print scheduled jobs without connecting to SDR++")
	verbose := flag.Bool("verbose", false, "Enable verbose logging")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("sdrpp-scheduler v%s\n", version)
		os.Exit(0)
	}

	// Set up logger
	logger := log.New(os.Stdout, "", log.LstdFlags)
	if *verbose {
		logger.SetFlags(log.LstdFlags | log.Lmicroseconds)
	}

	logger.Printf("sdrpp-scheduler v%s starting", version)

	// Load configuration
	cfg, err := LoadConfig(*configPath)
	if err != nil {
		logger.Fatalf("Failed to load config from %q: %v", *configPath, err)
	}

	logger.Printf("Loaded %d schedule entries from %s", len(cfg.Schedule), *configPath)

	if *dryRun {
		fmt.Println("\n=== Dry Run Mode - No connection to SDR++ ===")
		PrintSchedule(cfg)
		return
	}

	// Create SDR++ client
	client := NewRigCtlClient(cfg.SDRpp.Host, cfg.SDRpp.Port, cfg.SDRpp.Timeout.Duration, logger)

	// Verify connectivity
	logger.Printf("Connecting to SDR++ rigctl server at %s:%d...", cfg.SDRpp.Host, cfg.SDRpp.Port)
	if err := client.Ping(); err != nil {
		logger.Fatalf("Cannot reach SDR++ rigctl server: %v\n"+
			"Make sure SDR++ is running with the RigCtl Server module enabled.", err)
	}
	logger.Printf("Successfully connected to SDR++")

	// Create and start the scheduler
	scheduler := NewScheduler(cfg, client, logger)

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Printf("Received signal %v, shutting down gracefully...", sig)
		scheduler.Stop()
	}()

	// Print upcoming schedule
	logger.Println("Upcoming scheduled recordings:")
	PrintNextOccurrences(cfg, 5, logger)

	// Run the scheduler (blocking)
	scheduler.Run()
	logger.Println("Scheduler stopped.")
}

// PrintSchedule prints all schedule entries in a human-readable format.
func PrintSchedule(cfg *Config) {
	fmt.Printf("SDR++ server: %s:%d\n\n", cfg.SDRpp.Host, cfg.SDRpp.Port)
	fmt.Printf("%-30s %-16s %-8s %-12s %-10s %s\n",
		"Name", "Frequency", "Mode", "Duration", "Cron", "Next Run")
	fmt.Println("-----------------------------------------------------------------------------------------------------------")

	for _, entry := range cfg.Schedule {
		next := NextCronTime(entry.Cron)
		nextStr := "invalid cron"
		if !next.IsZero() {
			nextStr = next.Format("2006-01-02 15:04")
		}
		freq := FormatFrequency(entry.FrequencyHz)
		fmt.Printf("%-30s %-16s %-8s %-12s %-10s %s\n",
			entry.Name, freq, entry.Mode, entry.Duration, entry.Cron, nextStr)
	}
}

// PrintNextOccurrences logs the next N occurrences for each schedule entry.
func PrintNextOccurrences(cfg *Config, count int, logger *log.Logger) {
	for _, entry := range cfg.Schedule {
		next := NextCronTime(entry.Cron)
		if next.IsZero() {
			logger.Printf("  [%s] Invalid cron expression: %q", entry.Name, entry.Cron)
			continue
		}
		inDur := time.Until(next).Round(time.Second)
		logger.Printf("  [%s] %s @ %s (in %s) for %s",
			entry.Name,
			FormatFrequency(entry.FrequencyHz),
			next.Format("2006-01-02 15:04:05"),
			inDur,
			entry.Duration,
		)
	}
}
