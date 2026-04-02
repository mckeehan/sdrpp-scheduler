package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"time"
)

// RigCtlClient handles TCP communication with the SDR++ rigctl server.
type RigCtlClient struct {
	host    string
	port    int
	timeout time.Duration
	logger  *log.Logger
}

// NewRigCtlClient creates a new rigctl client.
func NewRigCtlClient(host string, port int, timeout time.Duration, logger *log.Logger) *RigCtlClient {
	return &RigCtlClient{
		host:    host,
		port:    port,
		timeout: timeout,
		logger:  logger,
	}
}

func (c *RigCtlClient) addr() string {
	return fmt.Sprintf("%s:%d", c.host, c.port)
}

// sendCommand opens a fresh TCP connection, sends a command, reads the response,
// and closes the connection.
//
// SDR++'s rigctl server expects one command per connection in many builds, so
// opening a new connection per command is the most reliable approach.
func (c *RigCtlClient) sendCommand(cmd string) (string, error) {
	conn, err := net.DialTimeout("tcp", c.addr(), c.timeout)
	if err != nil {
		return "", fmt.Errorf("connecting to %s: %w", c.addr(), err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(c.timeout))

	// Send the command
	_, err = fmt.Fprintf(conn, "%s\n", cmd)
	if err != nil {
		return "", fmt.Errorf("sending command %q: %w", cmd, err)
	}

	// Read response lines until we get RPRT or EOF
	var lines []string
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		// Stop reading after RPRT response (command acknowledgement)
		if strings.HasPrefix(line, "RPRT") {
			break
		}
		// For get commands, we may get data before RPRT
		// Collect all lines until RPRT or scanner exhausts
	}

	response := strings.Join(lines, "\n")

	// Check for errors: RPRT 0 = success, anything else = error
	for _, line := range lines {
		if strings.HasPrefix(line, "RPRT") {
			parts := strings.Fields(line)
			if len(parts) >= 2 && parts[1] != "0" {
				return response, fmt.Errorf("rigctl error for %q: %s", cmd, line)
			}
			break
		}
	}

	return response, nil
}

// Ping verifies the connection to SDR++ by querying the current frequency.
func (c *RigCtlClient) Ping() error {
	_, err := c.sendCommand("f")
	return err
}

// SetFrequency tunes SDR++ to the given frequency in Hz.
func (c *RigCtlClient) SetFrequency(hz int64) error {
	c.logger.Printf("rigctl: SetFrequency(%d Hz / %s)", hz, FormatFrequency(hz))
	resp, err := c.sendCommand(fmt.Sprintf("F %d", hz))
	if err != nil {
		return fmt.Errorf("SetFrequency: %w", err)
	}
	c.logger.Printf("rigctl: SetFrequency response: %q", resp)
	return nil
}

// SetMode sets the demodulation mode in SDR++.
// passband is in Hz; use 0 for the radio default.
// Supported modes: USB LSB AM FM WFM CW CWR RTTY DSB NFM RAW
func (c *RigCtlClient) SetMode(mode string, passband int) error {
	c.logger.Printf("rigctl: SetMode(%s, passband=%d)", mode, passband)
	resp, err := c.sendCommand(fmt.Sprintf("M %s %d", mode, passband))
	if err != nil {
		return fmt.Errorf("SetMode: %w", err)
	}
	c.logger.Printf("rigctl: SetMode response: %q", resp)
	return nil
}

// GetFrequency queries the current tuned frequency from SDR++.
func (c *RigCtlClient) GetFrequency() (int64, error) {
	resp, err := c.sendCommand("f")
	if err != nil {
		return 0, fmt.Errorf("GetFrequency: %w", err)
	}
	var freq int64
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "RPRT") {
			continue
		}
		if _, err := fmt.Sscanf(line, "%d", &freq); err == nil {
			return freq, nil
		}
	}
	return 0, fmt.Errorf("could not parse frequency from response: %q", resp)
}

// StartRecording sends the AOS command to SDR++ to begin recording.
// Requires the RigCtl Server module to have "Recording" enabled and
// a Recorder module selected as the controlled recorder in SDR++.
func (c *RigCtlClient) StartRecording() error {
	c.logger.Println("rigctl: StartRecording (AOS)")
	resp, err := c.sendCommand("\\start")
	c.logger.Printf("wjm: %s", resp)
	resp, err = c.sendCommand("AOS")
	if err != nil {
		// SDR++ may not respond with RPRT to AOS — treat non-fatal
		c.logger.Printf("rigctl: AOS response (may be empty): %q err=%v", resp, err)
		return nil
	}
	c.logger.Printf("rigctl: AOS response: %q", resp)
	return nil
}

// StopRecording sends the LOS command to SDR++ to stop recording.
func (c *RigCtlClient) StopRecording() error {
	c.logger.Println("rigctl: StopRecording (LOS)")
	resp, err := c.sendCommand("LOS")
	if err != nil {
		c.logger.Printf("rigctl: LOS response (may be empty): %q err=%v", resp, err)
		return nil
	}
	c.logger.Printf("rigctl: LOS response: %q", resp)
	return nil
}
